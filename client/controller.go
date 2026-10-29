// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"encoding/binary"
	"fmt"
)

// RGBController contains the cached state and capabilities of an OpenRGB controller.
type RGBController struct {
	Name            string     `json:"name"`
	ID              int        `json:"id"`
	Type            DeviceType `json:"type"`
	Vendor          string     `json:"vendor"`
	Model           string     `json:"model"`
	Serial          string     `json:"serial"`
	NumModes        uint16     `json:"num_modes"`
	ActiveMode      int        `json:"active_mode"`
	Modes           []Mode     `json:"modes"`
	NumZones        uint16     `json:"num_zones"`
	Zones           []Zone     `json:"zones"`
	LEDs            []LED      `json:"leds"`
	Colors          []RGBColor `json:"colors"`
	AltLedNames     []string   `json:"alt_led_names"`
	ControllerFlags uint32     `json:"controller_flags"`
}

// Mode describes a controller lighting mode, including its supported options and ranges.
type Mode struct {
	Name          string `json:"name"`
	id            int
	value         int
	Flags         Flags `json:"flags"`
	rawFlags      uint32
	SpeedMin      uint32 `json:"speed_min"`
	SpeedMax      uint32 `json:"speed_max"`
	BrightnessMin uint32 `json:"brightness_min"`
	BrightnessMax uint32 `json:"brightness_max"`
	ColorsMin     uint32 `json:"colors_min"`
	ColorsMax     uint32 `json:"colors_max"`
	speed         uint32
	brightness    uint32
	direction     uint32
	ColorMode     ColorMode `json:"color_mode"`
	numColors     uint16
	Colors        []RGBColor `json:"rgb_color"`
}

type modeFlags uint32

// Flags reports which options are supported by a lighting mode.
type Flags struct {
	flags modeFlags
}

func (f Flags) supports(flag modeFlags) bool { return f.flags&flag != 0 }

// SupportsSpeed reports whether this mode can change speed.
func (f Flags) SupportsSpeed() bool { return f.supports(HAS_SPEED) }

// SupportsBrightness reports whether this mode supports brightness control.
func (f Flags) SupportsBrightness() bool { return f.supports(HAS_BRIGHTNESS) }

// SupportsDirectionLR reports whether this mode supports left/right direction.
func (f Flags) SupportsDirectionLR() bool { return f.supports(HAS_DIR_LR) }

// SupportsDirectionUD reports whether this mode supports up/down direction.
func (f Flags) SupportsDirectionUD() bool { return f.supports(HAS_DIR_UD) }

// SupportsDirectionHV reports whether this mode supports horizontal/vertical direction.
func (f Flags) SupportsDirectionHV() bool { return f.supports(HAS_DIR_HV) }

// SupportsUniqueLEDColor reports whether the mode supports unique per-LED colors.
func (f Flags) SupportsUniqueLEDColor() bool { return f.supports(HAS_UNQ_COLOR) }

// SupportsModeColor reports whether the mode supports a mode-specific color palette.
func (f Flags) SupportsModeColor() bool { return f.supports(HAS_MODE_COLOR) }

// SupportsRandomColor reports whether the mode supports random colors.
func (f Flags) SupportsRandomColor() bool { return f.supports(HAS_RAND_COLOR) }

// SupportsSpeed reports whether this mode can change speed.
func (m Mode) SupportsSpeed() bool { return m.Flags.SupportsSpeed() }

// SupportsBrightness reports whether this mode supports brightness control.
func (m Mode) SupportsBrightness() bool { return m.Flags.SupportsBrightness() }

// SupportsDirectionLR reports whether this mode supports left/right direction.
func (m Mode) SupportsDirectionLR() bool { return m.Flags.SupportsDirectionLR() }

// SupportsDirectionUD reports whether this mode supports up/down direction.
func (m Mode) SupportsDirectionUD() bool { return m.Flags.SupportsDirectionUD() }

// SupportsDirectionHV reports whether this mode supports horizontal/vertical direction.
func (m Mode) SupportsDirectionHV() bool { return m.Flags.SupportsDirectionHV() }

// SupportsUniqueLEDColor reports whether the mode supports unique per-LED colors.
func (m Mode) SupportsUniqueLEDColor() bool { return m.Flags.SupportsUniqueLEDColor() }

// SupportsModeColor reports whether the mode supports a mode-specific color palette.
func (m Mode) SupportsModeColor() bool { return m.Flags.SupportsModeColor() }

// SupportsRandomColor reports whether the mode supports random colors.
func (m Mode) SupportsRandomColor() bool { return m.Flags.SupportsRandomColor() }

// Zone describes a controller lighting zone and its LED layout.
type Zone struct {
	Name         string `json:"name"`
	zoneIdx      int
	ZoneType     int `json:"zone_type"`
	minLeds      uint32
	maxLeds      uint32
	NumLeds      uint32 `json:"num_leds"`
	matrixLength uint16
	matrixWidth  uint32
	matrixHeight uint32
	matrixData   [][]int32
	Segments     uint16 `json:"segments"`
	segmentData  []Segment
	flags        uint32
}

// Segment describes a contiguous range of LEDs within a zone.
type Segment struct {
	Name        string `json:"name"`
	SegmentType int    `json:"segment_type"`
	StartIdx    uint32 `json:"start_idx"`
	NumLeds     uint32 `json:"num_leds"`
}

// LED identifies an LED by its server-provided name and index.
type LED struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

// ControllerFactory decodes a server controller payload into cached controller state.
type ControllerFactory struct {
	payload        []byte
	protocol       int
	controller_id  int
	rgb_controller RGBController
	decoder        PacketDecoder
}

// makeController decodes one server controller payload into the public model.
func (f *ControllerFactory) makeController() (RGBController, error) {
	f.decoder = *newPacketDecoder(f.payload, 4)
	f.rgb_controller.ID = f.controller_id
	err := f.parseMetaData()
	if err != nil {
		return f.rgb_controller, err
	}
	err = f.parseModes()
	if err != nil {
		return f.rgb_controller, err
	}
	err = f.parseZones()
	if err != nil {
		return f.rgb_controller, err
	}
	err = f.parseLeds()
	if err != nil {
		return f.rgb_controller, err
	}
	err = f.parseColors()
	if err != nil {
		return f.rgb_controller, err
	}
	if f.protocol == 5 {
		err = f.parseExtras()
		if err != nil {
			return f.rgb_controller, err
		}
	}
	return f.rgb_controller, nil
}

// parseMetaData reads controller identity fields, including fields gated by protocol version.
func (f *ControllerFactory) parseMetaData() error {

	f.rgb_controller.Type = DeviceType(f.decoder.uint32())
	f.rgb_controller.Name = f.decoder.varStr()
	if f.protocol >= 1 {
		f.rgb_controller.Vendor = f.decoder.varStr()
	}
	f.decoder.varStr() // skip description move decoder on to next field
	f.rgb_controller.Model = f.decoder.varStr()
	f.rgb_controller.Serial = f.decoder.varStr()
	f.decoder.varStr() // skip location move decoder on to next field

	return f.decoder.Err()
}

// parseModes decodes each mode and reads brightness fields only when the protocol provides them.
func (f *ControllerFactory) parseModes() error {

	f.rgb_controller.NumModes = f.decoder.uint16()
	f.rgb_controller.ActiveMode = int(f.decoder.uint32())

	for i := 0; i < int(f.rgb_controller.NumModes); i++ {
		mode_data := Mode{}
		mode_data.Name = f.decoder.varStr()
		mode_data.value = int(f.decoder.uint32())
		mode_data.id = i
		mode_data.rawFlags = f.decoder.uint32()
		mode_data.Flags = decodeModeFlags(modeFlags(mode_data.rawFlags))

		mode_data.SpeedMin = f.decoder.uint32()
		mode_data.SpeedMax = f.decoder.uint32()

		if f.protocol > 2 {
			mode_data.BrightnessMin = f.decoder.uint32()
			mode_data.BrightnessMax = f.decoder.uint32()
		}
		mode_data.ColorsMin = f.decoder.uint32()
		mode_data.ColorsMax = f.decoder.uint32()
		mode_data.speed = f.decoder.uint32()
		if f.protocol > 2 {
			mode_data.brightness = f.decoder.uint32()
		}
		mode_data.direction = f.decoder.uint32()
		mode_data.ColorMode = ColorMode(f.decoder.uint32())
		mode_data.numColors = f.decoder.uint16()
		mode_data.Colors = make([]RGBColor, 0, int(mode_data.numColors))

		for i := 0; i < int(mode_data.numColors); i++ {
			color_value := f.decoder.uint32()
			RGB := ConvertToRGBColor(color_value)
			mode_data.Colors = append(mode_data.Colors, RGB)
		}
		f.rgb_controller.Modes = append(f.rgb_controller.Modes, mode_data)
	}
	return f.decoder.Err()

}

// parseZones decodes zones in wire order and assigns each its controller-local index.
func (f *ControllerFactory) parseZones() error {

	f.rgb_controller.NumZones = f.decoder.uint16()

	for i := 0; i < int(f.rgb_controller.NumZones); i++ {
		zone_data, err := f.parseZoneData()
		if err != nil {
			return fmt.Errorf("failed to parse zone %d: %w", i, err)
		}
		zone_data.zoneIdx = i
		f.rgb_controller.Zones = append(f.rgb_controller.Zones, zone_data)
	}
	return f.decoder.Err()

}

// parseZoneData reads zone dimensions, optional matrix data, and protocol-gated segments and flags.
func (f *ControllerFactory) parseZoneData() (Zone, error) {
	zone_data := Zone{}
	zone_data.Name = f.decoder.varStr()
	zone_data.ZoneType = int(f.decoder.uint32())

	zone_data.minLeds = f.decoder.uint32()
	zone_data.maxLeds = f.decoder.uint32()
	zone_data.NumLeds = f.decoder.uint32()
	zone_data.matrixLength = f.decoder.uint16()
	// Check if matrix data exists
	if zone_data.matrixLength > 0 {
		if zone_data.matrixLength < 8 {
			return zone_data, fmt.Errorf("invalid matrix length %d", zone_data.matrixLength)
		}
		zone_data.matrixWidth = f.decoder.uint32()
		zone_data.matrixHeight = f.decoder.uint32()
		// Read the whole matrix data as bytes (after width and height fields)
		data, err := f.decoder.varBytes(zone_data.matrixLength - 8)
		if err != nil {
			return zone_data, fmt.Errorf("error reading matrix data: %w", err)
		}
		expectedBytes := uint64(zone_data.matrixWidth) * uint64(zone_data.matrixHeight) * 4
		if expectedBytes > uint64(len(data)) {
			return zone_data, fmt.Errorf("matrix dimensions require %d bytes, got %d", expectedBytes, len(data))
		}

		// Initialise the matrix slice for 2D data
		zone_data.matrixData = make([][]int32, zone_data.matrixHeight)

		// Process the data in 4-byte chunks (32 bits per LED)
		matrixIndex := 0 // To keep track of LED position in matrix

		for y := 0; y < int(zone_data.matrixHeight); y++ {
			zone_data.matrixData[y] = make([]int32, zone_data.matrixWidth)

			for x := 0; x < int(zone_data.matrixWidth); x++ {
				// Check if there's enough data to read 4 bytes
				if len(data) < 4 {
					return zone_data, fmt.Errorf("not enough data to unpack the matrix")
				}

				// Read the next 4 bytes (32-bit value for each LED)
				val := binary.LittleEndian.Uint32(data[:4]) // Read 32 bits (4 bytes) for one LED
				data = data[4:]                             // Move to the next 4-byte chunk

				// OpenRGB uses all bits set to mark an empty matrix cell.
				if val == 0xFFFFFFFF {
					zone_data.matrixData[y][x] = -1 // Invalid LED (you can use -1 or another special value)
				} else {
					zone_data.matrixData[y][x] = int32(val) // Valid LED value
				}
				matrixIndex++
			}
		}
	}
	if f.protocol > 3 {
		zone_data.Segments = f.decoder.uint16()
		if zone_data.Segments > 0 {
			for i := 0; i < int(zone_data.Segments); i++ {
				segment := Segment{}
				segment.Name = f.decoder.varStr()
				segment.SegmentType = int(f.decoder.uint32())
				segment.StartIdx = f.decoder.uint32()
				segment.NumLeds = f.decoder.uint32()
				zone_data.segmentData = append(zone_data.segmentData, segment)
			}
		}
	}
	if f.protocol > 4 {
		zone_data.flags = f.decoder.uint32()
	}
	if err := f.decoder.Err(); err != nil {
		return zone_data, err
	}
	return zone_data, nil
}

// parseLeds assigns zero-based controller indices to the server-provided LED names.
func (f *ControllerFactory) parseLeds() error {

	num_leds := f.decoder.uint16()
	for i := 0; i < int(num_leds); i++ {
		led_data := LED{}
		led_data.Name = f.decoder.varStr()
		f.decoder.uint32() // skip the value field
		led_data.Value = int(i)
		f.rgb_controller.LEDs = append(f.rgb_controller.LEDs, led_data)
	}
	return f.decoder.Err()
}

// parseColors decodes the cached color value for each controller LED.
func (f *ControllerFactory) parseColors() error {

	num_colors := f.decoder.uint16()
	for i := 0; i < int(num_colors); i++ {
		color_data := RGBColor{}
		color_value := f.decoder.uint32()
		color_data = ConvertToRGBColor(color_value)
		f.rgb_controller.Colors = append(f.rgb_controller.Colors, color_data)
	}
	return f.decoder.Err()
}

// parseExtras reads alternate LED names and controller flags added in protocol version 5.
func (f *ControllerFactory) parseExtras() error {

	// Parse alternate names for each LED
	num_alt_names := f.decoder.uint16()
	for i := 0; i < int(num_alt_names); i++ {
		alt_name := f.decoder.varStr()
		f.rgb_controller.AltLedNames = append(f.rgb_controller.AltLedNames, alt_name)
	}
	// Parse controller flags
	f.rgb_controller.ControllerFlags = f.decoder.uint32()
	return f.decoder.Err()
}
