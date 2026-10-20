// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func newQueueOnlyClient() *Client {
	return &Client{
		rgbControllers: make(map[string]*RGBController),
		devices:        make(map[string]*Device),
		queue:          make(chan writeRequest, 16),
		done:           make(chan struct{}),
		protocol:       5,
	}
}

func newTestDeviceWithController(t *testing.T) (*Client, *Device, net.Conn) {
	t.Helper()
	client, server := newTestClient(t, 5)
	device := addTestDeviceWithController(client)
	return client, device, server
}

func newQueueOnlyTestDeviceWithController() (*Client, *Device) {
	client := newQueueOnlyClient()
	return client, addTestDeviceWithController(client)
}

func addTestDeviceWithController(client *Client) *Device {
	flags := modeFlags(HAS_SPEED | HAS_BRIGHTNESS | HAS_DIR_LR | HAS_MODE_COLOR)
	client.rgbControllers["Demo"] = &RGBController{
		ID:   3,
		Name: "Demo",
		LEDs: []LED{{Name: "LED 0", Value: 0}, {Name: "LED 1", Value: 1}, {Name: "LED 2", Value: 2}, {Name: "LED 3", Value: 3}},
		Colors: []RGBColor{
			{R: 1, G: 2, B: 3},
			{R: 4, G: 5, B: 6},
			{R: 7, G: 8, B: 9},
			{R: 10, G: 11, B: 12},
		},
		Zones: []Zone{
			{Name: "Left", zoneIdx: 0, NumLeds: 2, segmentData: []Segment{{Name: "Left Segment", StartIdx: 0, NumLeds: 2}}},
			{Name: "Right", zoneIdx: 1, NumLeds: 2, segmentData: []Segment{{Name: "Right Segment", StartIdx: 1, NumLeds: 1}}},
		},
		Modes: []Mode{
			{Name: "Static", id: 0, value: 1, ColorMode: PER_LED},
			{
				Name: "Pulse", id: 1, value: 7, rawFlags: uint32(flags), Flags: decodeModeFlags(flags),
				SpeedMin: 1, SpeedMax: 3, BrightnessMin: 0, BrightnessMax: 100,
				ColorsMin: 1, ColorsMax: 2, speed: uint32(MEDIUM), brightness: 80,
				direction: uint32(LEFT), ColorMode: PER_LED, numColors: 1,
				Colors: []RGBColor{{R: 20, G: 30, B: 40}},
			},
		},
	}
	return &Device{ControllerID: 3, Mode: "Static", ModeIdx: 0, client: client}
}

func newProtocolTestClient(t *testing.T, protocol int) (*Client, net.Conn) {
	t.Helper()
	server, clientConn := net.Pipe()
	client := &Client{
		conn:            &connection{conn: clientConn},
		protocol:        protocol,
		done:            make(chan struct{}),
		incomingPackets: make(chan incomingPacket, 8),
		serverUpdates:   make(chan *PacketHeader, 8),
		rgbControllers:  make(map[string]*RGBController),
		devices:         make(map[string]*Device),
		plugins:         make(map[string]*Plugin),
	}
	go client.readLoop()
	go client.packetDispatcher()
	t.Cleanup(func() {
		close(client.done)
		clientConn.Close()
		server.Close()
	})
	return client, server
}

// checkProtocolTestPacket verifies the exact request written to the fake server,
// including its controller address and raw payload.
func checkProtocolTestPacket(server net.Conn, controllerID uint32, packetType PacketType, payload []byte) error {
	header, gotPayload, err := (&connection{conn: server}).ReadPacket(time.Second)
	if err != nil {
		return err
	}
	if header.DevIdx != controllerID || PacketType(header.PktID) != packetType || string(gotPayload) != string(payload) {
		return fmt.Errorf("request = (%d, %d, %v), want (%d, %d, %v)", header.DevIdx, header.PktID, gotPayload, controllerID, packetType, payload)
	}
	return nil
}

func sendProtocolTestPacket(server net.Conn, controllerID uint32, packetType PacketType, payload []byte) error {
	return (&connection{conn: server}).SendPacket(controllerID, uint32(packetType), payload)
}

func appendProtocolTestString(encoder *PacketEncoder, value string) {
	encoder.uint16(uint16(len(value) + 1))
	encoder.writeStringNullTerminated(value)
}

// serveControllerRefresh answers the count and controller-data requests used
// when testing a refresh against the client’s normal request/response path.
func serveControllerRefresh(server net.Conn) error {
	if err := checkProtocolTestPacket(server, 0, PKT_REQUEST_CONTROLLER_COUNT, nil); err != nil {
		return err
	}
	count := newPacketEncoder(0)
	count.uint32(1)
	if err := sendProtocolTestPacket(server, 0, PKT_REQUEST_CONTROLLER_COUNT, count.getPayload()); err != nil {
		return err
	}
	if err := checkProtocolTestPacket(server, 0, PKT_REQUEST_CONTROLLER_DATA, []byte{5, 0, 0, 0}); err != nil {
		return err
	}
	return sendProtocolTestPacket(server, 0, PKT_REQUEST_CONTROLLER_DATA, testControllerDataPayload())
}

// newTestClient creates a client connected to an in-memory pipe and starts its
// write worker so tests can inspect packets queued by Device methods.
func newTestClient(t *testing.T, protocol int) (*Client, net.Conn) {
	t.Helper()
	server, clientConn := net.Pipe()
	client := &Client{
		conn:           &connection{conn: clientConn},
		rgbControllers: make(map[string]*RGBController),
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

func testControllerDataPayload() []byte {
	// Keep this minimal but valid for protocol 5 so tests exercise the production decoder.
	encoder := newPacketEncoder(5)
	encoder.uint32(0)
	encoder.uint32(uint32(KEYBOARD))
	writeString := func(value string) {
		encoder.uint16(uint16(len(value) + 1))
		encoder.writeStringNullTerminated(value)
	}
	writeString("Test Keyboard")
	writeString("Test Vendor")
	writeString("Test Description")
	writeString("Test Model")
	writeString("Test Serial")
	writeString("Test Location")
	encoder.uint16(0)
	encoder.uint32(0)
	encoder.uint16(0)
	encoder.uint16(1)
	writeString("Test LED")
	encoder.uint32(0)
	encoder.uint16(1)
	encoder.uint32(ConvertRGBtoUint32(RGBColor{R: 10, G: 20, B: 30}))
	encoder.uint16(0)
	encoder.uint32(0)
	return encoder.getPayload()
}
