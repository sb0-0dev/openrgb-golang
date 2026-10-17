package openrgb

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

// SetMode sets the lighting mode on the named device.
func (c *Client) SetMode(deviceName string, modeName string) error {
	device, err := c.GetDevice(deviceName)
	if err != nil {
		return err
	}
	return device.SetMode(modeName)
}

// SetBrightness sets the brightness of the named device's current mode.
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

// SetColor sets every LED on the device to one color.
func (d *Device) SetColor(color RGBColor) error {
	return d.UpdateLeds(color)
}

// SetLEDColor sets one LED by its server-provided name.
func (d *Device) SetLEDColor(ledName string, color RGBColor) error {
	return d.UpdateLed(ledName, color)
}

// SetZoneColor sets every LED in a named zone to one color.
func (d *Device) SetZoneColor(zoneName string, color RGBColor) error {
	return d.UpdateZoneLeds(color, zoneName)
}
