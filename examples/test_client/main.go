package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	openrgb "github.com/sb0-0dev/openrgb-golang/client"
)

var (
	blue    = openrgb.RGBColor{R: 64, G: 96, B: 255}
	magenta = openrgb.RGBColor{R: 220, G: 40, B: 255}
)

func main() {
	// SDK: NewClient connects to the OpenRGB SDK server and loads controller
	// metadata, modes, zones, and LEDs for the session.
	rgb, err := openrgb.NewClient("localhost", "6742", "Go SDK showcase")
	if err != nil {
		log.Fatalf("connect to OpenRGB: %v", err)
	}
	defer func() {
		if err := rgb.Close(); err != nil {
			log.Printf("close OpenRGB client: %v", err)
		}
	}()

	// SDK: device type constants and GetDevicesByType let this example work
	// without hard-coding vendor or device names. The library also supports discovering devices
	// by name, but that is not shown here to keep the example general.
	showControllers(rgb)

	useDeviceType(rgb, openrgb.KEYBOARD, demoKeyboard)
	useDeviceType(rgb, openrgb.MOUSE, demoMouse)
	useDeviceType(rgb, openrgb.LED_STRIP, demoLedStrip)
	useDeviceType(rgb, openrgb.COOLER, demoCooler)

	// SDK: Device writes are queued for efficient transport; FlushWrites waits
	// for the server to process them and exposes asynchronous write errors.
	if err := rgb.FlushWrites(); err != nil {
		log.Fatalf("send queued updates: %v", err)
	}
	fmt.Println("RGB showcase updates sent")

	// Leave the initial device showcase visible for five seconds before
	// starting the direct keyboard animation.
	fmt.Println("keyboard animation starts in 5 seconds; press Ctrl+C to stop")
	time.Sleep(5 * time.Second)
	if err := animateKeyboards(rgb); err != nil {
		log.Fatalf("animate keyboards: %v", err)
	}
}

// example helper: showControllers prints a summary of every controller discovered
func showControllers(rgb *openrgb.Client) {
	controllers := rgb.ListControllers()
	for name, controller := range controllers {
		fmt.Printf("found %-24s type=%d leds=%d zones=%d\n",
			name, controller.Type, len(controller.LEDs), len(controller.Zones))
	}
}

// useDeviceType applies an operation to every controller of one device type.
// Using GetDevicesByType also makes the example handle multiple devices of the same type without additinal configuration.
func useDeviceType(rgb *openrgb.Client, deviceType openrgb.DeviceType, operation func(*openrgb.Client, *openrgb.Device) error) {
	devices, err := rgb.GetDevicesByType(deviceType)
	if err != nil {
		// A machine may not have every kind of hardware. That is normal for a
		// general example, so skip unavailable device types gracefully.
		fmt.Printf("skip device type %d: %v\n", deviceType, err)
		return
	}

	for _, device := range devices {
		if err := operation(rgb, device); err != nil {
			log.Printf("update device %d (type %d): %v", device.ControllerID, deviceType, err)
		}
	}
}

func demoKeyboard(_ *openrgb.Client, device *openrgb.Device) error {
	// SDK: SetCustomMode switches the keyboard to direct LED control.
	if err := device.SetCustomMode(); err != nil {
		return err
	}

	// SDK: GetLEDs discovers the controller's addressable keys, and
	// UpdateLeds initializes every key before the animation starts.
	if err := device.UpdateLeds(openrgb.RGBColor{R: 24, G: 96, B: 255}); err != nil {
		return err
	}
	leds, err := device.GetLEDs()
	if err != nil {
		return err
	}
	if len(leds) == 0 {
		return fmt.Errorf("keyboard has no addressable LEDs")
	}

	// SDK: UpdateLedWithOptions can target keys by their device-reported names instead of
	// relying on their positions.Other layouts may name keys differently.
	white := openrgb.RGBColor{R: 255, G: 255, B: 255}
	for _, keyName := range []string{
		"Key: Escape",
		// H
		"Key: Z", "Key: S", "Key: W", "Key: R", "Key: F", "Key: C", "Key: D",
		// I
		"Key: U", "Key: I", "Key: O", "Key: K", "Key: N", "Key: M", "Key: ,",
	} {
		if err := updateKeyboardLedByName(device, leds, keyName, white); err != nil {
			return err
		}
	}
	return nil
}

func updateKeyboardLedByName(device *openrgb.Device, leds []openrgb.LED, targetName string, color openrgb.RGBColor) error {
	for _, led := range leds {
		if strings.EqualFold(led.Name, targetName) {
			return device.UpdateLedWithOptions(led.Name, color)
		}
	}

	// Keyboard layouts vary, so an absent named key is not a device error.
	fmt.Printf("skip named keyboard LED %q: key not found\n", targetName)
	return nil
}

func animateKeyboards(rgb *openrgb.Client) error {
	devices, err := rgb.GetDevicesByType(openrgb.KEYBOARD)
	if err != nil {
		return err
	}

	keyboards := make([]struct {
		device *openrgb.Device
		leds   []openrgb.LED
	}, 0, len(devices))
	for _, device := range devices {
		if err := device.SetCustomMode(); err != nil {
			return fmt.Errorf("set keyboard %d to custom mode: %w", device.ControllerID, err)
		}
		leds, err := device.GetLEDs()
		if err != nil {
			return err
		}
		if len(leds) == 0 {
			return fmt.Errorf("keyboard %d has no addressable LEDs", device.ControllerID)
		}
		keyboards = append(keyboards, struct {
			device *openrgb.Device
			leds   []openrgb.LED
		}{device: device, leds: leds})
	}

	// Animation sends one full-frame packet per keyboard. Skip refreshes for
	// each frame; the next frame supplies the desired LED state anyway.
	fastWrite := openrgb.QueueWriteOptions{SkipRefresh: true}
	colors := []openrgb.RGBColor{
		{R: 255, G: 32, B: 96},
		{R: 255, G: 160, B: 32},
		{R: 224, G: 255, B: 32},
		{R: 32, G: 255, B: 128},
		{R: 32, G: 192, B: 255},
		{R: 160, G: 64, B: 255},
	}

	for frame := 0; ; frame++ {
		for _, keyboard := range keyboards {
			frameColors := make([]openrgb.RGBColor, len(keyboard.leds))
			for ledIndex := range keyboard.leds {
				frameColors[ledIndex] = colors[(ledIndex+frame)%len(colors)]
			}
			if err := keyboard.device.UpdateLedsWithColors(frameColors, fastWrite); err != nil {
				return fmt.Errorf("update keyboard %d: %w", keyboard.device.ControllerID, err)
			}
		}
		if err := rgb.FlushWrites(); err != nil {
			return err
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func demoMouse(_ *openrgb.Client, device *openrgb.Device) error {
	// SDK: SetCustomMode enables direct control, then UpdateLeds applies red
	// to every mouse LED in one operation.
	if err := device.SetCustomMode(); err != nil {
		return err
	}
	return device.UpdateLeds(openrgb.RGBColor{R: 255, G: 0, B: 0})
}

func setRainbow(rgb *openrgb.Client, device *openrgb.Device, deviceName string) error {
	// SDK: GetModes exposes the device's supported modes so the example
	// can use the exact Rainbow mode name reported by each controller.
	modes, err := device.GetModes()
	if err != nil {
		return err
	}

	modeNames := make([]string, 0, len(modes))
	for _, mode := range modes {
		modeNames = append(modeNames, mode.Name)
		// Select only the mode explicitly named "Rainbow". This intentionally
		// does not match modes such as "Rainbow Wave" or "Rainbow Flow".
		if strings.EqualFold(mode.Name, "Rainbow") {
			// SDK: SetMode activates the controller's built-in Rainbow effect.
			return device.SetMode(mode.Name)
		}
	}
	return fmt.Errorf("%s does not provide a Rainbow mode (available: %s)",
		deviceName, strings.Join(modeNames, ", "))
}

func demoLedStrip(rgb *openrgb.Client, device *openrgb.Device) error {
	// SDK: GetZones discovers strip zones, and ResizeZone changes each
	// zone to 72 LEDs without hard-coding controller-specific zone names.
	zones, err := device.GetZones()
	if err != nil {
		return err
	}
	if len(zones) == 0 {
		return fmt.Errorf("LED strip has no zones")
	}
	for _, zone := range zones {
		if err := device.ResizeZone(zone.Name, 72); err != nil {
			return fmt.Errorf("resize LED strip zone %q: %w", zone.Name, err)
		}
	}

	// SDK: inspect each mode's exported brightness range and select a mode
	// that both supports brightness and accepts the requested value of 50.
	controller, err := rgb.GetController(device.ControllerID)
	if err != nil {
		return err
	}
	brightnessMode := ""
	for _, mode := range controller.Modes {
		if mode.Flags.SupportsBrightness() && mode.BrightnessMin <= 50 && mode.BrightnessMax >= 50 {
			brightnessMode = mode.Name
			fmt.Printf("LED strip mode %q supports brightness %d-%d\n",
				mode.Name, mode.BrightnessMin, mode.BrightnessMax)
			break
		}
	}
	if brightnessMode == "" {
		return fmt.Errorf("LED strip has no mode that supports brightness 50")
	}

	// SDK: SetMode activates the brightness-capable mode, and SetBrightness
	// updates that mode. FlushWrites is required before waiting so the server
	// applies brightness 50 before the later Rainbow mode change.
	if err := device.SetMode(brightnessMode); err != nil {
		return err
	}
	if err := device.SetBrightness(50); err != nil {
		return err
	}
	if err := rgb.FlushWrites(); err != nil {
		return err
	}

	fmt.Println("LED strip brightness set to 50; switching to Rainbow in 5 seconds")
	time.Sleep(5 * time.Second)

	// Finally, let the resized strip run its built-in Rainbow effect.
	if err := setRainbow(rgb, device, "LED strip"); err != nil {
		return err
	}
	return nil
}

func demoCooler(_ *openrgb.Client, device *openrgb.Device) error {
	// SDK: SetCustomMode enables direct control, UpdateLeds applies one color
	// to every cooler LED, and SetColors changes the active mode's palette.
	if err := device.SetCustomMode(); err != nil {
		return err
	}
	if err := device.UpdateLeds(blue); err != nil {
		return err
	}
	return device.SetColors([]openrgb.RGBColor{blue, magenta}, openrgb.MODE_SPECIFIC)
}
