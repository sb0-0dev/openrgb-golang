// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

// Client manages an OpenRGB connection, cached controller state, and queued writes.
// Create clients with NewClient and release them with Close.
type Client struct {
	mu             sync.RWMutex // Protects rgbControllers and devices maps
	socketMu       sync.Mutex   // Serializes writes to the shared connection.
	requestMu      sync.Mutex   // Allows only one request/response transaction at a time.
	conn           *connection
	rgbControllers map[string]*RGBController // Cached controllers
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
	warningMu   sync.RWMutex
	warnings    []error
	plugins     map[string]*Plugin
	effects     map[string]*Effect
	// AutoRefresh refreshes controller state after state-changing queued writes.
	// It defaults to true; set it to false when the caller manages refreshes explicitly.
	AutoRefresh     bool
	done            chan struct{}
	incomingPackets chan incomingPacket
	serverUpdates   chan *PacketHeader
	// pendingRequest is the sole in-flight request; the protocol has no request IDs.
	pendingRequest *pendingRequest
	pendingMu      sync.Mutex
	// refreshMu protects both refresh timers and their coordination during Close.
	refreshMu     sync.Mutex
	refreshTimer  *time.Timer
	refreshTimers map[uint32]*time.Timer
	refreshWg     sync.WaitGroup
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

// pendingRequest routes the next packet of the expected type to its waiter.
type pendingRequest struct {
	expected PacketType
	response chan incomingPacket
}

// writeRequest is consumed by the background worker; errors after enqueueing
// are recorded for FlushWrites and Close rather than returned to the caller.
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

// minWriteInterval caps queued writes at about 60 per second so a burst cannot overload the server or RGB hardware.
const minWriteInterval = 17 * time.Millisecond

// writeQueueWorker processes queued writes and records failures for FlushWrites or Close.
func (c *Client) writeQueueWorker() {
	// The zero lastSend makes time.Since return a large value, so the first write is not delayed.
	var lastSend time.Time
	for req := range c.queue {
		if wait := minWriteInterval - time.Since(lastSend); wait > 0 {
			time.Sleep(wait)
		}
		err := c.sendPacket(req.controller_id, req.packetType, req.payload)
		lastSend = time.Now()
		if err != nil {
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

	// Set the client name
	if err := client.setName(clientName); err != nil {
		client.Close()
		return nil, fmt.Errorf("could not create client: %w", err)
	}
	serverProtocol, capabilityErr := client.setupClientCapabilities()
	// Negotiate the protocol version with the server
	negotiatedServerProtocol, err := client.negotiateProtocol(protocol)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("could not create client: %w", err)
	}
	if capabilityErr != nil && (serverProtocol > 5 || negotiatedServerProtocol > 5) {
		client.addWarning(fmt.Errorf("profile manager capability setup failed: %w", capabilityErr))
	}

	// Load the initial controller cache
	controllers, err := client.getControllers()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("Could not create client: %w", err)
	}
	client.rgbControllers = controllers
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
			if timer != nil && timer.Stop() {
				c.refreshWg.Done()
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

func (c *Client) FlushWrites() error {
	// Serialize against QueueWrite and Close so no new work can be
	// added while Wait observes a zero counter.
	c.queueMu.Lock()
	c.queueWg.Wait()
	c.refreshWg.Wait()
	c.queueMu.Unlock()
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

// Warnings returns a snapshot of non-fatal issues encountered while setting
// up optional server capabilities. Callers can inspect these after NewClient
// succeeds, for example to report that profile management may be unavailable.
func (c *Client) Warnings() []error {
	c.warningMu.RLock()
	defer c.warningMu.RUnlock()
	return append([]error(nil), c.warnings...)
}

func (c *Client) addWarning(warning error) {
	if warning == nil {
		return
	}
	c.warningMu.Lock()
	c.warnings = append(c.warnings, warning)
	c.warningMu.Unlock()
}

// setClientFlags sends the client flags to the server.
func (c *Client) setClientFlags(flags uint32) error {
	payload := make([]byte, 4)
	binary.LittleEndian.PutUint32(payload, flags)

	// TODO: fire and forget the client flags to the server for now, should
	// eventually handle the server's response to confirm the flags were set correctly.
	if err := c.sendPacket(0, PKT_SET_CLIENT_FLAGS, payload); err != nil {
		return err
	}

	return nil
}

// requestServerProtocolVersion asks the server for its supported protocol version.
func (c *Client) requestServerProtocolVersion(maxProtocol int) (int, error) {
	protocol := []byte{byte(maxProtocol), 0, 0, 0}
	_, payload, err := c.requestPacket(0, PKT_REQUEST_PROTOCOL_VERSION, protocol)
	if err != nil {
		return 0, fmt.Errorf("failed to negotiate protocol version: %w", err)
	}
	parser := newPacketDecoder(payload, 0)
	serverProtocol := int(parser.uint32())
	if err := parser.Err(); err != nil {
		return 0, fmt.Errorf("invalid protocol response: %w", err)
	}
	return serverProtocol, nil
}

// negotiateProtocol stores the server protocol capped at the client's maximum.
func (c *Client) negotiateProtocol(maxProtocol int) (int, error) {
	if maxProtocol > 5 {
		return 0, fmt.Errorf("maxProtocol %d is not supported, maximum supported protocol is 5", maxProtocol)
	}
	serverProtocol, err := c.requestServerProtocolVersion(maxProtocol)
	if err != nil {
		return 0, err
	}
	if serverProtocol >= maxProtocol {
		c.protocol = maxProtocol
	} else {
		c.protocol = serverProtocol
	}
	return serverProtocol, nil
}

// setupClientCapabilities uses the protocol 6 handshake required by some
// OpenRGB versions before they accept client flags for profile management.
// It sends no flags when the server reports protocol 5 or lower.
func (c *Client) setupClientCapabilities() (int, error) {
	serverProtocol, err := c.requestServerProtocolVersion(6)
	if err != nil || serverProtocol <= 5 {
		return serverProtocol, err
	}
	flags := NET_CLIENT_FLAG_SUPPORTS_RGBCONTROLLER |
		NET_CLIENT_FLAG_SUPPORTS_LOGMANAGER |
		NET_CLIENT_FLAG_SUPPORTS_PROFILEMANAGER |
		NET_CLIENT_FLAG_SUPPORTS_PLUGINMANAGER |
		NET_CLIENT_FLAG_SUPPORTS_SETTINGSMANAGER |
		NET_CLIENT_FLAG_REQUEST_LOCAL_CLIENT
	if err := c.setClientFlags(flags); err != nil {
		return serverProtocol, err
	}
	return serverProtocol, nil
}

// GetControllers requests and returns all the controllers from the server.
func (c *Client) getControllers() (controllers map[string]*RGBController, err error) {
	controllers = make(map[string]*RGBController)
	controller_count, err := c.GetControllerCount()
	if err != nil {
		return controllers, err
	}
	controllerList := make([]*RGBController, 0, controller_count)
	for i := range controller_count {
		controller_data, err := c.GetControllerData(uint32(i))
		if err != nil {
			return controllers, fmt.Errorf("failed to get controller %d: %w", i, err)
		}
		controllerList = append(controllerList, controller_data)
	}
	if len(controllerList) == 0 {
		return controllers, fmt.Errorf("no controllers found")
	}

	if len(controllerList) != controller_count {
		return controllers, fmt.Errorf("controller count mismatch: expected %d, got %d. Last error: %v", controller_count, len(controllerList), err)
	}

	return indexControllersByName(controllerList), nil
}

func indexControllersByName(controllerList []*RGBController) map[string]*RGBController {
	controllers := make(map[string]*RGBController, len(controllerList))
	nameCounts := make(map[string]int, len(controllerList))
	for _, controller := range controllerList {
		nameCounts[controller.Name]++
	}
	usedKeys := make(map[string]bool, len(controllerList))
	for name, count := range nameCounts {
		if count == 1 {
			usedKeys[name] = true
		}
	}
	nextDuplicateIndex := make(map[string]int, len(nameCounts))
	for _, controller := range controllerList {
		key := controller.Name
		if nameCounts[key] > 1 {
			key = uniqueNameKey(key, usedKeys, true, nextDuplicateIndex)
		}
		usedKeys[key] = true
		controllers[key] = controller
	}
	return controllers
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

// AddDevice creates and caches a Device for a uniquely named controller.
func (c *Client) AddDevice(device_name string) (*Device, error) {
	controller, err := c.GetControllerByName(device_name)
	if err != nil {
		return nil, err
	}
	return c.AddDeviceByID(uint32(controller.ID))
}

// AddDeviceByID creates and caches a Device for a controller ID.
func (c *Client) AddDeviceByID(controllerID uint32) (*Device, error) {
	controller, err := c.GetController(controllerID)
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
	defer c.mu.Unlock()
	for _, cachedDevice := range c.devices {
		if cachedDevice.ControllerID == controllerID {
			return cachedDevice, nil
		}
	}
	deviceKey := c.controllerKeyByIDLocked(controllerID)
	if deviceKey == "" {
		deviceKey = c.uniqueControllerKeyLocked(controller.Name)
	}
	c.devices[deviceKey] = &device

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

	controller, err := c.GetControllerByName(device_name)
	if err != nil {
		return nil, err
	}
	return c.GetDeviceByID(uint32(controller.ID))
}

// GetDeviceByID returns the cached device for a controller ID, creating it if needed.
func (c *Client) GetDeviceByID(controllerID uint32) (*Device, error) {
	c.mu.RLock()
	for _, device := range c.devices {
		if device.ControllerID == controllerID {
			c.mu.RUnlock()
			return device, nil
		}
	}
	c.mu.RUnlock()
	return c.AddDeviceByID(controllerID)
}

// GetDevicesByType returns all cached or created devices matching a controller type.
func (c *Client) GetDevicesByType(deviceType DeviceType) ([]*Device, error) {
	c.mu.RLock()
	controllerIDs := make([]uint32, 0)
	for _, ctrl := range c.rgbControllers {
		if ctrl.Type == deviceType {
			controllerIDs = append(controllerIDs, uint32(ctrl.ID))
		}
	}
	c.mu.RUnlock()

	if len(controllerIDs) == 0 {
		return nil, fmt.Errorf("no devices found for type %d", deviceType)
	}

	devices := make([]*Device, 0, len(controllerIDs))
	for _, controllerID := range controllerIDs {
		dev, err := c.GetDeviceByID(controllerID)
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

// ListControllers returns a shadow copy of the cached controller map.
func (c *Client) ListControllers() map[string]*RGBController {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*RGBController, len(c.rgbControllers))
	for name, controller := range c.rgbControllers {
		result[name] = cloneRGBController(controller)
	}

	return result
}

// cloneRGBController returns an independent copy of a controller.
func cloneRGBController(src *RGBController) *RGBController {
	if src == nil {
		return nil
	}

	// Copy scalar fields and preserve private fields.
	dst := *src

	// Copy slices.
	dst.Modes = cloneSlice(src.Modes)
	dst.Zones = cloneSlice(src.Zones)
	dst.LEDs = cloneSlice(src.LEDs)
	dst.Colors = cloneSlice(src.Colors)
	dst.AltLedNames = cloneSlice(src.AltLedNames)

	// Deep-copy nested mode colors.
	for i := range dst.Modes {
		dst.Modes[i].Colors = cloneSlice(src.Modes[i].Colors)
	}

	// Deep-copy nested zone data.
	for i := range dst.Zones {
		dst.Zones[i].segmentData = cloneSlice(src.Zones[i].segmentData)

		if src.Zones[i].matrixData != nil {
			dst.Zones[i].matrixData = make([][]int32, len(src.Zones[i].matrixData))
			for j := range src.Zones[i].matrixData {
				dst.Zones[i].matrixData[j] = cloneSlice(src.Zones[i].matrixData[j])
			}
		}
	}

	return &dst
}

// AddAllDevices creates and caches devices for all discovered controllers.
func (c *Client) AddAllDevices() error {
	c.mu.RLock()
	controllerIDs := make([]uint32, 0, len(c.rgbControllers))
	for _, controller := range c.rgbControllers {
		controllerIDs = append(controllerIDs, uint32(controller.ID))
	}
	c.mu.RUnlock()

	for _, controllerID := range controllerIDs {
		if _, err := c.AddDeviceByID(controllerID); err != nil {
			return fmt.Errorf("failed to add device for controller %d: %w", controllerID, err)
		}
	}

	return nil
}

// GetController returns a snapshot of a cached controller by ID.
func (c *Client) GetController(controllerID uint32) (*RGBController, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, ctrl := range c.rgbControllers {
		if ctrl.ID == int(controllerID) {
			return cloneRGBController(ctrl), nil
		}
	}

	return nil, fmt.Errorf("controller %d not found", controllerID)
}

// GetControllerByName returns a snapshot of a cached controller by name.
func (c *Client) GetControllerByName(name string) (*RGBController, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var match *RGBController
	for _, controller := range c.rgbControllers {
		if controller.Name != name {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("controller name %q is ambiguous; use GetController with a controller ID", name)
		}
		match = controller
	}
	if match == nil {
		match = c.rgbControllers[name]
	}
	if match == nil {
		return nil, fmt.Errorf("controller %q not found", name)
	}
	return cloneRGBController(match), nil
}

// controllerKeyByIDLocked returns the key of the controller with the given ID, or an empty string if not found.
func (c *Client) controllerKeyByIDLocked(controllerID uint32) string {
	for key, controller := range c.rgbControllers {
		if uint32(controller.ID) == controllerID {
			return key
		}
	}
	return ""
}

// uniqueNameKey returns an unused map key for name. When forceSuffix is true,
// the returned key uses the "name - N" convention even when the base is free.
func uniqueNameKey[V any](name string, used map[string]V, forceSuffix bool, nextIndex map[string]int) string {
	if _, exists := used[name]; !forceSuffix && !exists {
		return name
	}
	index := nextIndex[name] + 1
	for {
		if _, exists := used[fmt.Sprintf("%s - %d", name, index)]; !exists {
			break
		}
		index++
	}
	nextIndex[name] = index
	return fmt.Sprintf("%s - %d", name, index)
}

// uniqueControllerKeyLocked generates a unique key for a controller based on its name.
func (c *Client) uniqueControllerKeyLocked(name string) string {
	return uniqueNameKey(name, c.rgbControllers, false, make(map[string]int, 1))
}

// reindexControllersLocked reindexes the controllers map by sorting them by ID and regenerating the keys.
func (c *Client) reindexControllersLocked() {
	controllerList := make([]*RGBController, 0, len(c.rgbControllers))
	for _, controller := range c.rgbControllers {
		controllerList = append(controllerList, controller)
	}
	sort.Slice(controllerList, func(i, j int) bool {
		return controllerList[i].ID < controllerList[j].ID
	})
	c.rgbControllers = indexControllersByName(controllerList)
}

// reindexDevicesLocked reindexes the devices map based on the current controller keys.
func (c *Client) reindexDevicesLocked() {
	devices := make(map[string]*Device, len(c.devices))
	usedKeys := make(map[string]bool, len(c.devices))
	nextDuplicateIndex := make(map[string]int, len(c.devices))
	for oldKey, device := range c.devices {
		key := c.controllerKeyByIDLocked(device.ControllerID)
		if key == "" {
			key = oldKey
		}
		key = uniqueNameKey(key, usedKeys, false, nextDuplicateIndex)
		usedKeys[key] = true
		devices[key] = device
	}
	c.devices = devices
}

// RefreshController refreshes one controller's cached state from the server.
func (c *Client) RefreshController(controller_id uint32) error {
	updatedController, err := c.GetControllerData(controller_id)
	if err != nil {
		return err
	}

	// Update the controller in the map with write lock
	c.mu.Lock()
	key := c.controllerKeyByIDLocked(controller_id)
	if key == "" {
		key = c.uniqueControllerKeyLocked(updatedController.Name)
	}
	c.rgbControllers[key] = updatedController
	c.reindexControllersLocked()
	c.reindexDevicesLocked()
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
	c.rgbControllers = controllers
	c.reindexDevicesLocked()
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

	parser, err := lengthPrefixedDecoder(payload, 0, "profile list")
	if err != nil {
		return nil, err
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

// readLoop continuously reads packets from the connection and sends them to the
// dispatcher. Periodic read deadlines let it notice shutdown even when the
// server has nothing to send.
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

// packetDispatcher separates unsolicited server updates from replies to
// synchronous requests. Non-update packets that do not match the pending
// request are ignored.
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
		payload:       append([]byte(nil), payload...),
		skipRefresh:   options.SkipRefresh,
	}:
		return nil
	default:
		c.queueWg.Done()
		return fmt.Errorf("write queue is full")
	}
}
