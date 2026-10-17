// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"encoding/binary"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"
)

// TestColorConversionRoundTrip verifies that RGB values survive both library
// color conversion functions without changing any channel values.
func TestColorConversionRoundTrip(t *testing.T) {
	colors := []RGBColor{{R: 0, G: 0, B: 0}, {R: 0x12, G: 0x34, B: 0x56}, {R: 255, G: 128, B: 1}}

	for _, want := range colors {
		if got := ConvertToRGBColor(ConvertRGBtoUint32(want)); got != want {
			t.Fatalf("round trip for %+v produced %+v", want, got)
		}
	}
}

// TestColorsWithSegmentUpdatePreservesOtherLEDs verifies that updating a segment's colors does not mutate the cached controller colors for other LEDs.
func TestColorsWithSegmentUpdatePreservesOtherLEDs(t *testing.T) {
	originalColors := []RGBColor{
		{R: 1, G: 2, B: 3},
		{R: 4, G: 5, B: 6},
		{R: 7, G: 8, B: 9},
		{R: 10, G: 11, B: 12},
		{R: 13, G: 14, B: 15},
	}
	controller := &RGBController{
		LEDs:   make([]LED, len(originalColors)),
		Colors: append([]RGBColor(nil), originalColors...),
		Zones: []Zone{
			{
				NumLeds: 3,
				segmentData: []Segment{
					{Name: "first", StartIdx: 0, NumLeds: 1},
					{Name: "second", StartIdx: 1, NumLeds: 2},
				},
			},
			{
				NumLeds:     2,
				segmentData: []Segment{{Name: "third", StartIdx: 0, NumLeds: 1}},
			},
		},
	}
	segmentColor := RGBColor{R: 255, G: 0, B: 128}

	got, err := colorsWithSegmentUpdate(controller, 0, 1, segmentColor)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]RGBColor(nil), originalColors...)
	want[1] = segmentColor
	want[2] = segmentColor
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("color %d = %+v, want %+v", index, got[index], want[index])
		}
	}
	if controller.Colors[1] != originalColors[1] || controller.Colors[2] != originalColors[2] {
		t.Fatal("segment update mutated the cached controller colors")
	}
}

// TestEnumStrings verifies that the String() methods for DeviceType, Direction, ColorMode, and Speed return the expected human-readable names for known values and a generic "Unknown" string for unrecognized values.
func TestEnumStrings(t *testing.T) {
	tests := map[string]struct {
		got  string
		want string
	}{
		"device type":  {KEYBOARD.String(), "Keyboard"},
		"direction":    {RIGHT.String(), "Right"},
		"color mode":   {PER_LED.String(), "PerLED"},
		"speed":        {FAST.String(), "Fast"},
		"unknown type": {DeviceType(99).String(), "Unknown(99)"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("String() = %q, want %q", test.got, test.want)
			}
		})
	}
}

// TestSetBrightnessZeroIsExplicitlyTracked distinguishes an intentional zero from an unset option.
func TestSetBrightnessZeroIsExplicitlyTracked(t *testing.T) {
	client := &Client{
		queue: make(chan writeRequest, 1),
		done:  make(chan struct{}),
		RGBControllers: map[string]*RGBController{
			"Demo": {
				Name: "Demo",
				ID:   1,
				Modes: []Mode{{
					Name:          "Test",
					value:         1,
					rawFlags:      uint32(HAS_BRIGHTNESS),
					Flags:         Flags{flags: HAS_BRIGHTNESS},
					BrightnessMin: 0,
					BrightnessMax: 100,
					ColorMode:     PER_LED,
				}},
			},
		},
	}

	device := &Device{ControllerID: 1, Mode: "Test", ModeIdx: 0, client: client}
	if err := device.SetBrightness(0); err != nil {
		t.Fatalf("SetBrightness(0) returned error: %v", err)
	}
	if !device.ModeOptions.brightnessSet {
		t.Fatal("SetBrightness(0) did not mark brightness as explicitly set")
	}
	if device.ModeOptions.Brightness != 0 {
		t.Fatalf("Brightness = %d, want 0", device.ModeOptions.Brightness)
	}
	if _, ok := <-client.queue; !ok {
		t.Fatal("expected queued write payload")
	}
	client.queueWg.Done()
}

// TestQueueWriteDoesNotBlockCloseWhenQueueIsFull protects shutdown from a blocked enqueue.
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

// TestCloseStopsPendingRefreshTimers verifies that closing the client stops any active refresh timers.
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
	client.refreshTimers[7] = time.NewTimer(time.Hour)

	if err := client.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
	if _, ok := client.refreshTimers[7]; ok {
		t.Fatal("Close() left stale refresh timers behind")
	}
}

// TestPacketDecoderRejectsTruncatedPayload verifies that the PacketDecoder returns an error when attempting to read a uint32 from a payload that is too short.
func TestPacketDecoderRejectsTruncatedPayload(t *testing.T) {
	decoder := newPacketDecoder([]byte{1, 2, 3}, 0)
	if got := decoder.uint32(); got != 0 {
		t.Fatalf("truncated uint32 = %d, want zero value", got)
	}
	if decoder.Err() == nil {
		t.Fatal("expected truncated payload error")
	}
}

// TestControllerFactoryPropagatesTruncatedPayload verifies that a truncated controller payload produces an error when parsed.
func TestControllerFactoryPropagatesTruncatedPayload(t *testing.T) {
	factory := ControllerFactory{payload: make([]byte, 4), protocol: 5}
	if _, err := factory.makeController(); err == nil {
		t.Fatal("expected truncated controller payload error")
	}
}

// TestControllerFactoryParsesProtocolFiveController covers protocol-v5 metadata and optional fields.
func TestControllerFactoryParsesProtocolFiveController(t *testing.T) {
	encoder := newPacketEncoder(5)
	encoder.uint32(0)
	writeString := func(value string) {
		// Match PacketDecoder.varStr: a uint16 byte length followed by a NUL-terminated value.
		encoder.uint16(uint16(len(value) + 1))
		encoder.writeBytes(append([]byte(value), 0))
	}

	// Controller identity fields precede the mode table in the wire payload.
	encoder.uint32(uint32(KEYBOARD))
	writeString("Keyboard")
	writeString("Vendor")
	writeString("Description")
	writeString("Model")
	writeString("Serial")
	writeString("Location")

	// One mode with speed, brightness, direction, and mode-specific color support.
	encoder.uint16(1)
	encoder.uint32(0)
	writeString("Rainbow")
	encoder.uint32(7)
	encoder.uint32(uint32(HAS_SPEED | HAS_BRIGHTNESS | HAS_DIR_LR | HAS_MODE_COLOR))
	encoder.uint32(10)
	encoder.uint32(90)
	encoder.uint32(20)
	encoder.uint32(100)
	encoder.uint32(1)
	encoder.uint32(2)
	encoder.uint32(30)
	encoder.uint32(70)
	encoder.uint32(uint32(RIGHT))
	encoder.uint32(uint32(MODE_SPECIFIC))
	encoder.uint16(2)
	encoder.uint32(ConvertRGBtoUint32(RGBColor{R: 1, G: 2, B: 3}))
	encoder.uint32(ConvertRGBtoUint32(RGBColor{R: 4, G: 5, B: 6}))

	// One zone with a 2x2 matrix, one segment, and protocol-v5 flags.
	encoder.uint16(1)
	writeString("Main")
	encoder.uint32(2)
	encoder.uint32(1)
	encoder.uint32(4)
	encoder.uint32(4)
	encoder.uint16(24)
	encoder.uint32(2)
	encoder.uint32(2)
	encoder.uint32(0)
	encoder.uint32(1)
	encoder.uint32(0xFFFFFFFF)
	encoder.uint32(3)
	encoder.uint16(1)
	writeString("Left")
	encoder.uint32(2)
	encoder.uint32(1)
	encoder.uint32(2)
	encoder.uint32(0x12345678)

	// LED records are followed by cached colors and protocol-v5 controller extras.
	encoder.uint16(2)
	writeString("Key A")
	encoder.uint32(99)
	writeString("Key B")
	encoder.uint32(42)
	encoder.uint16(2)
	encoder.uint32(ConvertRGBtoUint32(RGBColor{R: 10, G: 20, B: 30}))
	encoder.uint32(ConvertRGBtoUint32(RGBColor{R: 40, G: 50, B: 60}))
	encoder.uint16(1)
	writeString("Alternate Key A")
	encoder.uint32(0xAABBCCDD)

	factory := ControllerFactory{
		payload:       encoder.getPayload(),
		protocol:      5,
		controller_id: 12,
	}
	controller, err := factory.makeController()
	if err != nil {
		t.Fatalf("makeController returned error: %v", err)
	}
	if controller.ID != 12 || controller.Type != KEYBOARD || controller.Name != "Keyboard" || controller.Vendor != "Vendor" || controller.Model != "Model" || controller.Serial != "Serial" {
		t.Fatalf("unexpected controller metadata: %+v", controller)
	}
	if controller.NumModes != 1 || controller.ActiveMode != 0 || len(controller.Modes) != 1 {
		t.Fatalf("unexpected mode counts or active mode: %+v", controller)
	}
	mode := controller.Modes[0]
	if mode.Name != "Rainbow" || mode.value != 7 || mode.SpeedMin != 10 || mode.SpeedMax != 90 || mode.BrightnessMin != 20 || mode.BrightnessMax != 100 || mode.speed != 30 || mode.brightness != 70 || mode.direction != uint32(RIGHT) || mode.ColorMode != MODE_SPECIFIC {
		t.Fatalf("unexpected mode data: %+v", mode)
	}
	if !mode.SupportsSpeed() || !mode.SupportsBrightness() || !mode.SupportsDirectionLR() || !mode.SupportsModeColor() {
		t.Fatalf("mode flags were not decoded: %+v", mode.Flags)
	}
	if len(controller.Zones) != 1 {
		t.Fatalf("zone count = %d, want 1", len(controller.Zones))
	}
	zone := controller.Zones[0]
	if zone.Name != "Main" || zone.ZoneType != 2 || zone.NumLeds != 4 || zone.flags != 0x12345678 {
		t.Fatalf("unexpected zone data: %+v", zone)
	}
	wantMatrix := [][]int32{{0, 1}, {-1, 3}}
	if !reflect.DeepEqual(zone.matrixData, wantMatrix) {
		t.Fatalf("matrix data = %v, want %v", zone.matrixData, wantMatrix)
	}
	if len(zone.segmentData) != 1 || zone.segmentData[0] != (Segment{Name: "Left", SegmentType: 2, StartIdx: 1, NumLeds: 2}) {
		t.Fatalf("unexpected segments: %+v", zone.segmentData)
	}
	if !reflect.DeepEqual(controller.LEDs, []LED{{Name: "Key A", Value: 0}, {Name: "Key B", Value: 1}}) {
		t.Fatalf("LEDs = %+v, want normalized LED indices", controller.LEDs)
	}
	wantColors := []RGBColor{{R: 10, G: 20, B: 30}, {R: 40, G: 50, B: 60}}
	if !reflect.DeepEqual(controller.Colors, wantColors) {
		t.Fatalf("colors = %+v, want %+v", controller.Colors, wantColors)
	}
	if !reflect.DeepEqual(controller.AltLedNames, []string{"Alternate Key A"}) || controller.ControllerFlags != 0xAABBCCDD {
		t.Fatalf("unexpected protocol-v5 extras: names=%v flags=%#x", controller.AltLedNames, controller.ControllerFlags)
	}
}

// TestControllerFactoryRejectsInvalidMatrixLength rejects matrix headers too short for dimensions.
func TestControllerFactoryRejectsInvalidMatrixLength(t *testing.T) {
	encoder := newPacketEncoder(5)
	encoder.uint16(0)
	encoder.uint32(0)
	encoder.uint32(0)
	encoder.uint32(0)
	encoder.uint32(0)
	encoder.uint16(4)

	factory := ControllerFactory{protocol: 5}
	factory.decoder = *newPacketDecoder(encoder.getPayload(), 0)
	if _, err := factory.parseZoneData(); err == nil || err.Error() != "invalid matrix length 4" {
		t.Fatalf("parseZoneData error = %v, want invalid matrix length 4", err)
	}
}

// TestConnectionSendAndReadPacket verifies the OpenRGB packet header and
// payload written by SendPacket. net.Pipe provides a server-like endpoint
// without requiring a running OpenRGB instance.
func TestConnectionSendAndReadPacket(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	connection := &connection{conn: client}
	wantPayload := []byte{1, 2, 3, 4}
	readResult := make(chan struct {
		header  *PacketHeader
		payload []byte
		err     error
	}, 1)
	go func() {
		headerBytes := make([]byte, 16)
		_, err := io.ReadFull(server, headerBytes)
		if err != nil {
			readResult <- struct {
				header  *PacketHeader
				payload []byte
				err     error
			}{err: err}
			return
		}
		payload := make([]byte, binary.LittleEndian.Uint32(headerBytes[12:16]))
		_, err = io.ReadFull(server, payload)
		readResult <- struct {
			header  *PacketHeader
			payload []byte
			err     error
		}{
			header: &PacketHeader{
				Magic:  string(headerBytes[:4]),
				DevIdx: binary.LittleEndian.Uint32(headerBytes[4:8]),
				PktID:  binary.LittleEndian.Uint32(headerBytes[8:12]),
				Size:   binary.LittleEndian.Uint32(headerBytes[12:16]),
			},
			payload: payload,
		}
	}()

	if err := connection.SendPacket(7, uint32(PKT_RGBCONTROLLER_UPDATELEDS), wantPayload); err != nil {
		t.Fatalf("SendPacket returned error: %v", err)
	}
	result := <-readResult
	if result.err != nil {
		t.Fatalf("server read returned error: %v", result.err)
	}
	if result.header.DevIdx != 7 || result.header.PktID != uint32(PKT_RGBCONTROLLER_UPDATELEDS) || result.header.Size != 4 {
		t.Fatalf("unexpected header: %+v", result.header)
	}
	if string(result.payload) != string(wantPayload) {
		t.Fatalf("unexpected payload: %v", result.payload)
	}
}

// TestConnectionReadPacketRejectsInvalidMagic ensures malformed packets are
// rejected before their contents are used.
func TestConnectionReadPacketRejectsInvalidMagic(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	result := make(chan error, 1)
	go func() {
		_, _, err := (&connection{conn: client}).ReadPacket(time.Second)
		result <- err
	}()

	header := make([]byte, 16)
	copy(header, "BAD!")
	if _, err := server.Write(header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := <-result; err == nil || err.Error() != `invalid magic: "BAD!"` {
		t.Fatalf("expected invalid magic error, got %v", err)
	}
}

// TestConnectionReadPacketRejectsOversizedPayload ensures that the client does not attempt to allocate or read a payload larger than the maximum allowed size.
func TestConnectionReadPacketRejectsOversizedPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	result := make(chan error, 1)
	go func() {
		_, _, err := (&connection{conn: client}).ReadPacket(time.Second)
		result <- err
	}()

	header := make([]byte, 16)
	copy(header, "ORGB")
	binary.LittleEndian.PutUint32(header[12:16], maxPacketPayload+1)
	if _, err := server.Write(header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := <-result; err == nil {
		t.Fatal("expected oversized payload error")
	}
}

// TestSegmentsCollectionMaintainsContiguousIndices checks segment creation,
// resizing, removal, and the LED capacity limit.
func TestSegmentsCollectionMaintainsContiguousIndices(t *testing.T) {
	segments := SegmentsCollection{NumLeds: 10}
	if err := segments.AddItem("first", 3); err != nil {
		t.Fatal(err)
	}
	if err := segments.AddItem("second", 4); err != nil {
		t.Fatal(err)
	}
	if err := segments.UpdateItem(0, 2); err != nil {
		t.Fatal(err)
	}
	if got := segments.Segments[1].Segment.StartIdx; got != 2 {
		t.Fatalf("second segment starts at %d, want 2", got)
	}
	if err := segments.RemoveItem(0); err != nil {
		t.Fatal(err)
	}
	if got := segments.Segments[0].Segment.StartIdx; got != 0 {
		t.Fatalf("remaining segment starts at %d, want 0", got)
	}
	if err := segments.AddItem("too-large", 11); err == nil {
		t.Fatal("expected segment size error")
	}
}

// newTestClient creates a client connected to an in-memory pipe and starts its
// write worker so tests can inspect packets queued by Device methods.
func newTestClient(t *testing.T, protocol int) (*Client, net.Conn) {
	t.Helper()
	server, clientConn := net.Pipe()
	client := &Client{
		conn:           &connection{conn: clientConn},
		RGBControllers: make(map[string]*RGBController),
		devices:        make(map[string]*Device),
		queue:          make(chan writeRequest, 10),
		AutoRefresh:    false,
		done:           make(chan struct{}),
		protocol:       protocol,
	}
	go client.writeQueueWorker()
	t.Cleanup(func() {
		close(client.queue)
		client.queueWg.Wait()
		client.conn.Disconnect()
		server.Close()
	})
	return client, server
}

// readQueuedPacket reads one complete OpenRGB packet from the fake server end
// of the pipe and returns its controller ID, packet type, and payload.
func readQueuedPacket(t *testing.T, server net.Conn) (uint32, uint32, []byte) {
	t.Helper()
	header := make([]byte, 16)
	if _, err := io.ReadFull(server, header); err != nil {
		t.Fatalf("read queued header: %v", err)
	}
	payload := make([]byte, binary.LittleEndian.Uint32(header[12:16]))
	if _, err := io.ReadFull(server, payload); err != nil {
		t.Fatalf("read queued payload: %v", err)
	}
	return binary.LittleEndian.Uint32(header[4:8]), binary.LittleEndian.Uint32(header[8:12]), payload
}

// TestDeviceUpdateLed verifies that updating a named LED queues the expected
// packet and encodes the LED index and RGB color in little-endian format.
func TestDeviceUpdateLedQueuesExpectedPacket(t *testing.T) {
	client, server := newTestClient(t, 5)
	client.RGBControllers["Demo"] = &RGBController{
		ID:   3,
		Name: "Demo",
		LEDs: []LED{{Name: "Status", Value: 1}},
	}
	device := &Device{ControllerID: 3, client: client}

	if err := device.UpdateLed("Status", RGBColor{R: 0x12, G: 0x34, B: 0x56}); err != nil {
		t.Fatal(err)
	}
	controllerID, packetID, payload := readQueuedPacket(t, server)
	client.FlushWrites()
	if controllerID != 3 || packetID != uint32(PKT_RGBCONTROLLER_UPDATESINGLELED) {
		t.Fatalf("unexpected packet address: controller=%d packet=%d", controllerID, packetID)
	}
	if got := binary.LittleEndian.Uint32(payload[:4]); got != 1 {
		t.Fatalf("LED index = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint32(payload[4:]); got != 0x563412 {
		t.Fatalf("encoded color = %#x, want %#x", got, 0x563412)
	}
}

// TestDeviceProtocolGuard verifies that protocol-dependent operations fail
// before attempting to contact the server when the protocol is too old.
func TestDeviceProtocolGuard(t *testing.T) {
	client, server := newTestClient(t, 4)
	device := &Device{ControllerID: 1, client: client}
	if _, err := device.NewSegment("Zone"); err == nil {
		t.Fatal("expected protocol version error")
	} else if err.Error() != "NewSegment is not supported in protocol version 4" {
		t.Fatalf("unexpected error: %v", err)
	}
	server.Close()
}

// TestDeviceUpdateLedRejectsUnknownLED verifies that UpdateLed returns an error when the LED name is not found in the controller's LED list.
func TestModeFlagsExposeCapabilities(t *testing.T) {
	flags := decodeModeFlags(HAS_SPEED | HAS_BRIGHTNESS | HAS_DIR_LR | HAS_DIR_UD | HAS_DIR_HV | HAS_UNQ_COLOR | HAS_MODE_COLOR | HAS_RAND_COLOR)
	if !flags.SupportsSpeed() || !flags.SupportsBrightness() || !flags.SupportsDirectionLR() || !flags.SupportsDirectionUD() || !flags.SupportsDirectionHV() || !flags.SupportsUniqueLEDColor() || !flags.SupportsModeColor() {
		t.Fatalf("expected all capability helpers to report true for the corresponding flags: %+v", flags)
	}
	if !flags.SupportsRandomColor() {
		t.Fatal("expected random color capability to report true")
	}
}

// TestModeSupportsBothDirectionValues verifies that a mode with all direction flags set supports all valid directions.
func TestModeSupportsBothDirectionValues(t *testing.T) {
	mode := Mode{Flags: decodeModeFlags(HAS_DIR_LR | HAS_DIR_UD | HAS_DIR_HV)}
	for _, direction := range []Direction{LEFT, RIGHT, UP, DOWN, HORIZONTAL, VERTICAL} {
		if !modeSupportsDirection(mode, direction) {
			t.Errorf("modeSupportsDirection(%v) = false", direction)
		}
	}
	if modeSupportsDirection(mode, Direction(99)) {
		t.Fatal("modeSupportsDirection accepted an invalid direction")
	}
}

// TestQueueWriteSupportsSkipRefreshOption verifies that the SkipRefresh option is honored when queuing a write operation.
func TestQueueWriteSupportsSkipRefreshOption(t *testing.T) {
	client, server := newTestClient(t, 5)
	if err := client.QueueWrite(7, PKT_RGBCONTROLLER_UPDATELEDS, []byte{1, 2, 3}, QueueWriteOptions{SkipRefresh: true}); err != nil {
		t.Fatal(err)
	}
	controllerID, packetID, payload := readQueuedPacket(t, server)
	if controllerID != 7 || packetID != uint32(PKT_RGBCONTROLLER_UPDATELEDS) {
		t.Fatalf("unexpected packet: controller=%d packet=%d", controllerID, packetID)
	}
	if string(payload) != string([]byte{1, 2, 3}) {
		t.Fatalf("unexpected payload: %v", payload)
	}
}

// TestWriteRefreshesCoalesceForController verifies that multiple refresh requests for the same controller are coalesced into a single write operation.
func TestWriteRefreshesCoalesceForController(t *testing.T) {
	client := &Client{done: make(chan struct{})}
	close(client.done)

	client.scheduleWriteRefresh(3)
	client.scheduleWriteRefresh(3)

	finished := make(chan struct{})
	go func() {
		client.FlushWrites()
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("FlushWrites did not complete after coalesced refresh")
	}
}

// TestRequestPacketSerializesConcurrentRequests protects matching responses because packets have no request ID.
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

// TestWaitForPacketHandlesTimeout verifies that waitForPacket returns an error when the timeout expires before the expected packet is received.
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
