// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Client manages an OpenRGB connection, cached controller state, and queued writes.
// Create clients with NewClient and release them with Close.
type Client struct {
	mu             sync.RWMutex // Protects RGBcontrollers and devices maps
	socketMu       sync.Mutex   // Serializes socket writes and request/response pairing
	requestMu      sync.Mutex   // Serializes complete request/response transactions
	conn           *connection
	RGBControllers map[string]*RGBController // Cached controllers
	devices        map[string]*Device        // Cached devices
	name           string
	protocol       int
	queue          chan writeRequest
	// queueMu prevents QueueWrite from sending on the queue while Close closes it.
	queueMu sync.RWMutex
	queueWg sync.WaitGroup
	// asyncErrors stores failures from work that runs after the initiating method returns.
	asyncErrMu  sync.Mutex
	asyncErrors []error
	closeOnce   sync.Once
	closeErr    error
	plugins     map[string]*Plugin
	effects     map[string]*Effect
	// AutoRefresh refreshes controller state after state-changing queued writes.
	// It defaults to true; set it to false when the caller manages refreshes explicitly.
	AutoRefresh     bool
	done            chan struct{}
	incomingPackets chan incomingPacket
	serverUpdates   chan *PacketHeader
	pendingRequest  *pendingRequest
	pendingMu       sync.Mutex
	refreshMu       sync.Mutex
	refreshTimer    *time.Timer
	refreshTimers   map[uint32]*time.Timer
	refreshWg       sync.WaitGroup
}

func (c *Client) requireProtocol(minVersion int, feature string) error {
	if c.protocol < minVersion {
		return fmt.Errorf("%s is not supported in protocol version %d", feature, c.protocol)
	}
	return nil
}

type incomingPacket struct {
	header  *PacketHeader
	payload []byte
}

type pendingRequest struct {
	expected PacketType
	response chan incomingPacket
}

type writeRequest struct {
	controller_id uint32
	packetType    PacketType
	payload       []byte
	skipRefresh   bool
}

// AutoRefreshRequestTypes lists packet types that should trigger an automatic
// refresh of the device list when received.
var AutoRefreshRequestTypes = []PacketType{
	PKT_RGBCONTROLLER_UPDATELEDS,
	PKT_RGBCONTROLLER_UPDATEZONELEDS,
	PKT_RGBCONTROLLER_UPDATESINGLELED,
	PKT_RGBCONTROLLER_SETCUSTOMMODE,
	PKT_RGBCONTROLLER_SAVEMODE,
	PKT_RGBCONTROLLER_UPDATEMODE,
}

// writeQueueWorker processes queued writes and records failures for FlushWrites or Close.
func (c *Client) writeQueueWorker() {
	for req := range c.queue {
		if err := c.sendPacket(req.controller_id, req.packetType, req.payload); err != nil {
			c.recordAsyncError(fmt.Errorf("send packet for controller %d: %w", req.controller_id, err))
		}

		// Coalesce refreshes from bursts of writes to the same controller.
		if c.AutoRefresh && !req.skipRefresh {
			if contains(AutoRefreshRequestTypes, req.packetType) {
				c.scheduleWriteRefresh(req.controller_id)
			}
		}

		if req.packetType == PKT_RGBCONTROLLER_CLEAR_SEGMENTS {
			err := c.RescanDevices()
			if err != nil {
				c.recordAsyncError(fmt.Errorf("rescan devices after clearing segments: %w", err))
			}
		}
		c.queueWg.Done()
	}
}

// NewClient connects to an OpenRGB server, negotiates the protocol, and loads
// the initial controller and plugin caches when supported. Optional arguments
// may set the maximum protocol version (int) and client name (string).
func NewClient(host string, port string, args ...interface{}) (*Client, error) {
	protocol := 5
	clientName := "Golang - OpenRGB"

	for _, arg := range args {
		switch v := arg.(type) {
		case int:
			protocol = v
		case string:
			clientName = v
		default:
			return nil, fmt.Errorf("unsupported client option type %T", arg)
		}
	}

	if protocol < 0 || protocol > 5 {
		return nil, fmt.Errorf("unsupported protocol version %d", protocol)
	}

	conn, err := Connect(host, port)
	if err != nil {
		return nil, err
	}
	// Initialize the client with the connection and empty caches
	client := Client{
		conn:            conn,
		devices:         make(map[string]*Device),
		plugins:         make(map[string]*Plugin),
		queue:           make(chan writeRequest, 100),
		AutoRefresh:     true,
		done:            make(chan struct{}),
		incomingPackets: make(chan incomingPacket, 100),
		serverUpdates:   make(chan *PacketHeader, 100),
		refreshTimers:   make(map[uint32]*time.Timer),
		protocol:        protocol,
	}
	// Start the read loop and packet dispatcher
	go client.readLoop()
	go client.packetDispatcher()

	// Start the write queue worker
	go client.writeQueueWorker()

	// Set the client name and negotiate the protocol version
	if err := client.setName(clientName); err != nil {
		client.Close()
		return nil, fmt.Errorf("could not create client: %w", err)
	}
	if err := client.negotiateProtocol(protocol); err != nil {
		client.Close()
		return nil, fmt.Errorf("Could not create client: %w", err)
	}

	// Load the initial controller cache
	controllers, err := client.getControllers()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("Could not create client: %w", err)
	}
	client.RGBControllers = controllers
	// Load the plugin list if the protocol supports it
	if client.protocol >= 4 {
		if _, err := client.GetPluginList(); err != nil {
			client.Close()
			return nil, fmt.Errorf("could not create client: failed to load plugins: %w", err)
		}
	}
	return &client, nil
}

// setName sends a packet to the server to set the client name and updates the client's name field.
func (c *Client) setName(client_name string) error {
	name := []byte(client_name)
	if err := c.sendPacket(0, PKT_SET_CLIENT_NAME, name); err != nil {
		return fmt.Errorf("failed to set client name: %w", err)
	}
	c.name = client_name
	return nil
}

// Disconnect closes the underlying connection without draining queued writes.
// Prefer Close when the client owns asynchronous work.
func (c *Client) Disconnect() error {
	return c.conn.Disconnect()
}

// Close gracefully shuts down the client, waits for queued writes, and returns any
// asynchronous write or socket errors observed during shutdown.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.refreshMu.Lock()
		if c.refreshTimer != nil {
			c.refreshTimer.Stop()
			c.refreshTimer = nil
		}
		for controllerID, timer := range c.refreshTimers {
			if timer != nil {
				timer.Stop()
			}
			delete(c.refreshTimers, controllerID)
		}
		c.refreshMu.Unlock()

		// Prevent new queue submissions while closing the shutdown channels so the
		// queue never races with a concurrent enqueue. The queue is non-blocking
		// during enqueue operations, so this remains safe even when the queue is full.
		c.queueMu.Lock()
		select {
		case <-c.done:
		default:
			close(c.done)
		}
		close(c.queue)
		c.queueMu.Unlock()
		c.queueWg.Wait()
		c.refreshWg.Wait()
		c.closeErr = errors.Join(c.conn.Disconnect(), c.takeAsyncError())
	})
	return c.closeErr
}

// FlushWrites waits for all currently queued async writes to complete and returns
// any errors produced by those writes or their automatic refreshes.
// It does not close the client or the write queue.
func (c *Client) FlushWrites() error {
	c.queueWg.Wait()
	c.refreshWg.Wait()
	return c.takeAsyncError()
}

// recordAsyncError appends an error to the asyncErrors slice in a thread-safe manner.
func (c *Client) recordAsyncError(err error) {
	if err == nil {
		return
	}
	c.asyncErrMu.Lock()
	c.asyncErrors = append(c.asyncErrors, err)
	c.asyncErrMu.Unlock()
}

// takeAsyncError retrieves and clears the accumulated asynchronous errors in a thread-safe manner.
func (c *Client) takeAsyncError() error {
	c.asyncErrMu.Lock()
	defer c.asyncErrMu.Unlock()
	if len(c.asyncErrors) == 0 {
		return nil
	}
	err := errors.Join(c.asyncErrors...)
	c.asyncErrors = nil
	return err
}

// negotiateProtocol negotiates the protocol version with the server, using the provided maxProtocol as the upper limit.
func (c *Client) negotiateProtocol(maxProtocol int) error {
	if maxProtocol > 5 {
		return fmt.Errorf("maxProtocol %d is not supported, maximum supported protocol is 5", maxProtocol)
	}
	// send request for supported protocol version
	protocol := []byte{byte(maxProtocol), 0, 0, 0}
	_, payload, err := c.requestPacket(0, PKT_REQUEST_PROTOCOL_VERSION, protocol)
	if err != nil {
		return fmt.Errorf("failed to negotiate protocol version: %w", err)
	}

	// parse the server's response to determine the negotiated protocol version
	parser := newPacketDecoder(payload, 0)
	serverProtocol := int(parser.uint32())
	if err := parser.Err(); err != nil {
		return fmt.Errorf("invalid protocol response: %w", err)
	}
	// Set the client's protocol to the minimum of the server's supported version and the maxProtocol specified by the client.
	if serverProtocol >= maxProtocol {
		c.protocol = maxProtocol
	} else {
		c.protocol = serverProtocol
	}
	return nil
}

// GetControllers requests and returns all the controllers from the server.
func (c *Client) getControllers() (controllers map[string]*RGBController, err error) {
	controllers = make(map[string]*RGBController)
	controller_count, err := c.GetControllerCount()
	if err != nil {
		return controllers, err
	}
	for i := range controller_count {
		controller_data, err := c.GetControllerData(uint32(i))
		if err != nil {
			return controllers, fmt.Errorf("failed to get controller %d: %w", i, err)
		}
		controllers[controller_data.Name] = controller_data
	}
	if len(controllers) == 0 {
		return controllers, fmt.Errorf("no controllers found")
	}

	if len(controllers) != controller_count {
		return controllers, fmt.Errorf("controller count mismatch: expected %d, got %d. Last error: %v", controller_count, len(controllers), err)
	}
	return controllers, nil
}

// GetControllerCount returns the number of controllers currently known to the server.
func (c *Client) GetControllerCount() (controllers int, err error) {
	// send request for controller count
	_, payload, err := c.requestPacket(0, PKT_REQUEST_CONTROLLER_COUNT, nil)
	if err != nil {
		return 0, err
	}
	if len(payload) < 4 {
		return 0, fmt.Errorf("invalid controller count response: expected at least 4 bytes, got %d", len(payload))
	}
	controllers_data := binary.LittleEndian.Uint32(payload[:4])
	return int(controllers_data), nil
}

// GetControllerData requests and parses the current state of a controller.
func (c *Client) GetControllerData(controller_id uint32) (*RGBController, error) {

	// send request for controller count
	protocol := []byte{byte(c.protocol), 0, 0, 0}
	header, payload, err := c.requestPacket(controller_id, PKT_REQUEST_CONTROLLER_DATA, protocol)
	if err != nil {
		return nil, err
	}
	if PacketType(header.PktID) != PKT_REQUEST_CONTROLLER_DATA {
		return nil, fmt.Errorf("unexpected packet type: expected %d, got %d", PKT_REQUEST_CONTROLLER_DATA, header.PktID)
	}

	// Initialise RGBController struct and processor.
	factory := ControllerFactory{payload: payload, controller_id: int(controller_id), protocol: c.protocol}
	rgb_controller, err := factory.makeController()
	if err != nil {
		return nil, fmt.Errorf("failed to parse controller data for controller %d: %w", controller_id, err)
	}
	return &rgb_controller, err
}

// AddDevice creates and caches a Device for a discovered controller name.
func (c *Client) AddDevice(device_name string) (*Device, error) {
	controller, err := c.GetControllerByName(device_name)
	if err != nil {
		return nil, err
	}

	// Create a new Device instance and initialize its fields based on the controller data.
	device := Device{ControllerID: uint32(controller.ID), conn: c.conn, client: c}
	for i := range controller.Modes {
		if controller.Modes[i].id == controller.ActiveMode {
			device.Mode = controller.Modes[i].Name
			device.ModeIdx = i
			break
		}
	}

	c.mu.Lock()
	c.devices[device_name] = &device
	c.mu.Unlock()

	return &device, nil
}

// GetDevice returns a cached device, or creates and caches it if not found.
func (c *Client) GetDevice(device_name string) (*Device, error) {
	c.mu.RLock()
	device, ok := c.devices[device_name]
	c.mu.RUnlock()

	if ok {
		return device, nil
	}

	return c.AddDevice(device_name)
}

// GetDevicesByType returns all cached or created devices matching a controller type.
func (c *Client) GetDevicesByType(deviceType DeviceType) ([]*Device, error) {
	c.mu.RLock()
	controllerNames := make([]string, 0)
	// Collect controller names that match the specified device type.
	for name, ctrl := range c.RGBControllers {
		if ctrl.Type == deviceType {
			controllerNames = append(controllerNames, name)
		}
	}
	c.mu.RUnlock()

	if len(controllerNames) == 0 {
		return nil, fmt.Errorf("no devices found for type %d", deviceType)
	}

	// Create and cache devices for the matching controller names.
	devices := make([]*Device, 0, len(controllerNames))
	for _, name := range controllerNames {
		dev, err := c.GetDevice(name)
		if err != nil {
			return nil, err
		}
		devices = append(devices, dev)
	}
	return devices, nil
}

// GetDeviceByType returns the first device matching a controller type.
func (c *Client) GetDeviceByType(deviceType DeviceType) (*Device, error) {
	devices, err := c.GetDevicesByType(deviceType)
	if err != nil {
		return nil, err
	}
	return devices[0], nil
}

// ListDevices returns a copy of the cached device map.
func (c *Client) ListDevices() map[string]*Device {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Return a copy to prevent external modification
	devicesCopy := make(map[string]*Device, len(c.devices))
	for k, v := range c.devices {
		devicesCopy[k] = v
	}
	return devicesCopy
}

// AddAllDevices creates and caches devices for all discovered controllers.
func (c *Client) AddAllDevices() error {
	c.mu.RLock()
	controller_names := make([]string, 0, len(c.RGBControllers))
	for controller_name := range c.RGBControllers {
		controller_names = append(controller_names, controller_name)
	}
	c.mu.RUnlock()

	for _, controller_name := range controller_names {
		_, err := c.AddDevice(controller_name)
		if err != nil {
			return fmt.Errorf("failed to add device %q: %w", controller_name, err)
		}
	}
	return nil
}

// GetController returns a cached controller by ID.
func (c *Client) GetController(controller_id uint32) (*RGBController, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, ctrl := range c.RGBControllers {
		if ctrl.ID == int(controller_id) {
			return ctrl, nil
		}
	}
	return nil, fmt.Errorf("controller %d not found", controller_id)
}

// GetControllerByName returns a cached controller by name.
func (c *Client) GetControllerByName(name string) (*RGBController, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if ctrl, ok := c.RGBControllers[name]; ok {
		return ctrl, nil
	}
	return nil, fmt.Errorf("controller %q not found", name)
}

// RefreshController refreshes one controller's cached state from the server.
func (c *Client) RefreshController(controller_id uint32) error {
	updatedController, err := c.GetControllerData(controller_id)
	if err != nil {
		return err
	}

	// Update the controller in the map with write lock
	c.mu.Lock()
	c.RGBControllers[updatedController.Name] = updatedController
	c.mu.Unlock()

	return nil
}

// RefreshAllControllers refreshes all controllers and replaces the cached controller map.
func (c *Client) RefreshAllControllers() error {
	controllers, err := c.getControllers()
	if err != nil {
		return fmt.Errorf("failed to refresh controllers: %w", err)
	}

	// Update the controllers map with write lock
	c.mu.Lock()
	c.RGBControllers = controllers
	c.mu.Unlock()

	return nil
}

// GetProfiles returns the profiles available on the server.
func (c *Client) GetProfiles() ([]string, error) {
	if err := c.requireProtocol(2, "GetProfiles"); err != nil {
		return nil, err
	}
	_, payload, err := c.requestPacket(0, PKT_REQUEST_PROFILE_LIST, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get profiles: %w", err)
	}

	parser := newPacketDecoder(payload, 0)
	dataLength := parser.uint32()
	if int(dataLength) != len(payload) {
		return nil, fmt.Errorf("invalid profile list data length: expected %d, got %d", dataLength, len(payload))
	}
	profileCount := parser.uint16()
	profiles := make([]string, profileCount)
	for i := range profileCount {
		profiles[i] = parser.varStr()
	}
	if err := parser.Err(); err != nil {
		return nil, fmt.Errorf("invalid profile list: %w", err)
	}
	return profiles, nil
}

// LoadProfile loads the specified profile on the server and refreshes the controller state.
func (c *Client) LoadProfile(profileName string) error {
	if err := c.requireProtocol(2, "LoadProfile"); err != nil {
		return err
	}
	payload := []byte(profileName)
	if err := c.sendPacket(0, PKT_REQUEST_LOAD_PROFILE, payload); err != nil {
		return err
	}
	if err := c.RefreshAllControllers(); err != nil {
		return fmt.Errorf("failed to refresh controllers after loading profile: %w", err)
	}
	return nil
}

// SaveProfile saves the current state of all controllers to the specified profile on the server.
func (c *Client) SaveProfile(profileName string) error {
	if err := c.requireProtocol(2, "SaveProfile"); err != nil {
		return err
	}
	payload := []byte(profileName)
	if err := c.sendPacket(0, PKT_REQUEST_SAVE_PROFILE, payload); err != nil {
		return err
	}
	return nil
}

// DeleteProfile deletes the specified profile from the server.
func (c *Client) DeleteProfile(profileName string) error {
	if err := c.requireProtocol(2, "DeleteProfile"); err != nil {
		return err
	}
	payload := []byte(profileName)
	if err := c.sendPacket(0, PKT_REQUEST_DELETE_PROFILE, payload); err != nil {
		return err
	}
	return nil
}

// RescanDevices asks the server to rediscover devices and refreshes controller state.
func (c *Client) RescanDevices() error {
	if err := c.requireProtocol(5, "Rescan"); err != nil {
		return err
	}

	if err := c.sendPacket(0, PKT_REQUEST_RESCAN_DEVICES, nil); err != nil {
		return fmt.Errorf("failed to request device rescan: %w", err)
	}

	// Wait for the server to announce the start of the rescan.
	if _, _, err := c.waitForPacket(PKT_DEVICE_LIST_UPDATED, time.Second*300); err != nil {
		return fmt.Errorf("failed to receive device rescan start update: %w", err)
	}

	// Wait for the server to announce the completion of the rescan.
	if _, _, err := c.waitForPacket(PKT_DEVICE_LIST_UPDATED, time.Second*300); err != nil {
		return fmt.Errorf("failed to receive device rescan completion update: %w", err)
	}

	time.Sleep(5 * time.Second) // Give the server a moment to complete the rescan before refreshing controllers
	// Refresh all controllers after rescan
	if err := c.RefreshAllControllers(); err != nil {
		return fmt.Errorf("failed to refresh controllers after rescan: %w", err)
	}

	return nil
}

// readLoop continuously reads packets from the connection and sends them to the incomingPackets channel.
func (c *Client) readLoop() {
	defer close(c.incomingPackets)

	for {
		select {
		case <-c.done:
			return
		default:
		}

		header, payload, err := c.conn.ReadPacket(2 * time.Second)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			select {
			case <-c.done:
				return
			default:
			}
			c.recordAsyncError(fmt.Errorf("socket read: %w", err))
			return
		}

		packet := incomingPacket{header: header, payload: payload}
		select {
		case c.incomingPackets <- packet:
		case <-c.done:
			return
		}
	}
}

// packetDispatcher processes incoming packets and dispatches them to the appropriate handlers or pending requests.
func (c *Client) packetDispatcher() {
	defer close(c.serverUpdates)
	for packet := range c.incomingPackets {
		if packet.header == nil {
			continue
		}

		if PacketType(packet.header.PktID) == PKT_DEVICE_LIST_UPDATED {
			select {
			case c.serverUpdates <- packet.header:
			default:
			}
			c.handleServerUpdatePacket(packet.header)
			continue
		}

		c.pendingMu.Lock()
		waiter := c.pendingRequest
		if waiter != nil && waiter.expected == PacketType(packet.header.PktID) {
			c.pendingRequest = nil
			c.pendingMu.Unlock()
			select {
			case waiter.response <- packet:
				continue
			case <-c.done:
				continue
			}
		}
		c.pendingMu.Unlock()
	}
}

// waitForPacket waits for a specific packet type to be received, with a timeout.
func (c *Client) waitForPacket(packetType PacketType, timeout time.Duration) (*PacketHeader, []byte, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case header, ok := <-c.serverUpdates:
		if !ok || header == nil {
			return nil, nil, fmt.Errorf("connection closed while waiting for packet %d", packetType)
		}
		if PacketType(header.PktID) != packetType {
			return nil, nil, fmt.Errorf("unexpected packet type: expected %d, got %d", packetType, header.PktID)
		}
		return header, nil, nil
	case <-c.done:
		return nil, nil, fmt.Errorf("client closed")
	case <-timer.C:
		return nil, nil, fmt.Errorf("timed out waiting for packet %d", packetType)
	}
}

// handleServerUpdatePacket processes server update packets, such as device list updates, and schedules a controller refresh if needed.
func (c *Client) handleServerUpdatePacket(header *PacketHeader) {
	if header == nil {
		return
	}

	if header.PktID == uint32(PKT_DEVICE_LIST_UPDATED) {
		c.scheduleControllerRefresh()
	}
}

// scheduleWriteRefresh schedules a short, per-controller refresh debounce for
// state-changing writes. A burst of writes therefore produces one controller
// request while preserving the latest cached state after the burst settles.
func (c *Client) scheduleWriteRefresh(controllerID uint32) {
	const debounceWindow = 50 * time.Millisecond

	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	if c.refreshTimers == nil {
		c.refreshTimers = make(map[uint32]*time.Timer)
	}
	if timer := c.refreshTimers[controllerID]; timer != nil {
		timer.Stop()
		timer.Reset(debounceWindow)
		return
	}

	c.refreshWg.Add(1)
	c.refreshTimers[controllerID] = time.AfterFunc(debounceWindow, func() {
		c.refreshMu.Lock()
		delete(c.refreshTimers, controllerID)
		c.refreshMu.Unlock()
		defer c.refreshWg.Done()

		select {
		case <-c.done:
			return
		default:
		}

		if err := c.RefreshController(controllerID); err != nil {
			c.recordAsyncError(fmt.Errorf("refresh controller %d: %w", controllerID, err))
		}
	})
}

// scheduleControllerRefresh schedules a refresh of all controllers after a debounce window to avoid multiple rapid refreshes.
func (c *Client) scheduleControllerRefresh() {
	const debounceWindow = 5000 * time.Millisecond

	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	if c.refreshTimer != nil {
		c.refreshTimer.Stop()
	}

	var timer *time.Timer
	timer = time.AfterFunc(debounceWindow, func() {
		select {
		case <-c.done:
			return
		default:
		}

		if err := c.RefreshAllControllers(); err != nil {
			c.recordAsyncError(fmt.Errorf("refresh controllers after device list update: %w", err))
			return
		}

		c.refreshMu.Lock()
		if c.refreshTimer == timer {
			c.refreshTimer = nil
		}
		c.refreshMu.Unlock()
	})
	c.refreshTimer = timer
}

// QueueWrite enqueues a packet write to be processed asynchronously. The returned
// error only covers enqueueing; call FlushWrites to observe eventual send or
// refresh errors. Automatic refreshes are coalesced per controller unless
// SkipRefresh is set.
func (c *Client) QueueWrite(controller_id uint32, packetType PacketType, payload []byte, opts ...QueueWriteOptions) error {
	options := QueueWriteOptions{}
	if len(opts) > 0 {
		options = opts[0]
	}

	c.queueMu.Lock()
	defer c.queueMu.Unlock()
	select {
	case <-c.done:
		return fmt.Errorf("client is closed")
	default:
	}

	c.queueWg.Add(1)
	select {
	case c.queue <- writeRequest{
		controller_id: controller_id,
		packetType:    packetType,
		payload:       payload,
		skipRefresh:   options.SkipRefresh,
	}:
		return nil
	default:
		c.queueWg.Done()
		return fmt.Errorf("write queue is full")
	}
}
