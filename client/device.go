// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// ModeOptions contains the values applied by mode update operations.
// Values that the selected mode does not support are ignored.
type ModeOptions struct {
	Colors        []RGBColor `json:"colors"`
	ColorMode     ColorMode  `json:"color_mode"`
	Speed         Speed      `json:"speed"`
	Brightness    int        `json:"brightness"`
	Direction     Direction  `json:"direction"`
	SaveToDevice  bool       `json:"save_to_device"`
	brightnessSet bool       `json:"-"`
}

// QueueWriteOptions configures how a queued write should be handled.
type QueueWriteOptions struct {
	// SkipRefresh skips the automatic controller refresh scheduled for packets
	// in AutoRefreshRequestTypes.
	SkipRefresh bool
}

// Device represents a connected RGB controller and provides operations for its LEDs,
// zones, and lighting modes. Writes are queued unless otherwise noted.
type Device struct {
	ControllerID uint32 `json:"controller_id"` // Thread-safe ID reference
	conn         *connection
	Mode         string      `json:"mode"`         // Current mode name
	ModeIdx      int         `json:"mode_idx"`     // Current mode index
	ModeOptions  ModeOptions `json:"mode_options"` // Options for the current mode
	client       *Client     // Reference back to parent client for state sync
}

// controller retrieves the RGBController associated with this device from the client.
func (d *Device) controller() (*RGBController, error) {
	if d.client == nil {
		return nil, fmt.Errorf("device not attached to a client")
	}
	return d.client.GetController(d.ControllerID)
}

// GetLEDs returns the server-provided LED data for this device.
func (d *Device) GetLEDs() ([]LED, error) {
	controller, err := d.controller()
	if err != nil {
		return nil, err
	}
	return controller.LEDs, nil
}

// GetZones returns the server-provided zone data for this device.
func (d *Device) GetZones() ([]Zone, error) {
	controller, err := d.controller()
	if err != nil {
		return nil, err
	}
	return controller.Zones, nil
}

// GetSegments returns the server-provided segments for a zone by index.
func (d *Device) GetSegments(zoneIndex int) ([]Segment, error) {
	controller, err := d.controller()
	if err != nil {
		return nil, err
	}
	if zoneIndex < 0 || zoneIndex >= len(controller.Zones) {
		return nil, fmt.Errorf("zone index %d is out of range", zoneIndex)
	}
	return append([]Segment(nil), controller.Zones[zoneIndex].segmentData...), nil
}

// GetModes returns the server-provided mode data for this device.
func (d *Device) GetModes() ([]Mode, error) {
	controller, err := d.controller()
	if err != nil {
		return nil, err
	}
	return controller.Modes, nil
}

// GetMode returns the current mode data for this device.
func (d *Device) GetMode() (*Mode, error) {
	controller, err := d.controller()
	if err != nil {
		return nil, err
	}
	if d.ModeIdx < 0 || d.ModeIdx >= len(controller.Modes) {
		return nil, fmt.Errorf("invalid mode index %d for controller %d", d.ModeIdx, controller.ID)
	}
	return &controller.Modes[d.ModeIdx], nil
}

// UpdateLedWithOptions queues a color update for one LED with write options.
// Use QueueWriteOptions.SkipRefresh for high-frequency animations.
func (d *Device) UpdateLedWithOptions(ledName string, color RGBColor, opts ...QueueWriteOptions) error {
	controller, err := d.client.GetController(d.ControllerID)
	if err != nil {
		return err
	}

	ledID, err := controller.ledIndexByName(ledName)
	if err != nil {
		return err
	}

	encoder := newPacketEncoder(d.client.protocol)
	encoder.uint32(uint32(ledID))
	encoder.uint32(ConvertRGBtoUint32(color))

	return d.client.QueueWrite(uint32(controller.ID), PKT_RGBCONTROLLER_UPDATESINGLELED, encoder.getPayload(), opts...)
}

// SetCustomMode enables direct LED and zone control for this device.
// Unlike queued LED updates, the command is sent immediately.
func (d *Device) SetCustomMode() error {
	err := d.client.sendPacket(d.ControllerID, PKT_RGBCONTROLLER_SETCUSTOMMODE, nil)
	if err != nil {
		return err
	}
	return nil
}

// UpdateLeds queues the same color update for every LED on this device.
func (d *Device) UpdateLeds(color RGBColor) error {
	controller, err := d.client.GetController(d.ControllerID)
	if err != nil {
		return err
	}

	colors := make([]RGBColor, len(controller.LEDs))
	for index := range colors {
		colors[index] = color
	}
	return d.UpdateLedsWithColors(colors)
}

// UpdateLedsWithColors queues one packet containing a color for every LED on
// this device. The colors slice must match the device's LED count.
// QueueWriteOptions can be used to skip refreshes during animations.
func (d *Device) UpdateLedsWithColors(colors []RGBColor, opts ...QueueWriteOptions) error {
	controller, err := d.client.GetController(d.ControllerID)
	if err != nil {
		return err
	}
	if len(colors) != len(controller.LEDs) {
		return fmt.Errorf("expected %d LED colors, got %d", len(controller.LEDs), len(colors))
	}

	payload, err := buildLengthPrefixedPayload(func(payload *PacketEncoder) error {
		payload.uint16(uint16(len(colors)))
		for _, color := range colors {
			payload.uint32(ConvertRGBtoUint32(color))
		}
		return nil
	})
	if err != nil {
		return err
	}

	return d.client.QueueWrite(uint32(controller.ID), PKT_RGBCONTROLLER_UPDATELEDS, payload, opts...)
}

// UpdateZoneLeds queues the same color update for every LED in a named zone.
// Use FlushWrites to wait for the write and collect any asynchronous error.
func (d *Device) UpdateZoneLeds(color RGBColor, zone string) error {
	controller, err := d.client.GetController(d.ControllerID)
	if err != nil {
		return err
	}

	zoneData, err := controller.zoneByName(zone)
	if err != nil {
		return err
	}

	payload, err := buildLengthPrefixedPayload(func(payload *PacketEncoder) error {
		payload.uint32(uint32(zoneData.zoneIdx))
		payload.uint16(0) // placeholder for num_colors
		for i := 0; i < int(zoneData.NumLeds); i++ {
			payload.uint32(ConvertRGBtoUint32(color))
		}
		binary.LittleEndian.PutUint16(payload.buffer.Bytes()[8:10], uint16(zoneData.NumLeds))
		return nil
	})
	if err != nil {
		return err
	}

	return d.client.QueueWrite(uint32(controller.ID), PKT_RGBCONTROLLER_UPDATEZONELEDS, payload)
}

// UpdateSegmentLeds queues a full-device LED update with the selected segment
// changed and all other LEDs kept at their cached colors.
func (d *Device) UpdateSegmentLeds(zoneIndex, segmentIndex int, color RGBColor) error {
	controller, err := d.client.GetController(d.ControllerID)
	if err != nil {
		return err
	}
	colors, err := colorsWithSegmentUpdate(controller, zoneIndex, segmentIndex, color)
	if err != nil {
		return err
	}
	return d.UpdateLedsWithColors(colors)
}

// colorsWithSegmentUpdate returns a full-device color copy with one segment changed.
// Zone offsets translate each zone-local segment index to the controller-wide LED list.
func colorsWithSegmentUpdate(controller *RGBController, zoneIndex, segmentIndex int, color RGBColor) ([]RGBColor, error) {
	if zoneIndex < 0 || zoneIndex >= len(controller.Zones) {
		return nil, fmt.Errorf("zone index %d is out of range", zoneIndex)
	}
	zone := &controller.Zones[zoneIndex]
	if segmentIndex < 0 || segmentIndex >= len(zone.segmentData) {
		return nil, fmt.Errorf("segment index %d is out of range for zone %d", segmentIndex, zoneIndex)
	}
	if len(controller.Colors) != len(controller.LEDs) {
		return nil, fmt.Errorf("expected %d cached LED colors, got %d", len(controller.LEDs), len(controller.Colors))
	}
	segment := zone.segmentData[segmentIndex]
	if uint64(segment.StartIdx)+uint64(segment.NumLeds) > uint64(zone.NumLeds) {
		return nil, fmt.Errorf("segment %d exceeds zone %d LED bounds", segmentIndex, zoneIndex)
	}
	zoneOffset := uint64(0)
	for index := 0; index < zoneIndex; index++ {
		zoneOffset += uint64(controller.Zones[index].NumLeds)
	}
	start := zoneOffset + uint64(segment.StartIdx)
	end := start + uint64(segment.NumLeds)
	if end > uint64(len(controller.Colors)) {
		return nil, fmt.Errorf("segment %d in zone %d exceeds device LED count", segmentIndex, zoneIndex)
	}

	colors := append([]RGBColor(nil), controller.Colors...)
	for index := start; index < end; index++ {
		colors[int(index)] = color
	}
	return colors, nil
}

// updateMode queues the current mode options, optionally saving them to device memory.
func (d *Device) updateMode(SaveToDevice bool) error {
	controller, err := d.client.GetController(d.ControllerID)
	if err != nil {
		return err
	}

	_, modeIdx, err := controller.modeByName(d.Mode)
	if err != nil {
		return err
	}
	d.ModeIdx = modeIdx

	payload, err := buildLengthPrefixedPayload(func(e *PacketEncoder) error {
		e.uint32(uint32(d.ModeIdx))
		modePayload, err := d._buildMode()
		if err != nil {
			return err
		}
		e.writeBytes(modePayload)
		return nil
	})
	if err != nil {
		return err
	}

	controllerId := controller.ID
	if SaveToDevice {
		if err := d.requireProtocol(3, "SaveMode"); err != nil {
			return err
		}
		if err := d.client.QueueWrite(uint32(controllerId), PKT_RGBCONTROLLER_SAVEMODE, payload); err != nil {
			return err
		}
	} else {
		if err := d.client.QueueWrite(uint32(controllerId), PKT_RGBCONTROLLER_UPDATEMODE, payload); err != nil {
			return err
		}
	}

	return nil
}

// _buildMode serializes the selected mode and uses server values for options the caller left unset.
func (d *Device) _buildMode() ([]byte, error) {
	controller, err := d.client.GetController(d.ControllerID)
	if err != nil {
		return nil, err
	}

	mode := controller.Modes[d.ModeIdx]

	// initialize mode options with server defaults
	options := ModeOptions{
		ColorMode:  ColorMode(mode.ColorMode),
		Speed:      Speed(mode.speed),
		Brightness: int(mode.brightness),
		Direction:  Direction(mode.direction),
	}

	// override mode options with user-specified values if provided
	if len(d.ModeOptions.Colors) > 0 {
		options.Colors = append([]RGBColor(nil), d.ModeOptions.Colors...)
	} else {
		options.Colors = make([]RGBColor, mode.ColorsMin)
	}
	if len(options.Colors) > int(^uint16(0)) {
		return nil, fmt.Errorf("mode color palette has %d colors, maximum is %d", len(options.Colors), int(^uint16(0)))
	}

	if mode.Flags.SupportsSpeed() {
		options.Speed = d.ModeOptions.Speed
	}
	if mode.Flags.SupportsBrightness() && (d.ModeOptions.brightnessSet || d.ModeOptions.Brightness != 0) {
		options.Brightness = d.ModeOptions.Brightness
	}
	if d.ModeOptions.ColorMode != 0 {
		options.ColorMode = d.ModeOptions.ColorMode
	}
	if modeSupportsDirection(mode, d.ModeOptions.Direction) {
		options.Direction = d.ModeOptions.Direction
	}

	// serialize mode and options into payload
	payload := new(bytes.Buffer)
	modeName := append([]byte(mode.Name), 0)
	binary.Write(payload, binary.LittleEndian, uint16(len(modeName)))
	payload.Write(modeName)
	binary.Write(payload, binary.LittleEndian, uint32(mode.value))
	binary.Write(payload, binary.LittleEndian, uint32(mode.rawFlags))
	binary.Write(payload, binary.LittleEndian, uint32(mode.SpeedMin))
	binary.Write(payload, binary.LittleEndian, uint32(mode.SpeedMax))
	binary.Write(payload, binary.LittleEndian, uint32(mode.BrightnessMin))
	binary.Write(payload, binary.LittleEndian, uint32(mode.BrightnessMax))
	binary.Write(payload, binary.LittleEndian, uint32(mode.ColorsMin))
	binary.Write(payload, binary.LittleEndian, uint32(mode.ColorsMax))
	binary.Write(payload, binary.LittleEndian, uint32(options.Speed))
	binary.Write(payload, binary.LittleEndian, uint32(options.Brightness))
	binary.Write(payload, binary.LittleEndian, uint32(options.Direction))
	binary.Write(payload, binary.LittleEndian, uint32(options.ColorMode))
	binary.Write(payload, binary.LittleEndian, uint16(len(options.Colors)))

	for _, color := range options.Colors {
		binary.Write(payload, binary.LittleEndian, uint32(ConvertRGBtoUint32(color)))
	}
	return payload.Bytes(), nil
}

func modeSupportsDirection(mode Mode, direction Direction) bool {
	// Direction values are grouped by the protocol capability flag they require.
	switch direction {
	case LEFT, RIGHT:
		return mode.Flags.SupportsDirectionLR()
	case UP, DOWN:
		return mode.Flags.SupportsDirectionUD()
	case HORIZONTAL, VERTICAL:
		return mode.Flags.SupportsDirectionHV()
	default:
		return false
	}
}

// SetMode changes the device's active mode and queues its configured options.
// If ModeOptions.SaveToDevice is true, it also queues a save to device memory.
// Use FlushWrites to wait for these writes and collect asynchronous errors.
func (d *Device) SetMode(modeName string) error {
	previousMode, previousModeIdx := d.Mode, d.ModeIdx
	d.Mode = modeName
	err := d.updateMode(false)
	if err != nil {
		d.Mode, d.ModeIdx = previousMode, previousModeIdx
		return err
	}
	if d.ModeOptions.SaveToDevice {
		if err := d.updateMode(true); err != nil {
			d.Mode, d.ModeIdx = previousMode, previousModeIdx
			return err
		}
	}
	return nil
}

// applyModeUpdate applies the current mode options to the device, optionally saving to device memory if required.
// It returns any error encountered during the update.
func (d *Device) applyModeUpdate() error {
	if d.ModeOptions.SaveToDevice {
		return d.updateMode(true)
	}
	return d.updateMode(false)
}

// requireProtocol checks that the client's protocol version meets the minimum required for a feature.
func (d *Device) requireProtocol(minVersion int, feature string) error {
	if d.client == nil {
		return fmt.Errorf("device not attached to a client")
	}
	return d.client.requireProtocol(minVersion, feature)
}

// SetSpeed sets the speed of the current mode when that mode supports speed.
func (d *Device) SetSpeed(speed Speed) error {
	d.ModeOptions.Speed = speed
	return d.applyModeUpdate()
}

// SetBrightness sets the brightness of the current mode when supported.
func (d *Device) SetBrightness(brightness int) error {
	d.ModeOptions.Brightness = brightness
	d.ModeOptions.brightnessSet = true
	return d.applyModeUpdate()
}

// SetDirection sets the direction of the current mode when supported.
func (d *Device) SetDirection(direction Direction) error {
	d.ModeOptions.Direction = direction
	return d.applyModeUpdate()
}

// SetColors sets the color palette and color mode of the current mode.
func (d *Device) SetColors(colors []RGBColor, color_mode ColorMode) error {
	d.ModeOptions.Colors = colors
	d.ModeOptions.ColorMode = color_mode
	return d.applyModeUpdate()
}

// RefreshDevice refreshes this device's controller state from the server.
func (d *Device) RefreshDevice() error {
	if d.client == nil {
		return fmt.Errorf("device not attached to a client")
	}

	// Delegate to client to refresh the controller state
	return d.client.RefreshController(d.ControllerID)
}

// ResizeZone queues a change to the number of LEDs assigned to a zone.
// The new size must be accepted by the server's zone limits.
func (d *Device) ResizeZone(zoneName string, newSize uint32) error {
	controller, err := d.client.GetController(d.ControllerID)
	if err != nil {
		return err
	}

	zoneData, err := controller.zoneByName(zoneName)
	if err != nil {
		return err
	}

	payload := new(bytes.Buffer)
	binary.Write(payload, binary.LittleEndian, uint32(zoneData.zoneIdx))
	binary.Write(payload, binary.LittleEndian, uint32(newSize))
	return d.client.QueueWrite(uint32(controller.ID), PKT_RGBCONTROLLER_RESIZEZONE, payload.Bytes())
}

// ClearSegments removes all segments from a named zone. It requires protocol 5.
func (d *Device) ClearSegments(zoneName string) error {
	if err := d.requireProtocol(5, "ClearSegments"); err != nil {
		return err
	}

	controller, err := d.client.GetController(d.ControllerID)
	if err != nil {
		return err
	}

	zoneData, err := controller.zoneByName(zoneName)
	if err != nil {
		return err
	}

	payload := new(bytes.Buffer)
	binary.Write(payload, binary.LittleEndian, int32(zoneData.zoneIdx))
	return d.client.QueueWrite(uint32(controller.ID), PKT_RGBCONTROLLER_CLEAR_SEGMENTS, payload.Bytes())
}

// NewSegment creates an empty segment collection for a zone. Populate it so
// segments cover the entire zone, then pass it to AddSegment.
func (d *Device) NewSegment(zoneName string) (*SegmentsCollection, error) {
	if err := d.requireProtocol(5, "NewSegment"); err != nil {
		return nil, err
	}

	controller, err := d.client.GetController(d.ControllerID)
	if err != nil {
		return nil, err
	}

	zoneData, err := controller.zoneByName(zoneName)
	if err != nil {
		return nil, err
	}

	return &SegmentsCollection{ZoneIdx: zoneData.zoneIdx, NumLeds: zoneData.NumLeds}, nil
}

// AddSegment queues a complete, contiguous segments collection for a zone.
// The segments must cover every LED in the zone without gaps or overlaps.
// Use FlushWrites to wait for the individual segment writes to complete.
func (d *Device) AddSegment(segmentsData SegmentsCollection) error {
	if err := d.requireProtocol(5, "AddSegment"); err != nil {
		return err
	}

	controller, err := d.client.GetController(d.ControllerID)
	if err != nil {
		return err
	}
	if segmentsData.ZoneIdx < 0 || segmentsData.ZoneIdx >= len(controller.Zones) {
		return fmt.Errorf("zone index %d is out of range", segmentsData.ZoneIdx)
	}
	zone := controller.Zones[segmentsData.ZoneIdx]
	if segmentsData.NumLeds != zone.NumLeds {
		return fmt.Errorf("segment collection has %d LEDs, zone has %d", segmentsData.NumLeds, zone.NumLeds)
	}

	var nextLED uint64
	for index, segmentItem := range segmentsData.Segments {
		segment := segmentItem.Segment
		if segment.NumLeds == 0 {
			return fmt.Errorf("segment %d has no LEDs", index)
		}
		if uint64(segment.StartIdx) != nextLED {
			return fmt.Errorf("segment %d starts at LED %d, expected LED %d", index, segment.StartIdx, nextLED)
		}
		nextLED += uint64(segment.NumLeds)
		if nextLED > uint64(zone.NumLeds) {
			return fmt.Errorf("segments exceed zone LED count %d", zone.NumLeds)
		}
	}
	if nextLED != uint64(zone.NumLeds) {
		return fmt.Errorf("segments cover %d of %d zone LEDs", nextLED, zone.NumLeds)
	}

	controllerId := controller.ID

	for _, segmentItem := range segmentsData.Segments {
		segment := segmentItem.Segment
		payload, err := buildLengthPrefixedPayload(func(e *PacketEncoder) error {
			e.uint32(uint32(segmentsData.ZoneIdx))
			e.uint16(uint16(len(segment.Name) + 1))
			e.writeStringNullTerminated(segment.Name)
			e.uint32(uint32(segment.SegmentType))
			e.uint32(segment.StartIdx)
			e.uint32(segment.NumLeds)
			return nil
		})
		if err != nil {
			return err
		}
		if err := d.client.QueueWrite(uint32(controllerId), PKT_RGBCONTROLLER_ADD_SEGMENT, payload); err != nil {
			return err
		}
	}

	return nil
}
