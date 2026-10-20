// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// RGBColor represents an RGB color with red, green, and blue components.
type RGBColor struct {
	R uint8
	G uint8
	B uint8
}

// ConvertToRGBColor converts an OpenRGB packed color value to RGBColor.
func ConvertToRGBColor(color uint32) RGBColor {
	r := uint8(color) & 0xFF
	g := uint8(color>>8) & 0xFF
	b := uint8(color>>16) & 0xFF
	return RGBColor{R: r, G: g, B: b}
}

// ConvertRGBtoUint32 converts RGBColor to the packed color format used by OpenRGB.
func ConvertRGBtoUint32(color RGBColor) uint32 {
	colorValue := uint32(color.B)<<16 | (uint32(color.G) << 8) | uint32(color.R)
	return colorValue
}

// check if a slice contains a packet type
func contains(slice []PacketType, item PacketType) bool {
	for _, v := range slice {
		if v == item {
			return true
		}
	}
	return false
}

// convert int to bool
func intToBool(n int) bool {
	return n != 0
}

// PacketDecoder reads little-endian values from a protocol payload. A failed
// read is recorded and subsequent reads return zero values until Err is checked.
type PacketDecoder struct {
	offset int
	start  int
	buffer []byte
	err    error
}

// create a new PacketDecoder with the given payload and offset
func newPacketDecoder(payload []byte, offset int) *PacketDecoder {
	return &PacketDecoder{offset: offset, buffer: payload}
}

// uint16 reads a uint16 from the buffer and advances the offset
func (p *PacketDecoder) uint16() uint16 {
	data, ok := p.read(2)
	if !ok {
		return 0
	}
	value := binary.LittleEndian.Uint16(data)
	return value
}

// uint32 reads a uint32 from the buffer and advances the offset
func (p *PacketDecoder) uint32() uint32 {
	data, ok := p.read(4)
	if !ok {
		return 0
	}
	value := binary.LittleEndian.Uint32(data)
	return value
}

// Byte reads a single byte from the buffer and advances the offset
func (p *PacketDecoder) Byte() int {
	data, ok := p.read(1)
	if !ok {
		return 0
	}
	return int(data[0])
}

// varStr reads a variable-length string from the buffer and advances the offset
func (p *PacketDecoder) varStr() string {
	length := int(p.uint16())
	if p.err != nil {
		return ""
	}
	data, ok := p.read(length)
	if !ok {
		return ""
	}
	value := string(data)
	return strings.TrimRight(value, "\x00")
}

// varBytes reads a variable-length byte slice from the buffer and advances the offset
func (p *PacketDecoder) varBytes(length uint16) ([]byte, error) {
	value, ok := p.read(int(length))
	if !ok {
		return nil, p.err
	}
	return value, nil
}

// read attempts to read the specified number of bytes from the buffer, returning an error if insufficient data is available.
func (p *PacketDecoder) read(size int) ([]byte, bool) {
	if p.err != nil {
		return nil, false
	}
	if size < 0 || p.offset < 0 || size > len(p.buffer)-p.offset {
		p.err = fmt.Errorf("insufficient data to read %d bytes at offset %d", size, p.offset)
		return nil, false
	}
	value := p.buffer[p.offset : p.offset+size]
	p.offset += size
	return value, true
}

func (p *PacketDecoder) Err() error {
	return p.err
}

// PacketEncoder builds protocol payloads using little-endian integer fields.
type PacketEncoder struct {
	buffer   bytes.Buffer
	protocol int
}

// create a new PacketEncoder with the given protocol version
func newPacketEncoder(protocol int) *PacketEncoder {
	return &PacketEncoder{protocol: protocol}
}

// uint16 writes a uint16 to the buffer in little-endian format
func (e *PacketEncoder) uint16(value uint16) {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, value)
	e.buffer.Write(b)
}

// uint32 writes a uint32 to the buffer in little-endian format
func (e *PacketEncoder) uint32(value uint32) {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, value)
	e.buffer.Write(b)
}

// returns the current payload as a byte slice
func (e *PacketEncoder) getPayload() []byte {
	return e.buffer.Bytes()
}

// writeBytes writes a byte slice to the buffer
func (e *PacketEncoder) writeBytes(data []byte) {
	e.buffer.Write(data)
}

// writeStringNullTerminated writes a string to the buffer with a null terminator
func (e *PacketEncoder) writeStringNullTerminated(value string) {
	e.buffer.WriteString(value)
	e.buffer.WriteByte(0)
}

// setLengthPrefix writes a length prefix to the buffer
func (e *PacketEncoder) setLengthPrefix() error {
	payload := e.buffer.Bytes()
	if len(payload) < 4 {
		return fmt.Errorf("payload too short to set length prefix")
	}
	binary.LittleEndian.PutUint32(payload[0:4], uint32(len(payload)))
	return nil
}

// buildLengthPrefixedPayload builds a payload with a length prefix using the provided build function
func buildLengthPrefixedPayload(build func(*PacketEncoder) error) ([]byte, error) {
	encoder := newPacketEncoder(0)
	encoder.uint32(0)
	if err := build(encoder); err != nil {
		return nil, err
	}
	if err := encoder.setLengthPrefix(); err != nil {
		return nil, err
	}
	return encoder.getPayload(), nil
}

// sendPacket sends a packet to the OpenRGB server immediately, bypassing the queue
func (c *Client) sendPacket(controller_id uint32, packetType PacketType, payload []byte) error {
	c.socketMu.Lock()
	defer c.socketMu.Unlock()
	return c.conn.SendPacket(controller_id, uint32(packetType), payload)
}

// requestPacket sends a request packet to the OpenRGB server and waits for the response.
// The request mutex is required because the protocol has no request identifier.
func (c *Client) requestPacket(controller_id uint32, packetType PacketType, payload []byte) (*PacketHeader, []byte, error) {
	// The protocol does not include a request ID, so only one request may be
	// registered and awaiting a response at a time.
	c.requestMu.Lock()
	defer c.requestMu.Unlock()

	responseCh := make(chan incomingPacket, 1)
	c.pendingMu.Lock()
	c.pendingRequest = &pendingRequest{expected: packetType, response: responseCh}
	c.pendingMu.Unlock()

	c.socketMu.Lock()
	err := c.conn.SendPacket(controller_id, uint32(packetType), payload)
	c.socketMu.Unlock()
	if err != nil {
		// Do not leave a stale waiter behind when the request could not be sent.
		c.pendingMu.Lock()
		c.pendingRequest = nil
		c.pendingMu.Unlock()
		return nil, nil, err
	}

	select {
	case <-c.done:
		return nil, nil, fmt.Errorf("client closed")
	case packet := <-responseCh:
		return packet.header, packet.payload, nil
	case <-time.After(30 * time.Second):
		// Release the pending slot so later requests cannot use this timed-out waiter.
		c.pendingMu.Lock()
		if c.pendingRequest != nil && c.pendingRequest.response == responseCh {
			c.pendingRequest = nil
		}
		c.pendingMu.Unlock()
		return nil, nil, fmt.Errorf("timed out waiting for response to packet %d", packetType)
	}
}

// SegmentsCollection is a collection of segments for a specific controller zone.
type SegmentsCollection struct {
	ZoneIdx  int           `json:"zone_idx"`
	NumLeds  uint32        `json:"num_leds"`
	Segments []SegmentItem `json:"segments"`
}

// SegmentItem associates a segment definition with its collection index.
type SegmentItem struct {
	SegmentIdx int32   `json:"segment_idx"`
	Segment    Segment `json:"segment"`
}

// AddItem appends a segment when its length fits in the remaining zone LEDs.
// Its start index is assigned after the previously added segment.
func (s *SegmentsCollection) AddItem(segmentName string, segmentLen int32) error {

	if segmentLen <= 0 {
		return fmt.Errorf("segment length must be greater than zero")
	}

	// allocate a new segment index based on the current number of segments
	segmentIdx := int32(len(s.Segments))

	// compute total leds already assigned in the collection
	totalLeds := uint64(0)
	for _, item := range s.Segments {
		totalLeds += uint64(item.Segment.NumLeds)
	}

	//create a new segment object dynamically
	segment := Segment{
		Name:        segmentName,
		SegmentType: 1, // default type
		NumLeds:     uint32(segmentLen),
		StartIdx:    uint32(totalLeds),
	}

	// create a new slice for segments if it is nil
	if s.Segments == nil {
		s.Segments = []SegmentItem{}
	}

	// check if total number of leds in the collection exceeds the number of leds in the zone
	if totalLeds+uint64(segment.NumLeds) > uint64(s.NumLeds) {
		return fmt.Errorf("cannot add segment: total number of LEDs exceeds zone limit")
	}
	s.Segments = append(s.Segments, SegmentItem{
		SegmentIdx: segmentIdx,
		Segment:    segment,
	})
	// ensure start indices are contiguous and correct
	s.RecalculateStartIndices()
	return nil
}

// UpdateItem changes a segment's length if the resulting collection still fits
// in the zone, then recalculates the start indices of following segments.
func (s *SegmentsCollection) UpdateItem(segmentIdx int32, SegmentLen int32) error {
	// Validate the segment index
	if segmentIdx < 0 || int(segmentIdx) >= len(s.Segments) {
		return fmt.Errorf("invalid segment index: %d", segmentIdx)
	}

	// Validate the new segment length
	if SegmentLen <= 0 {
		return fmt.Errorf("segment length must be greater than zero")
	}

	updatedTotal := uint64(SegmentLen)
	for index, item := range s.Segments {
		if index != int(segmentIdx) {
			updatedTotal += uint64(item.Segment.NumLeds)
		}
	}
	if updatedTotal > uint64(s.NumLeds) {
		return fmt.Errorf("cannot update segment: total number of LEDs exceeds zone limit")
	}

	// Update the segment length
	s.Segments[segmentIdx].Segment.NumLeds = uint32(SegmentLen)

	// Recalculate the start index for segmments ensuring they are contiguous
	s.RecalculateStartIndices()

	return nil
}

// RemoveItem deletes a segment and compacts the remaining indices and start
// positions.
func (s *SegmentsCollection) RemoveItem(segmentIdx int32) error {
	for i, item := range s.Segments {
		if item.SegmentIdx == segmentIdx {
			s.Segments = append(s.Segments[:i], s.Segments[i+1:]...)
			for index := range s.Segments {
				s.Segments[index].SegmentIdx = int32(index)
			}
			s.RecalculateStartIndices()
			return nil
		}
	}

	return fmt.Errorf("segment not found: %d", segmentIdx)
}

// RecalculateStartIndices sets each segment's start index to the total length
// of all preceding segments.
func (s *SegmentsCollection) RecalculateStartIndices() {
	for i := 0; i < len(s.Segments); i++ {
		if i == 0 {
			s.Segments[i].Segment.StartIdx = 0
		} else {
			s.Segments[i].Segment.StartIdx = s.Segments[i-1].Segment.StartIdx + s.Segments[i-1].Segment.NumLeds
		}
	}
}

// decodeModeFlags decodes mode flags from a modeFlags value into a Flags struct
func decodeModeFlags(flagsValue modeFlags) Flags {
	return Flags{flags: flagsValue}
}

// lookup a zone by name in the controller and return a pointer to it, or an error if not found
func (c *RGBController) zoneByName(name string) (*Zone, error) {
	for i := range c.Zones {
		if c.Zones[i].Name == name {
			return &c.Zones[i], nil
		}
	}
	return nil, fmt.Errorf("invalid zone: %s", name)
}

// lookup a mode by name in the controller and return a pointer to it, its index, or an error if not found
func (c *RGBController) modeByName(name string) (*Mode, int, error) {
	for i := range c.Modes {
		if c.Modes[i].Name == name {
			return &c.Modes[i], i, nil
		}
	}
	return nil, -1, fmt.Errorf("invalid mode: %s", name)
}

// lookup a LED by name in the controller and return its index, or an error if not found
func (c *RGBController) ledIndexByName(name string) (int, error) {
	for i := range c.LEDs {
		if c.LEDs[i].Name == name {
			return c.LEDs[i].Value, nil
		}
	}
	return -1, fmt.Errorf("invalid led: %s", name)
}

// cloneSlice returns an independent copy while preserving nil slices.
func cloneSlice[T any](src []T) []T {
	if src == nil {
		return nil
	}

	dst := make([]T, len(src))
	copy(dst, src)
	return dst
}
