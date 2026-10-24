// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"fmt"
)

// Convenience methods for common operations. These methods are not required to
// use the library, but they make common tasks easier without requiring callers
// to manage device objects directly.

// DiscoverDevices refreshes controller state, creates Device objects for every
// discovered controller, and returns them indexed by device name.
func (c *Client) DiscoverDevices() (map[string]*Device, error) {
	if err := c.RefreshAllControllers(); err != nil {
		return nil, err
	}
	if err := c.AddAllDevices(); err != nil {
		return nil, err
	}
	return c.ListDevices(), nil
}

// SetColor sets every LED on the named device to one color.
// The write remains asynchronous; call FlushWrites when completion is required.
func (c *Client) SetColor(deviceName string, color RGBColor) error {
	device, err := c.GetDevice(deviceName)
	if err != nil {
		return err
	}
	return device.SetColor(color)
}

// SetMode sets the lighting mode on the named device. The resulting write is
// asynchronous; call FlushWrites when completion or write errors matter.
func (c *Client) SetMode(deviceName string, modeName string) error {
	device, err := c.GetDevice(deviceName)
	if err != nil {
		return err
	}
	return device.SetMode(modeName)
}

// SetBrightness sets the brightness of the named device's current mode. The
// resulting write is asynchronous; call FlushWrites when completion or errors matter.
func (c *Client) SetBrightness(deviceName string, brightness int) error {
	device, err := c.GetDevice(deviceName)
	if err != nil {
		return err
	}
	return device.SetBrightness(brightness)
}

// ResizeZone changes the LED count for a named zone on a named device.
func (c *Client) ResizeZone(deviceName string, zoneName string, size uint32) error {
	device, err := c.GetDevice(deviceName)
	if err != nil {
		return err
	}
	return device.ResizeZone(zoneName, size)
}

// SetColor queues the same color for every LED on the device.
func (d *Device) SetColor(color RGBColor) error {
	return d.UpdateLeds(color)
}

// SetLEDColor queues a color update for one LED by its server-provided name.
func (d *Device) SetLEDColor(ledName string, color RGBColor) error {
	return d.UpdateLed(ledName, color)
}

// SetZoneColor queues the same color for every LED in a named zone.
func (d *Device) SetZoneColor(zoneName string, color RGBColor) error {
	return d.UpdateZoneLeds(color, zoneName)
}

// functions to provide human-readable names for enums

// String returns a human-readable name for the device type.
func (t DeviceType) String() string {
	names := [...]string{"Motherboard", "DRAM", "GPU", "Cooler", "LED Strip", "Keyboard", "Mouse", "Mousemat", "Headset", "Headset Stand", "Gamepad", "Light", "Speaker", "Virtual", "Storage", "PC Case", "Microphone", "Accessory", "Keypad", "Laptop", "Monitor", "Unknown"}
	if int(t) >= 0 && int(t) < len(names) {
		return names[t]
	}
	return fmt.Sprintf("Unknown(%d)", t)
}

// String returns a human-readable name for the direction.
func (d Direction) String() string {
	names := [...]string{"Left", "Right", "Up", "Down", "Horizontal", "Vertical"}
	if int(d) >= 0 && int(d) < len(names) {
		return names[d]
	}
	return fmt.Sprintf("Unknown(%d)", d)
}

// String returns a human-readable name for the color mode.
func (m ColorMode) String() string {
	names := [...]string{"None", "PerLED", "ModeSpecific", "Random"}
	if int(m) >= 0 && int(m) < len(names) {
		return names[m]
	}
	return fmt.Sprintf("Unknown(%d)", m)
}

// String returns a human-readable name for the speed value.
func (s Speed) String() string {
	names := [...]string{"Slow", "Medium", "Fast"}
	if int(s) >= 0 && int(s) < len(names) {
		return names[s]
	}
	return fmt.Sprintf("Unknown(%d)", s)
}
