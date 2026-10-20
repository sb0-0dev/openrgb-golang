// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"encoding/binary"
	"net"
	"reflect"
	"testing"
)

// TestSetBrightnessZeroIsExplicitlyTracked verifies that setting the brightness to zero is correctly tracked as an explicit change.
func TestSetBrightnessZeroIsExplicitlyTracked(t *testing.T) {
	client := &Client{
		queue: make(chan writeRequest, 1),
		done:  make(chan struct{}),
		rgbControllers: map[string]*RGBController{
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

// assertQueuedLEDColors is a helper function that verifies the queued LED update packet matches the expected colors.
func assertQueuedLEDColors(t *testing.T, client *Client, server net.Conn, wantPacket PacketType, wantColors []RGBColor) {
	t.Helper()
	controllerID, packetID, payload := readQueuedPacket(t, server)
	if controllerID != 3 || PacketType(packetID) != wantPacket {
		t.Fatalf("unexpected LED packet: controller=%d packet=%d", controllerID, packetID)
	}
	decoder := newPacketDecoder(payload, 0)
	if got := decoder.uint32(); got != uint32(len(payload)) {
		t.Fatalf("payload length prefix = %d, want %d", got, len(payload))
	}
	if got := decoder.uint16(); int(got) != len(wantColors) {
		t.Fatalf("LED color count = %d, want %d", got, len(wantColors))
	}
	for index, want := range wantColors {
		if got := ConvertToRGBColor(decoder.uint32()); got != want {
			t.Errorf("LED color %d = %+v, want %+v", index, got, want)
		}
	}
	if err := decoder.Err(); err != nil {
		t.Fatalf("invalid LED update payload: %v", err)
	}
	if err := client.FlushWrites(); err != nil {
		t.Fatalf("FlushWrites returned error: %v", err)
	}
}

// TestDeviceUpdateLed verifies that updating a named LED queues the expected
// packet and encodes the LED index and RGB color in little-endian format.
func TestDeviceUpdateLedQueuesExpectedPacket(t *testing.T) {
	client, server := newTestClient(t, 5)
	client.rgbControllers["Demo"] = &RGBController{
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

// TestDeviceUpdateLedsWithColorsQueuesEveryColor verifies that updating multiple LEDs with specific colors queues the correct packet for each color.
func TestDeviceUpdateLedsWithColorsQueuesEveryColor(t *testing.T) {
	client, device, server := newTestDeviceWithController(t)
	want := []RGBColor{{R: 100, G: 1, B: 2}, {R: 3, G: 110, B: 4}, {R: 5, G: 6, B: 120}, {R: 130, G: 8, B: 9}}
	if err := device.UpdateLedsWithColors(want); err != nil {
		t.Fatalf("UpdateLedsWithColors returned error: %v", err)
	}
	assertQueuedLEDColors(t, client, server, PKT_RGBCONTROLLER_UPDATELEDS, want)
}

// TestDeviceUpdateLedsRepeatsColorAndRejectsWrongCount verifies that UpdateLeds repeats the color for all LEDs and that UpdateLedsWithColors rejects an incorrect color count.
func TestDeviceUpdateLedsRepeatsColorAndRejectsWrongCount(t *testing.T) {
	client, device, server := newTestDeviceWithController(t)
	color := RGBColor{R: 30, G: 20, B: 10}
	if err := device.UpdateLeds(color); err != nil {
		t.Fatalf("UpdateLeds returned error: %v", err)
	}
	assertQueuedLEDColors(t, client, server, PKT_RGBCONTROLLER_UPDATELEDS, []RGBColor{color, color, color, color})
	if err := device.UpdateLedsWithColors([]RGBColor{color}); err == nil {
		t.Fatal("UpdateLedsWithColors accepted a color count that does not match the LED count")
	}
	if len(client.queue) != 0 {
		t.Fatal("invalid color count queued a write")
	}
}

// TestDeviceUpdateZoneLedsQueuesZoneAndColors verifies that updating a zone's LEDs queues the correct zone index and color data.
func TestDeviceUpdateZoneLedsQueuesZoneAndColors(t *testing.T) {
	client, device, server := newTestDeviceWithController(t)
	color := RGBColor{R: 40, G: 50, B: 60}
	if err := device.UpdateZoneLeds(color, "Right"); err != nil {
		t.Fatalf("UpdateZoneLeds returned error: %v", err)
	}
	controllerID, packetID, payload := readQueuedPacket(t, server)
	if controllerID != 3 || PacketType(packetID) != PKT_RGBCONTROLLER_UPDATEZONELEDS {
		t.Fatalf("unexpected zone packet: controller=%d packet=%d", controllerID, packetID)
	}
	decoder := newPacketDecoder(payload, 0)
	if got := decoder.uint32(); got != uint32(len(payload)) {
		t.Fatalf("payload length prefix = %d, want %d", got, len(payload))
	}
	if got := decoder.uint32(); got != 1 {
		t.Fatalf("zone index = %d, want 1", got)
	}
	if got := decoder.uint16(); got != 2 {
		t.Fatalf("zone color count = %d, want 2", got)
	}
	for index := range 2 {
		if got := ConvertToRGBColor(decoder.uint32()); got != color {
			t.Errorf("zone color %d = %+v, want %+v", index, got, color)
		}
	}
	if err := decoder.Err(); err != nil {
		t.Fatalf("invalid zone update payload: %v", err)
	}
	if err := client.FlushWrites(); err != nil {
		t.Fatalf("FlushWrites returned error: %v", err)
	}
}

// TestDeviceUpdateSegmentLedsChangesOnlyTargetSegment verifies that updating a segment's LEDs only affects the targeted segment.
func TestDeviceUpdateSegmentLedsChangesOnlyTargetSegment(t *testing.T) {
	client, device, server := newTestDeviceWithController(t)
	color := RGBColor{R: 200, G: 100, B: 50}
	want := []RGBColor{
		{R: 1, G: 2, B: 3},
		{R: 4, G: 5, B: 6},
		{R: 7, G: 8, B: 9},
		color,
	}
	if err := device.UpdateSegmentLeds(1, 0, color); err != nil {
		t.Fatalf("UpdateSegmentLeds returned error: %v", err)
	}
	assertQueuedLEDColors(t, client, server, PKT_RGBCONTROLLER_UPDATELEDS, want)
}

// TestDeviceSetModeQueuesUpdateAndSave verifies that setting a device mode queues both the mode update and save packets correctly.
func TestDeviceSetModeQueuesUpdateAndSave(t *testing.T) {
	client, device, server := newTestDeviceWithController(t)
	wantColor := RGBColor{R: 90, G: 80, B: 70}
	device.ModeOptions = ModeOptions{
		Speed:        FAST,
		Brightness:   42,
		Direction:    RIGHT,
		ColorMode:    MODE_SPECIFIC,
		Colors:       []RGBColor{wantColor},
		SaveToDevice: true,
	}
	if err := device.SetMode("Pulse"); err != nil {
		t.Fatalf("SetMode returned error: %v", err)
	}
	controllerID, updatePacket, updatePayload := readQueuedPacket(t, server)
	saveControllerID, savePacket, savePayload := readQueuedPacket(t, server)
	if err := client.FlushWrites(); err != nil {
		t.Fatalf("FlushWrites returned error: %v", err)
	}
	if controllerID != 3 || saveControllerID != 3 || PacketType(updatePacket) != PKT_RGBCONTROLLER_UPDATEMODE || PacketType(savePacket) != PKT_RGBCONTROLLER_SAVEMODE {
		t.Fatalf("unexpected mode packets: update=(%d,%d), save=(%d,%d)", controllerID, updatePacket, saveControllerID, savePacket)
	}
	if string(updatePayload) != string(savePayload) {
		t.Fatal("saved mode payload differs from the mode update payload")
	}
	decoder := newPacketDecoder(updatePayload, 0)
	if got := decoder.uint32(); got != uint32(len(updatePayload)) {
		t.Fatalf("payload length prefix = %d, want %d", got, len(updatePayload))
	}
	if got := decoder.uint32(); got != 1 {
		t.Fatalf("mode index = %d, want 1", got)
	}
	if got := decoder.varStr(); got != "Pulse" {
		t.Fatalf("mode name = %q, want Pulse", got)
	}
	if got := decoder.uint32(); got != 7 {
		t.Fatalf("mode value = %d, want 7", got)
	}
	if got := decoder.uint32(); got != uint32(HAS_SPEED|HAS_BRIGHTNESS|HAS_DIR_LR|HAS_MODE_COLOR) {
		t.Fatalf("mode flags = %#x, want supported option flags", got)
	}
	for range 6 {
		decoder.uint32()
	}
	if got := decoder.uint32(); got != uint32(FAST) {
		t.Errorf("mode speed = %d, want %d", got, FAST)
	}
	if got := decoder.uint32(); got != 42 {
		t.Errorf("mode brightness = %d, want 42", got)
	}
	if got := decoder.uint32(); got != uint32(RIGHT) {
		t.Errorf("mode direction = %d, want %d", got, RIGHT)
	}
	if got := decoder.uint32(); got != uint32(MODE_SPECIFIC) {
		t.Errorf("mode color mode = %d, want %d", got, MODE_SPECIFIC)
	}
	if got := decoder.uint16(); got != 1 {
		t.Fatalf("mode color count = %d, want 1", got)
	}
	if got := ConvertToRGBColor(decoder.uint32()); got != wantColor {
		t.Errorf("mode color = %+v, want %+v", got, wantColor)
	}
	if err := decoder.Err(); err != nil {
		t.Fatalf("invalid mode update payload: %v", err)
	}
}

// TestDeviceAccessorsReadControllerSnapshot verifies that the device accessors return a snapshot of the controller state and are not affected by subsequent changes to the cached controller.
func TestDeviceAccessorsReadControllerSnapshot(t *testing.T) {
	client, device, _ := newTestDeviceWithController(t)
	leds, err := device.GetLEDs()
	if err != nil || len(leds) != 4 || leds[0].Name != "LED 0" {
		t.Fatalf("GetLEDs() = %+v, %v", leds, err)
	}
	zones, err := device.GetZones()
	if err != nil || len(zones) != 2 || zones[1].Name != "Right" {
		t.Fatalf("GetZones() = %+v, %v", zones, err)
	}
	segments, err := device.GetSegments(1)
	if err != nil || !reflect.DeepEqual(segments, []Segment{{Name: "Right Segment", StartIdx: 1, NumLeds: 1}}) {
		t.Fatalf("GetSegments(1) = %+v, %v", segments, err)
	}
	if _, err := device.GetSegments(-1); err == nil {
		t.Fatal("GetSegments(-1) succeeded")
	}
	modes, err := device.GetModes()
	if err != nil || len(modes) != 2 || modes[1].Name != "Pulse" {
		t.Fatalf("GetModes() = %+v, %v", modes, err)
	}
	mode, err := device.GetMode()
	if err != nil || mode.Name != "Static" {
		t.Fatalf("GetMode() = %+v, %v", mode, err)
	}

	client.rgbControllers["Demo"].LEDs[0].Name = "Changed"
	if leds[0].Name != "LED 0" {
		t.Fatal("GetLEDs returned a view into the cached controller")
	}
}

// TestDeviceAccessorsRejectMissingClientAndInvalidMode verifies that device accessors return errors when the client is missing or the mode index is invalid.
func TestDeviceAccessorsRejectMissingClientAndInvalidMode(t *testing.T) {
	device := &Device{ControllerID: 1}
	if _, err := device.GetLEDs(); err == nil {
		t.Fatal("GetLEDs succeeded without an attached client")
	}
	client := newQueueOnlyClient()
	client.rgbControllers["Demo"] = &RGBController{ID: 1, Name: "Demo"}
	device = &Device{ControllerID: 1, ModeIdx: 1, client: client}
	if _, err := device.GetMode(); err == nil {
		t.Fatal("GetMode succeeded with an invalid mode index")
	}
}

// TestDeviceModeSettersQueueUpdates verifies that setting mode options queues the correct update packets and retains the options in the device.
func TestDeviceModeSettersQueueUpdates(t *testing.T) {
	client, device := newQueueOnlyTestDeviceWithController()
	device.Mode = "Pulse"
	device.ModeIdx = 1
	if err := device.SetSpeed(FAST); err != nil {
		t.Fatal(err)
	}
	if err := device.SetDirection(RIGHT); err != nil {
		t.Fatal(err)
	}
	colors := []RGBColor{{R: 11, G: 22, B: 33}}
	if err := device.SetColors(colors, MODE_SPECIFIC); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		request := <-client.queue
		if request.packetType != PKT_RGBCONTROLLER_UPDATEMODE || request.controller_id != 3 {
			t.Fatalf("unexpected mode update request: %+v", request)
		}
		client.queueWg.Done()
	}
	if device.ModeOptions.Speed != FAST || device.ModeOptions.Direction != RIGHT || !reflect.DeepEqual(device.ModeOptions.Colors, colors) || device.ModeOptions.ColorMode != MODE_SPECIFIC {
		t.Fatalf("mode options not retained: %+v", device.ModeOptions)
	}
}

// TestSetColorsSerializesSuppliedPaletteCount verifies that the SetColors method correctly serializes the number of colors supplied in the update packet.
func TestSetColorsSerializesSuppliedPaletteCount(t *testing.T) {
	client, device := newQueueOnlyTestDeviceWithController()
	device.Mode = "Pulse"
	device.ModeIdx = 1
	colors := []RGBColor{{R: 1, G: 2, B: 3}, {R: 4, G: 5, B: 6}}
	if err := device.SetColors(colors, MODE_SPECIFIC); err != nil {
		t.Fatal(err)
	}
	request := <-client.queue
	if request.packetType != PKT_RGBCONTROLLER_UPDATEMODE {
		t.Fatalf("queued packet type = %d, want mode update", request.packetType)
	}
	client.queueWg.Done()

	decoder := newPacketDecoder(request.payload, 0)
	decoder.uint32()
	decoder.uint32()
	if got := decoder.varStr(); got != "Pulse" {
		t.Fatalf("mode name = %q, want Pulse", got)
	}
	for range 12 {
		decoder.uint32()
	}
	if got := decoder.uint16(); int(got) != len(colors) {
		t.Fatalf("serialized color count = %d, want %d", got, len(colors))
	}
	for index, want := range colors {
		if got := ConvertToRGBColor(decoder.uint32()); got != want {
			t.Errorf("serialized color %d = %+v, want %+v", index, got, want)
		}
	}
	if err := decoder.Err(); err != nil {
		t.Fatalf("invalid mode payload: %v", err)
	}
}

// TestDeviceZoneOperationsQueueExpectedPackets verifies that zone operations queue the expected packets with correct parameters.
func TestDeviceZoneOperationsQueueExpectedPackets(t *testing.T) {
	client, device := newQueueOnlyTestDeviceWithController()
	if err := device.ResizeZone("Right", 5); err != nil {
		t.Fatal(err)
	}
	request := <-client.queue
	if request.packetType != PKT_RGBCONTROLLER_RESIZEZONE || binary.LittleEndian.Uint32(request.payload[:4]) != 1 || binary.LittleEndian.Uint32(request.payload[4:]) != 5 {
		t.Fatalf("unexpected resize request: %+v", request)
	}
	client.queueWg.Done()

	if err := device.ClearSegments("Right"); err != nil {
		t.Fatal(err)
	}
	request = <-client.queue
	if request.packetType != PKT_RGBCONTROLLER_CLEAR_SEGMENTS || int32(binary.LittleEndian.Uint32(request.payload)) != 1 {
		t.Fatalf("unexpected clear-segments request: %+v", request)
	}
	client.queueWg.Done()

	segments := SegmentsCollection{
		ZoneIdx: 1,
		NumLeds: 2,
		Segments: []SegmentItem{{Segment: Segment{
			Name: "New", SegmentType: 2, StartIdx: 0, NumLeds: 2,
		}}},
	}
	if err := device.AddSegment(segments); err != nil {
		t.Fatal(err)
	}
	request = <-client.queue
	if request.packetType != PKT_RGBCONTROLLER_ADD_SEGMENT || request.controller_id != 3 {
		t.Fatalf("unexpected add-segment request: %+v", request)
	}
	client.queueWg.Done()
}

// TestAddSegmentRejectsInvalidZoneCoverage verifies that adding a segment rejects collections with invalid zone coverage.
func TestAddSegmentRejectsInvalidZoneCoverage(t *testing.T) {
	tests := []struct {
		name       string
		collection SegmentsCollection
	}{
		{
			name:       "collection LED count mismatch",
			collection: SegmentsCollection{ZoneIdx: 1, NumLeds: 1, Segments: []SegmentItem{{Segment: Segment{StartIdx: 0, NumLeds: 2}}}},
		},
		{
			name:       "incomplete coverage",
			collection: SegmentsCollection{ZoneIdx: 1, NumLeds: 2, Segments: []SegmentItem{{Segment: Segment{StartIdx: 0, NumLeds: 1}}}},
		},
		{
			name: "non-contiguous segments",
			collection: SegmentsCollection{ZoneIdx: 1, NumLeds: 2, Segments: []SegmentItem{
				{Segment: Segment{StartIdx: 0, NumLeds: 1}},
				{Segment: Segment{StartIdx: 2, NumLeds: 1}},
			}},
		},
		{
			name:       "exceeds zone",
			collection: SegmentsCollection{ZoneIdx: 1, NumLeds: 2, Segments: []SegmentItem{{Segment: Segment{StartIdx: 0, NumLeds: 3}}}},
		},
		{
			name:       "empty collection",
			collection: SegmentsCollection{ZoneIdx: 1, NumLeds: 2},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, device := newQueueOnlyTestDeviceWithController()
			if err := device.AddSegment(test.collection); err == nil {
				t.Fatal("AddSegment accepted invalid zone coverage")
			}
			if len(client.queue) != 0 {
				t.Fatalf("AddSegment queued %d packets for an invalid collection", len(client.queue))
			}
		})
	}
}

// TestDeviceSetCustomModeSendsPacket verifies that calling SetCustomMode sends the correct packet to the server.
func TestDeviceSetCustomModeSendsPacket(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	device := &Device{ControllerID: 9, client: &Client{conn: &connection{conn: clientConn}}}
	result := make(chan error, 1)
	go func() { result <- device.SetCustomMode() }()
	controllerID, packetID, payload := readQueuedPacket(t, server)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if controllerID != 9 || packetID != uint32(PKT_RGBCONTROLLER_SETCUSTOMMODE) || len(payload) != 0 {
		t.Fatalf("unexpected custom-mode packet: controller=%d packet=%d payload=%v", controllerID, packetID, payload)
	}
}

// TestRefreshDeviceUpdatesControllerCache verifies that calling RefreshDevice updates the client's controller cache with the latest data from the server.
func TestRefreshDeviceUpdatesControllerCache(t *testing.T) {
	client, server := newProtocolTestClient(t, 5)
	device := &Device{ControllerID: 0, client: client}
	serverDone := make(chan error, 1)
	go func() {
		if err := checkProtocolTestPacket(server, 0, PKT_REQUEST_CONTROLLER_DATA, []byte{5, 0, 0, 0}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- sendProtocolTestPacket(server, 0, PKT_REQUEST_CONTROLLER_DATA, testControllerDataPayload())
	}()
	if err := device.RefreshDevice(); err != nil {
		t.Fatalf("RefreshDevice() error: %v", err)
	}
	if _, ok := client.rgbControllers["Test Keyboard"]; !ok {
		t.Fatal("RefreshDevice() did not update the controller cache")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}

// TestColorsWithSegmentUpdatePreservesOtherLEDs verifies that updating the colors for a specific segment preserves the colors of other LEDs.
func TestColorsWithSegmentUpdatePreservesOtherLEDs(t *testing.T) {
	originalColors := []RGBColor{{R: 1, G: 2, B: 3}, {R: 4, G: 5, B: 6}, {R: 7, G: 8, B: 9}, {R: 10, G: 11, B: 12}, {R: 13, G: 14, B: 15}}
	controller := &RGBController{
		LEDs:   make([]LED, len(originalColors)),
		Colors: append([]RGBColor(nil), originalColors...),
		Zones: []Zone{
			{NumLeds: 3, segmentData: []Segment{{Name: "first", StartIdx: 0, NumLeds: 1}, {Name: "second", StartIdx: 1, NumLeds: 2}}},
			{NumLeds: 2, segmentData: []Segment{{Name: "third", StartIdx: 0, NumLeds: 1}}},
		},
	}
	segmentColor := RGBColor{R: 255, G: 0, B: 128}
	got, err := colorsWithSegmentUpdate(controller, 0, 1, segmentColor)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]RGBColor(nil), originalColors...)
	want[1], want[2] = segmentColor, segmentColor
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("color %d = %+v, want %+v", index, got[index], want[index])
		}
	}
	if controller.Colors[1] != originalColors[1] || controller.Colors[2] != originalColors[2] {
		t.Fatal("segment update mutated the cached controller colors")
	}
}

// TestModeSupportsBothDirectionValues verifies that a mode correctly reports support for all valid direction values and rejects invalid ones.
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
