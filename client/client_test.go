// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestNewClientLoadsInitialControllerCache verifies that NewClient populates the RGBControllers cache with the discovered controller data.
func TestNewClientLoadsInitialControllerCache(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}

	releaseServer := make(chan struct{})
	var releaseServerOnce sync.Once
	release := func() { releaseServerOnce.Do(func() { close(releaseServer) }) }
	defer release()
	serverDone := make(chan error, 1)
	go func() {
		server, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer server.Close()
		serverConnection := &connection{conn: server}
		readRequest := func(packetType PacketType, controllerID uint32, payload []byte) error {
			header, gotPayload, err := serverConnection.ReadPacket(5 * time.Second)
			if err != nil {
				return err
			}
			if PacketType(header.PktID) != packetType || header.DevIdx != controllerID || string(gotPayload) != string(payload) {
				return fmt.Errorf("request = (%d, %d, %v), want (%d, %d, %v)", header.PktID, header.DevIdx, gotPayload, packetType, controllerID, payload)
			}
			return nil
		}
		sendResponse := func(packetType PacketType, payload []byte) error {
			return serverConnection.SendPacket(0, uint32(packetType), payload)
		}

		if err := readRequest(PKT_SET_CLIENT_NAME, 0, []byte("Unit Test Client")); err != nil {
			serverDone <- err
			return
		}
		if err := readRequest(PKT_REQUEST_PROTOCOL_VERSION, 0, []byte{6, 0, 0, 0}); err != nil {
			serverDone <- err
			return
		}
		protocolResponse := newPacketEncoder(0)
		protocolResponse.uint32(5)
		if err := sendResponse(PKT_REQUEST_PROTOCOL_VERSION, protocolResponse.getPayload()); err != nil {
			serverDone <- err
			return
		}
		if err := readRequest(PKT_REQUEST_PROTOCOL_VERSION, 0, []byte{5, 0, 0, 0}); err != nil {
			serverDone <- err
			return
		}
		if err := sendResponse(PKT_REQUEST_PROTOCOL_VERSION, protocolResponse.getPayload()); err != nil {
			serverDone <- err
			return
		}
		if err := readRequest(PKT_REQUEST_CONTROLLER_COUNT, 0, nil); err != nil {
			serverDone <- err
			return
		}
		countResponse := newPacketEncoder(0)
		countResponse.uint32(1)
		if err := sendResponse(PKT_REQUEST_CONTROLLER_COUNT, countResponse.getPayload()); err != nil {
			serverDone <- err
			return
		}
		if err := readRequest(PKT_REQUEST_CONTROLLER_DATA, 0, []byte{5, 0, 0, 0}); err != nil {
			serverDone <- err
			return
		}
		if err := sendResponse(PKT_REQUEST_CONTROLLER_DATA, testControllerDataPayload()); err != nil {
			serverDone <- err
			return
		}
		if err := readRequest(PKT_REQUEST_PLUGIN_LIST, 0, nil); err != nil {
			serverDone <- err
			return
		}
		pluginResponse := newPacketEncoder(0)
		pluginResponse.uint32(6)
		pluginResponse.uint16(0)
		if err := sendResponse(PKT_REQUEST_PLUGIN_LIST, pluginResponse.getPayload()); err != nil {
			serverDone <- err
			return
		}
		<-releaseServer
		serverDone <- nil
	}()
	client, err := NewClient(host, port, "Unit Test Client")
	if err != nil {
		release()
		t.Fatalf("NewClient returned error: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close returned error: %v", err)
		}
		release()
		if err := <-serverDone; err != nil {
			t.Errorf("fake server failed: %v", err)
		}
	}()

	if client.name != "Unit Test Client" || client.protocol != 5 {
		t.Fatalf("client name/protocol = %q/%d, want %q/5", client.name, client.protocol, "Unit Test Client")
	}

	controllers := client.ListControllers()
	controller, ok := controllers["Test Keyboard"]
	if !ok {
		t.Fatal("NewClient did not cache the discovered controller")
	}
	if controller.ID != 0 || controller.Type != KEYBOARD || len(controller.LEDs) != 1 || controller.LEDs[0].Name != "Test LED" {
		t.Fatalf("unexpected cached controller: %+v", controller)
	}
	if client.plugins == nil || len(client.plugins) != 0 {
		t.Fatalf("unexpected plugin cache: %v", client.plugins)
	}
}

// TestQueueWriteDoesNotBlockCloseWhenQueueIsFull protects shutdown from a
// concurrent enqueue that cannot fit in the full, non-blocking write queue.
func TestQueueWriteDoesNotBlockCloseWhenQueueIsFull(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()

	client := &Client{
		conn:            &connection{conn: clientConn},
		queue:           make(chan writeRequest, 1),
		done:            make(chan struct{}),
		incomingPackets: make(chan incomingPacket, 1),
		serverUpdates:   make(chan *PacketHeader, 1),
		refreshTimers:   make(map[uint32]*time.Timer),
	}
	client.queue <- writeRequest{}

	writeErr := make(chan error, 1)
	go func() {
		writeErr <- client.QueueWrite(1, PKT_RGBCONTROLLER_UPDATELEDS, nil)
	}()

	select {
	case err := <-writeErr:
		if err == nil {
			t.Fatal("QueueWrite returned nil when queue was full")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("QueueWrite blocked when the queue was full")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- client.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close() blocked while the queue was full")
	}
}

// TestCloseStopsPendingRefreshTimers verifies Close accounts for a stopped
// timer's wait-group entry instead of waiting for its callback to run.
func TestCloseStopsPendingRefreshTimers(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()

	client := &Client{
		conn:          &connection{conn: clientConn},
		queue:         make(chan writeRequest, 1),
		done:          make(chan struct{}),
		refreshTimers: make(map[uint32]*time.Timer),
	}
	timer := time.NewTimer(time.Hour)
	client.refreshTimers[7] = timer
	client.refreshWg.Add(1)

	closeResult := make(chan error, 1)
	go func() {
		closeResult <- client.Close()
	}()
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close() returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() blocked waiting for a stopped refresh timer")
	}
	if _, ok := client.refreshTimers[7]; ok {
		t.Fatal("Close() left stale refresh timers behind")
	}
	if timer.Stop() {
		t.Fatal("Close() left the refresh timer active")
	}
}

// TestQueueWriteThrottlesSendsToMinWriteInterval verifies that queued writes are
// sent no closer together than minWriteInterval.
func TestQueueWriteThrottlesSendsToMinWriteInterval(t *testing.T) {
	client, server := newTestClient(t, 5)
	const writes = 4
	for i := 0; i < writes; i++ {
		if err := client.QueueWrite(1, PKT_RGBCONTROLLER_UPDATELEDS, []byte{byte(i)}); err != nil {
			t.Fatalf("QueueWrite(%d) returned error: %v", i, err)
		}
	}

	// net.Pipe is synchronous, so each read completes when the worker sends the packet.
	arrivals := make([]time.Time, 0, writes)
	for i := 0; i < writes; i++ {
		readQueuedPacket(t, server)
		arrivals = append(arrivals, time.Now())
	}

	// Allow a small margin for timer and scheduling jitter in the read timestamps.
	const tolerance = 2 * time.Millisecond
	for i := 1; i < len(arrivals); i++ {
		if gap := arrivals[i].Sub(arrivals[i-1]); gap < minWriteInterval-tolerance {
			t.Fatalf("gap between writes %d and %d = %v, want at least %v", i-1, i, gap, minWriteInterval)
		}
	}
}

// TestQueueWriteSupportsSkipRefreshOption verifies that the SkipRefresh option is honored when queuing a write operation.
func TestQueueWriteSupportsSkipRefreshOption(t *testing.T) {
	client := &Client{queue: make(chan writeRequest, 1), done: make(chan struct{})}
	if err := client.QueueWrite(7, PKT_RGBCONTROLLER_UPDATELEDS, []byte{1, 2, 3}, QueueWriteOptions{SkipRefresh: true}); err != nil {
		t.Fatal(err)
	}
	request := <-client.queue
	defer client.queueWg.Done()
	if request.controller_id != 7 || request.packetType != PKT_RGBCONTROLLER_UPDATELEDS {
		t.Fatalf("unexpected queued packet: controller=%d packet=%d", request.controller_id, request.packetType)
	}
	if string(request.payload) != string([]byte{1, 2, 3}) {
		t.Fatalf("unexpected payload: %v", request.payload)
	}
	if !request.skipRefresh {
		t.Fatal("QueueWrite did not propagate SkipRefresh to the queued request")
	}
}

// TestWriteRefreshesCoalesceForController verifies that repeated writes to one
// controller produce only one debounced refresh request.
func TestWriteRefreshesCoalesceForController(t *testing.T) {
	server, clientConn := net.Pipe()
	client := &Client{
		conn:            &connection{conn: clientConn},
		done:            make(chan struct{}),
		incomingPackets: make(chan incomingPacket, 1),
		serverUpdates:   make(chan *PacketHeader, 1),
		protocol:        5,
	}
	go client.readLoop()
	go client.packetDispatcher()
	defer func() {
		close(client.done)
		clientConn.Close()
		server.Close()
	}()

	refreshCount := make(chan int, 1)
	go func() {
		count := 0
		serverConnection := &connection{conn: server}
		for {
			header, _, err := serverConnection.ReadPacket(100 * time.Millisecond)
			if err != nil {
				refreshCount <- count
				return
			}
			if PacketType(header.PktID) != PKT_REQUEST_CONTROLLER_DATA {
				continue
			}
			count++
			if err := serverConnection.SendPacket(header.DevIdx, header.PktID, nil); err != nil {
				refreshCount <- count
				return
			}
		}
	}()

	client.scheduleWriteRefresh(3)
	client.scheduleWriteRefresh(3)
	if err := client.FlushWrites(); err == nil || !strings.Contains(err.Error(), "refresh controller 3") {
		t.Fatalf("FlushWrites() error = %v, want refresh error from the single coalesced request", err)
	}
	select {
	case count := <-refreshCount:
		if count != 1 {
			t.Fatalf("controller refresh requests = %d, want 1", count)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not finish observing refresh requests")
	}
}

// TestRequestPacketSerializesConcurrentRequests ensures simultaneous callers
// cannot misassociate replies because protocol packets have no request ID.
func TestRequestPacketSerializesConcurrentRequests(t *testing.T) {
	server, clientConn := net.Pipe()
	client := &Client{
		conn:            &connection{conn: clientConn},
		done:            make(chan struct{}),
		incomingPackets: make(chan incomingPacket, 2),
		serverUpdates:   make(chan *PacketHeader, 2),
	}
	go client.readLoop()
	go client.packetDispatcher()

	serverDone := make(chan error, 1)
	go func() {
		for range 2 {
			header := make([]byte, 16)
			if _, err := io.ReadFull(server, header); err != nil {
				serverDone <- err
				return
			}
			payloadSize := binary.LittleEndian.Uint32(header[12:16])
			payload := make([]byte, payloadSize)
			if _, err := io.ReadFull(server, payload); err != nil {
				serverDone <- err
				return
			}
			response := &connection{conn: server}
			if err := response.SendPacket(
				binary.LittleEndian.Uint32(header[4:8]),
				binary.LittleEndian.Uint32(header[8:12]),
				[]byte{byte(header[8])},
			); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()

	requestTypes := []PacketType{PKT_REQUEST_PROFILE_LIST, PKT_REQUEST_LOAD_PROFILE}
	results := make(chan struct {
		packetType PacketType
		header     *PacketHeader
		payload    []byte
		err        error
	}, len(requestTypes))
	var waitGroup sync.WaitGroup
	for _, packetType := range requestTypes {
		waitGroup.Add(1)
		go func(packetType PacketType) {
			defer waitGroup.Done()
			header, payload, err := client.requestPacket(0, packetType, nil)
			results <- struct {
				packetType PacketType
				header     *PacketHeader
				payload    []byte
				err        error
			}{packetType, header, payload, err}
		}(packetType)
	}
	waitGroup.Wait()
	close(results)

	for result := range results {
		if result.err != nil {
			t.Fatalf("request %d failed: %v", result.packetType, result.err)
		}
		if result.header == nil || PacketType(result.header.PktID) != result.packetType {
			t.Fatalf("request %d received unexpected header: %+v", result.packetType, result.header)
		}
		if len(result.payload) != 1 {
			t.Fatalf("request %d received unexpected payload: %v", result.packetType, result.payload)
		}
	}

	if err := <-serverDone; err != nil {
		t.Fatalf("fake server failed: %v", err)
	}
	close(client.done)
	clientConn.Close()
	server.Close()
}

// TestWaitForPacketHandlesClosedUpdates verifies that waitForPacket returns an error when the serverUpdates channel is closed, simulating a client shutdown.
func TestWaitForPacketHandlesClosedUpdates(t *testing.T) {
	client := &Client{serverUpdates: make(chan *PacketHeader), done: make(chan struct{})}
	close(client.serverUpdates)
	if _, _, err := client.waitForPacket(PKT_DEVICE_LIST_UPDATED, time.Second); err == nil {
		t.Fatal("expected closed update channel error")
	}
}

// TestWaitForPacketTimesOut verifies that waitForPacket returns an error when no matching update arrives before the timeout.
func TestWaitForPacketTimesOut(t *testing.T) {
	client := &Client{
		serverUpdates: make(chan *PacketHeader),
		done:          make(chan struct{}),
	}
	if _, _, err := client.waitForPacket(PKT_DEVICE_LIST_UPDATED, time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("waitForPacket() error = %v, want timeout error", err)
	}
}

// TestCloseReturnsTheSameErrorOnRepeatedCalls verifies that repeated Close calls return the same error.
func TestCloseReturnsTheSameErrorOnRepeatedCalls(t *testing.T) {
	server, clientConn := net.Pipe()
	client := &Client{
		conn:  &connection{conn: clientConn},
		queue: make(chan writeRequest, 1),
		done:  make(chan struct{}),
	}
	go client.writeQueueWorker()

	if err := client.QueueWrite(1, PKT_RGBCONTROLLER_UPDATELEDS, nil); err != nil {
		t.Fatal(err)
	}
	server.Close()

	firstErr := client.Close()
	secondErr := client.Close()
	if firstErr == nil {
		t.Fatal("expected close error")
	}
	if secondErr == nil || secondErr.Error() != firstErr.Error() {
		t.Fatalf("repeated close error = %v, want %v", secondErr, firstErr)
	}
}

// TestDisconnectClosesConnectionWithoutWaiting verifies that Disconnect immediately closes the connection without waiting for queued writes.
func TestDisconnectClosesConnectionWithoutWaiting(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()

	client := &Client{
		conn:            &connection{conn: clientConn},
		protocol:        5,
		done:            make(chan struct{}),
		incomingPackets: make(chan incomingPacket, 1),
		serverUpdates:   make(chan *PacketHeader, 1),
		plugins:         make(map[string]*Plugin),
		effects:         make(map[string]*Effect),
	}
	go client.readLoop()

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- client.Disconnect()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Disconnect returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Disconnect blocked")
	}
}

// TestGetDevicesByTypeReturnsDevicesMatchingType verifies that GetDevicesByType returns all devices for a given controller type.
func TestGetDevicesByTypeReturnsDevicesMatchingType(t *testing.T) {
	client, _ := newTestClient(t, 5)

	controller1 := &RGBController{ID: 1, Type: KEYBOARD, Name: "Keyboard 1"}
	controller2 := &RGBController{ID: 2, Type: MONITOR, Name: "Monitor 1"}
	controller3 := &RGBController{ID: 3, Type: KEYBOARD, Name: "Keyboard 2"}

	client.rgbControllers = map[string]*RGBController{
		"Keyboard1": controller1,
		"Monitor1":  controller2,
		"Keyboard2": controller3,
	}

	devices, err := client.GetDevicesByType(KEYBOARD)
	if err != nil {
		t.Fatalf("GetDevicesByType returned error: %v", err)
	}

	if len(devices) != 2 {
		t.Fatalf("expected 2 keyboard devices, got %d", len(devices))
	}

	found := make(map[uint32]bool)
	for _, dev := range devices {
		found[dev.ControllerID] = true
	}
	if !found[1] || !found[3] {
		t.Fatalf("expected both keyboard controllers in results")
	}
}

func TestDuplicateControllerNamesKeepDistinctIDs(t *testing.T) {
	first := &RGBController{ID: 1, Type: KEYBOARD, Name: "Keyboard"}
	second := &RGBController{ID: 2, Type: KEYBOARD, Name: "Keyboard"}
	controllers := indexControllersByName([]*RGBController{first, second})
	if len(controllers) != 2 || controllers["Keyboard - 1"] != first || controllers["Keyboard - 2"] != second {
		t.Fatalf("duplicate controller indexing = %#v", controllers)
	}

	client := newQueueOnlyClient()
	client.rgbControllers = controllers
	if _, err := client.GetControllerByName("Keyboard"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("GetControllerByName(duplicate) error = %v, want ambiguity error", err)
	}
	for _, controllerID := range []uint32{1, 2} {
		controller, err := client.GetController(controllerID)
		if err != nil || uint32(controller.ID) != controllerID {
			t.Fatalf("GetController(%d) = %+v, %v", controllerID, controller, err)
		}
	}

	devices, err := client.GetDevicesByType(KEYBOARD)
	if err != nil {
		t.Fatalf("GetDevicesByType() error = %v", err)
	}
	if len(devices) != 2 || devices[0].ControllerID == devices[1].ControllerID {
		t.Fatalf("GetDevicesByType() returned controller IDs %d and %d", devices[0].ControllerID, devices[1].ControllerID)
	}

	client.mu.Lock()
	client.rgbControllers["Keyboard - 1"].Name = "Renamed Keyboard"
	client.reindexControllersLocked()
	client.reindexDevicesLocked()
	client.mu.Unlock()
	if len(client.ListControllers()) != 2 || len(client.ListDevices()) != 2 {
		t.Fatal("reindexing after a controller rename lost a controller or device")
	}
	if _, err := client.GetControllerByName("Renamed Keyboard"); err != nil {
		t.Fatalf("GetControllerByName after rename: %v", err)
	}
}

// TestAddAllDevicesCreatesAndCachesAllDevices verifies that AddAllDevices creates and caches devices for all discovered controllers.
func TestAddAllDevicesCreatesAndCachesAllDevices(t *testing.T) {
	client, _ := newTestClient(t, 5)

	controller1 := &RGBController{ID: 1, Type: KEYBOARD, Name: "Keyboard 1", LEDs: []LED{{Name: "LED1"}}}
	controller2 := &RGBController{ID: 2, Type: MONITOR, Name: "Monitor 1", LEDs: []LED{{Name: "LED1"}}}

	client.rgbControllers = map[string]*RGBController{
		"Keyboard1": controller1,
		"Monitor1":  controller2,
	}

	err := client.AddAllDevices()
	if err != nil {
		t.Fatalf("AddAllDevices returned error: %v", err)
	}

	cached := client.ListDevices()
	if len(cached) != 2 {
		t.Fatalf("expected 2 cached devices, got %d", len(cached))
	}

	dev1, ok := cached["Keyboard1"]
	if !ok {
		t.Fatal("Keyboard1 not found in cache")
	}
	if dev1.ControllerID != 1 {
		t.Fatalf("Keyboard1 ControllerID = %d, want 1", dev1.ControllerID)
	}

	dev2, ok := cached["Monitor1"]
	if !ok {
		t.Fatal("Monitor1 not found in cache")
	}
	if dev2.ControllerID != 2 {
		t.Fatalf("Monitor1 ControllerID = %d, want 2", dev2.ControllerID)
	}
}

// TestClientDeviceCacheMethods verifies the behavior of the client device cache methods.
func TestClientDeviceCacheMethods(t *testing.T) {
	client := newQueueOnlyClient()
	client.rgbControllers["Keyboard"] = &RGBController{
		ID: 2, Name: "Keyboard", Type: KEYBOARD, ActiveMode: 4,
		Modes: []Mode{{id: 1, Name: "Static"}, {id: 4, Name: "Rainbow"}},
	}
	added, err := client.AddDevice("Keyboard")
	if err != nil || added.ControllerID != 2 || added.Mode != "Rainbow" || added.ModeIdx != 1 {
		t.Fatalf("AddDevice() = %+v, %v", added, err)
	}
	cached, err := client.GetDevice("Keyboard")
	if err != nil || cached != added {
		t.Fatalf("GetDevice() = %p, %v, want cached %p", cached, err, added)
	}
	if _, err := client.AddDevice("missing"); err == nil {
		t.Fatal("AddDevice succeeded for an unknown controller")
	}
	listed := client.ListDevices()
	delete(listed, "Keyboard")
	if _, ok := client.ListDevices()["Keyboard"]; !ok {
		t.Fatal("ListDevices exposed the internal map")
	}
	byName, err := client.GetControllerByName("Keyboard")
	if err != nil || byName.Name != "Keyboard" {
		t.Fatalf("GetControllerByName() = %+v, %v", byName, err)
	}
	if _, err := client.GetControllerByName("missing"); err == nil {
		t.Fatal("GetControllerByName succeeded for an unknown controller")
	}
	if _, err := client.GetDeviceByType(MONITOR); err == nil {
		t.Fatal("GetDeviceByType succeeded without a matching device")
	}
	got, err := client.GetDeviceByType(KEYBOARD)
	if err != nil || got != added {
		t.Fatalf("GetDeviceByType() = %p, %v", got, err)
	}
}

// TestGetProfilesParsesServerResponse verifies that the client correctly parses the server response for profile listings.
func TestGetProfilesParsesServerResponse(t *testing.T) {
	client, server := newProtocolTestClient(t, 5)
	serverDone := make(chan error, 1)
	go func() {
		if err := checkProtocolTestPacket(server, 0, PKT_REQUEST_PROFILE_LIST, nil); err != nil {
			serverDone <- err
			return
		}
		response := newPacketEncoder(0)
		response.uint32(0)
		response.uint16(2)
		appendProtocolTestString(response, "Desk")
		appendProtocolTestString(response, "Streaming")
		response.setLengthPrefix()
		serverDone <- sendProtocolTestPacket(server, 0, PKT_REQUEST_PROFILE_LIST, response.getPayload())
	}()

	profiles, err := client.GetProfiles()
	if err != nil || strings.Join(profiles, ",") != "Desk,Streaming" {
		t.Fatalf("GetProfiles() = %v, %v", profiles, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}

// TestProfileMutationPacketsAndRefresh verifies that profile mutation requests are correctly sent and that the client handles the subsequent controller refresh.
func TestProfileMutationPacketsAndRefresh(t *testing.T) {
	client, server := newProtocolTestClient(t, 5)
	serverDone := make(chan error, 1)
	go func() {
		for _, request := range []struct {
			packetType PacketType
			name       string
		}{
			{PKT_REQUEST_SAVE_PROFILE, "Desk"},
			{PKT_REQUEST_DELETE_PROFILE, "Old"},
			{PKT_REQUEST_LOAD_PROFILE, "Streaming"},
		} {
			if err := checkProtocolTestPacket(server, 0, request.packetType, []byte(request.name)); err != nil {
				serverDone <- err
				return
			}
			if request.packetType == PKT_REQUEST_LOAD_PROFILE {
				serverDone <- serveControllerRefresh(server)
				return
			}
		}
		serverDone <- nil
	}()

	if err := client.SaveProfile("Desk"); err != nil {
		t.Fatalf("SaveProfile() error: %v", err)
	}
	if err := client.DeleteProfile("Old"); err != nil {
		t.Fatalf("DeleteProfile() error: %v", err)
	}
	if err := client.LoadProfile("Streaming"); err != nil {
		t.Fatalf("LoadProfile() error: %v", err)
	}
	if _, ok := client.rgbControllers["Test Keyboard"]; !ok {
		t.Fatal("LoadProfile() did not refresh the controller cache")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}

// TestRescanDevicesStopsWhenClientCloses verifies that the RescanDevices method stops and returns an error when the client is closed.
func TestRescanDevicesStopsWhenClientCloses(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := &Client{
		conn:          &connection{conn: clientConn},
		protocol:      5,
		done:          make(chan struct{}),
		serverUpdates: make(chan *PacketHeader),
	}
	result := make(chan error, 1)
	go func() { result <- client.RescanDevices() }()

	if err := checkProtocolTestPacket(server, 0, PKT_REQUEST_RESCAN_DEVICES, nil); err != nil {
		t.Fatalf("rescan request: %v", err)
	}
	close(client.done)
	if err := <-result; err == nil || !strings.Contains(err.Error(), "start update") {
		t.Fatalf("RescanDevices() error = %v, want closed-client update error", err)
	}
}

func TestProfileMethodsRequireProtocol(t *testing.T) {
	client := &Client{protocol: 1}
	if _, err := client.GetProfiles(); err == nil {
		t.Fatal("GetProfiles() succeeded below protocol 2")
	}
	for name, operation := range map[string]func() error{
		"LoadProfile":   func() error { return client.LoadProfile("Desk") },
		"SaveProfile":   func() error { return client.SaveProfile("Desk") },
		"DeleteProfile": func() error { return client.DeleteProfile("Desk") },
	} {
		if err := operation(); err == nil {
			t.Errorf("%s succeeded below protocol 2", name)
		}
	}
}

// TestCloneRGBController verifies that cloneRGBController creates a deep copy of an RGBController, including nested slices and structures.
func TestCloneRGBController(t *testing.T) {
	src := &RGBController{
		Name: "Test Controller",
		ID:   1,
		LEDs: []LED{
			{Name: "LED 1"},
		},
		Colors: []RGBColor{
			{R: 255, G: 0, B: 0},
		},
		Modes: []Mode{
			{
				Colors: []RGBColor{
					{R: 0, G: 255, B: 0},
				},
			},
		},
		Zones: []Zone{
			{
				segmentData: []Segment{
					{},
				},
				matrixData: [][]int32{
					{1, 2, 3},
				},
			},
		},
		AltLedNames: []string{"LED A"},
	}

	clone := cloneRGBController(src)

	if clone == nil {
		t.Fatal("expected non-nil clone")
	}

	if clone == src {
		t.Fatal("clone should be a different controller")
	}

	// Verify scalar fields are copied.
	if clone.Name != src.Name || clone.ID != src.ID {
		t.Fatal("scalar fields were not copied correctly")
	}

	// Verify slice contents are copied.
	if len(clone.LEDs) != len(src.LEDs) {
		t.Fatal("LED slice length mismatch")
	}
	if len(clone.Modes) != len(src.Modes) {
		t.Fatal("mode slice length mismatch")
	}
	if len(clone.Zones) != len(src.Zones) {
		t.Fatal("zone slice length mismatch")
	}

	// Modify the clone's top-level slices.
	clone.LEDs[0].Name = "Modified LED"
	clone.Colors[0].R = 100
	clone.AltLedNames[0] = "Modified name"

	if src.LEDs[0].Name != "LED 1" {
		t.Error("modifying cloned LEDs changed the original")
	}
	if src.Colors[0].R != 255 {
		t.Error("modifying cloned colors changed the original")
	}
	if src.AltLedNames[0] != "LED A" {
		t.Error("modifying cloned LED names changed the original")
	}

	// Verify nested mode colors are independent.
	clone.Modes[0].Colors[0].G = 100

	if src.Modes[0].Colors[0].G != 255 {
		t.Error("modifying cloned mode colors changed the original")
	}

	// Verify nested zone matrix data is independent.
	clone.Zones[0].matrixData[0][0] = 99

	if src.Zones[0].matrixData[0][0] != 1 {
		t.Error("modifying cloned matrix data changed the original")
	}

	// Verify the segment slice has independent backing storage.
	clone.Zones[0].segmentData[0] = Segment{}

	if &clone.Zones[0].segmentData[0] == &src.Zones[0].segmentData[0] {
		t.Error("cloned segment slice shares backing storage with original")
	}
}

// TestCloneRGBControllerNil verifies that cloning a nil RGBController returns nil.
func TestCloneRGBControllerNil(t *testing.T) {
	clone := cloneRGBController(nil)

	if clone != nil {
		t.Fatal("expected nil when cloning a nil controller")
	}
}
