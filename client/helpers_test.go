// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import "testing"

// TestEnumStrings verifies that the String() methods for various enums return the expected human-readable strings.
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

// TestConvenienceDeviceHelpersQueueOperations verifies that the convenience helper methods on the client and device correctly queue the expected packets.
func TestConvenienceDeviceHelpersQueueOperations(t *testing.T) {
	color := RGBColor{R: 10, G: 20, B: 30}
	tests := []struct {
		name       string
		packetType PacketType
		call       func(*Client, *Device) error
	}{
		{"client set color", PKT_RGBCONTROLLER_UPDATELEDS, func(client *Client, _ *Device) error { return client.SetColor("Demo", color) }},
		{"client set mode", PKT_RGBCONTROLLER_UPDATEMODE, func(client *Client, _ *Device) error { return client.SetMode("Demo", "Pulse") }},
		{"client set brightness", PKT_RGBCONTROLLER_UPDATEMODE, func(client *Client, _ *Device) error { return client.SetBrightness("Demo", 50) }},
		{"client resize zone", PKT_RGBCONTROLLER_RESIZEZONE, func(client *Client, _ *Device) error { return client.ResizeZone("Demo", "Right", 5) }},
		{"device set color", PKT_RGBCONTROLLER_UPDATELEDS, func(_ *Client, device *Device) error { return device.SetColor(color) }},
		{"device update LED", PKT_RGBCONTROLLER_UPDATESINGLELED, func(_ *Client, device *Device) error { return device.UpdateLedWithOptions("LED 0", color) }},
		{"device set zone color", PKT_RGBCONTROLLER_UPDATEZONELEDS, func(_ *Client, device *Device) error { return device.SetZoneColor("Right", color) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, device := newQueueOnlyTestDeviceWithController()
			client.devices["Demo"] = device
			if err := test.call(client, device); err != nil {
				t.Fatal(err)
			}
			request := <-client.queue
			if request.controller_id != 3 || request.packetType != test.packetType {
				t.Fatalf("queued request = (%d, %d), want (3, %d)", request.controller_id, request.packetType, test.packetType)
			}
			client.queueWg.Done()
		})
	}
}

// TestDiscoverDevicesRefreshesAndCachesDevices verifies that DiscoverDevices correctly refreshes the controller state from the server and caches the discovered devices.
func TestDiscoverDevicesRefreshesAndCachesDevices(t *testing.T) {
	client, server := newProtocolTestClient(t, 5)
	serverDone := make(chan error, 1)
	go func() { serverDone <- serveControllerRefresh(server) }()

	devices, err := client.DiscoverDevices()
	if err != nil || len(devices) != 1 || devices["Test Keyboard"].ControllerID != 0 {
		t.Fatalf("DiscoverDevices() = %+v, %v", devices, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}
