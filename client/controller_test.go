// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"reflect"
	"testing"
)

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
	if !mode.Flags.SupportsSpeed() || !mode.Flags.SupportsBrightness() || !mode.Flags.SupportsDirectionLR() || !mode.Flags.SupportsModeColor() {
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

func TestControllerCapabilityFlags(t *testing.T) {
	flags := Flags{flags: HAS_UNQ_COLOR | HAS_RAND_COLOR}
	if !flags.SupportsUniqueLEDColor() || !flags.SupportsRandomColor() {
		t.Fatalf("capability flags not reported: %+v", flags)
	}
}

func TestModeFlagsExposeCapabilities(t *testing.T) {
	flags := decodeModeFlags(HAS_SPEED | HAS_BRIGHTNESS | HAS_DIR_LR | HAS_DIR_UD | HAS_DIR_HV | HAS_UNQ_COLOR | HAS_MODE_COLOR | HAS_RAND_COLOR)
	if !flags.SupportsSpeed() || !flags.SupportsBrightness() || !flags.SupportsDirectionLR() || !flags.SupportsDirectionUD() || !flags.SupportsDirectionHV() || !flags.SupportsUniqueLEDColor() || !flags.SupportsModeColor() {
		t.Fatalf("expected all capability helpers to report true for the corresponding flags: %+v", flags)
	}
	if !flags.SupportsRandomColor() {
		t.Fatal("expected random color capability to report true")
	}
}
