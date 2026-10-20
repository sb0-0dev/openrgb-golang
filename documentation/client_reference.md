# Client API Quick Reference

## Introduction

This document summarizes the public API of the `openrgb-golang/client` package. It is intended for developers who want to control OpenRGB-compatible devices from Go applications. The package provides a client for connecting to an OpenRGB SDK server and controlling RGB devices.

Package import:

```go
import "github.com/sb0-0dev/openrgb-golang/client"
```

## Connection and Lifecycle

| Method | Description |
| --- | --- |
| `NewClient(host, port string, args ...interface{}) (*Client, error)` | Connects to an OpenRGB SDK server, negotiates the protocol, and loads the initial controller cache. Optional arguments can include a maximum protocol version (`int`) and client name (`string`). |
| `Connect(host, port string) (*connection, error)` | Opens a TCP connection to the OpenRGB server. The returned connection type is package-internal; most callers should use `NewClient`. |
| `Client.Close() error` | Gracefully closes the client after queued writes and refreshes finish. Returns asynchronous write or socket errors. |
| `Client.Disconnect() error` | Immediately closes the underlying connection without draining queued writes. Prefer `Close` for normal shutdown. |
| `Client.FlushWrites() error` | Waits for currently queued writes and automatic refreshes, then returns any asynchronous errors. The client remains open. |
| `Client.QueueWrite(controllerID uint32, packetType PacketType, payload []byte, opts ...QueueWriteOptions) error` | Queues a raw packet write for advanced callers. The returned error only covers enqueueing; use `FlushWrites` for send and refresh errors. |

- `Client.AutoRefresh` defaults to `true`. Set it to `false` when the application manages controller refreshes explicitly.
- `QueueWrite` returns an enqueue error immediately if the internal write queue is full, instead of blocking the caller. Call `FlushWrites` when you need to wait for queued writes and refreshes to complete and collect asynchronous errors.
- The optional `QueueWriteOptions` argument is supported by `Client.QueueWrite`, `Device.UpdateLedWithOptions`, and `Device.UpdateLedsWithColors`. Set `SkipRefresh: true` to skip the automatic refresh after a write, which is useful for high-frequency updates.

## Controller Discovery and State

| Method | Description |
| --- | --- |
| `Client.GetControllerCount() (int, error)` | Requests the number of controllers currently reported by the server. |
| `Client.GetControllerData(controllerID uint32) (*RGBController, error)` | Requests and parses the current state of one controller. |
| `Client.GetController(controllerID uint32) (*RGBController, error)` | Returns a cached controller by numeric ID. |
| `Client.GetControllerByName(name string) (*RGBController, error)` | Returns a cached controller by name. |
| `Client.ListControllers() map[string]*RGBController` | Returns a map of deep-copied controller snapshots. |
| `Client.RefreshController(controllerID uint32) error` | Refreshes one controller in the cache. |
| `Client.RefreshAllControllers() error` | Refreshes all controllers and replaces the cached controller map. |
| `Client.RescanDevices() error` | Requests server-side device rediscovery, waits for completion, and refreshes all controllers. Requires protocol 5. |
| `Client.DiscoverDevices() (map[string]*Device, error)` | Refreshes controllers, creates devices for all controllers, and returns them by device name. |
| `Client.AddDevice(deviceName string) (*Device, error)` | Creates and caches a device for a controller name. |
| `Client.GetDevice(deviceName string) (*Device, error)` | Returns a cached device or creates it if necessary. |
| `Client.GetDevicesByType(deviceType DeviceType) ([]*Device, error)` | Returns all devices matching a controller type. |
| `Client.GetDeviceByType(deviceType DeviceType) (*Device, error)` | Returns the first device matching a controller type. |
| `Client.AddAllDevices() error` | Creates and caches devices for every controller in the current cache. |
| `Client.ListDevices() map[string]*Device` | Returns a copy of the cached device map. |

### Cache and Mutability

The client does not expose its controller or device maps directly. `ListControllers` returns a new map containing deep copies, and `GetController` and `GetControllerByName` return deep-copied controller snapshots. Adding, removing, or replacing entries in those returned maps, or changing fields and slices on those controller snapshots, does not change the client's cached controller state. Use the refresh methods to update that cache from the server.

`ListDevices` returns a new map, so changing its entries does not add, remove, or replace devices in the client's cache. Its values are pointers to the cached `Device` objects, though; changing an exported field such as `Mode`, `ModeIdx`, or `ModeOptions` through one of those pointers changes that local cached object. Direct field assignment does not send a command to the server. Use the `Device` methods to request device changes; writes are generally queued asynchronously, so call `FlushWrites` when completion or asynchronous errors matter.

Controller data returned by `Device.GetLEDs`, `GetZones`, `GetModes`, and `GetMode` comes from a controller snapshot. Editing those returned values does not update the client's controller cache or the server. `GetSegments` also returns a copy of the selected segment slice.

## Client Convenience Operations

These methods resolve a device by name and delegate to the corresponding `Device` method.

| Method | Description |
| --- | --- |
| `Client.SetColor(deviceName string, color RGBColor) error` | Sets every LED on a named device to one color. |
| `Client.SetMode(deviceName, modeName string) error` | Sets the active mode on a named device. |
| `Client.SetBrightness(deviceName string, brightness int) error` | Sets brightness for the named device's current mode. |
| `Client.ResizeZone(deviceName, zoneName string, size uint32) error` | Changes the LED count for a named zone. |

## Device LED Operations

| Method | Description |
| --- | --- |
| `Device.GetLEDs() ([]LED, error)` | Returns the server-provided LED data. |
| `Device.RefreshDevice() error` | Refreshes this device's controller state through its parent client. |
| `Device.SetCustomMode() error` | Requests custom/direct LED control mode. |
| `Device.UpdateLed(ledName string, color RGBColor) error` | Queues a color update for one named LED. |
| `Device.UpdateLedWithOptions(ledName string, color RGBColor, opts ...QueueWriteOptions) error` | Queues a color update for one named LED with queue options. |
| `Device.UpdateLeds(color RGBColor) error` | Queues one color for every LED on the device. |
| `Device.UpdateLedsWithColors(colors []RGBColor, opts ...QueueWriteOptions) error` | Queues one color per LED. The slice length must match the device's LED count. |
| `Device.UpdateSegmentLeds(zoneIndex, segmentIndex int, color RGBColor) error` | Queues a color update for one segment while preserving colors outside that segment. |
| `Device.SetColor(color RGBColor) error` | Convenience alias for `UpdateLeds`. |
| `Device.SetLEDColor(ledName string, color RGBColor) error` | Convenience alias for `UpdateLed`. |

Use `QueueWriteOptions{SkipRefresh: true}` for high-frequency animations where automatic state refreshes are unnecessary for each frame.

## Modes and Effects

| Method | Description |
| --- | --- |
| `Device.GetModes() ([]Mode, error)` | Returns the server-provided mode data and supported options. |
| `Device.SetMode(modeName string) error` | Changes the active mode. If `Device.ModeOptions.SaveToDevice` is true, also saves it to device memory. |
| `Device.SetSpeed(speed Speed) error` | Sets the current mode's speed when supported. |
| `Device.SetBrightness(brightness int) error` | Sets the current mode's brightness when supported. |
| `Device.SetDirection(direction Direction) error` | Sets the current mode's direction when supported. |
| `Device.SetColors(colors []RGBColor, colorMode ColorMode) error` | Sets the current mode's color palette and color mode. |
| `Client.GetPluginList() ([]Plugin, error)` | Retrieves and caches installed plugins. Requires protocol 4 or newer. |
| `Client.GetEffectList() ([]Effect, error)` | Retrieves effects from the OpenRGB Effects Plugin. Requires protocol 4 or newer and a loaded plugin list. |
| `Client.SetEffect(effectName string, enabled bool) error` | Enables or disables a named effect. Requires protocol 4 or newer and a loaded plugin list. |

`Device.ModeOptions` controls mode updates:

```go
device.ModeOptions = openrgb.ModeOptions{
    Speed:        openrgb.FAST,
    Brightness:   75,
    Direction:    openrgb.RIGHT,
    Colors:       []openrgb.RGBColor{{R: 255, G: 0, B: 0}},
    ColorMode:    openrgb.PER_LED,
    SaveToDevice: false,
}
```

## Zones and Segments

| Method | Description |
| --- | --- |
| `Device.GetZones() ([]Zone, error)` | Returns the server-provided zone data. |
| `Device.GetSegments(zoneIndex int) ([]Segment, error)` | Returns the segments for a zone by index. |
| `Device.UpdateZoneLeds(color RGBColor, zone string) error` | Queues one color for every LED in a named zone. |
| `Device.SetZoneColor(zoneName string, color RGBColor) error` | Convenience alias for `UpdateZoneLeds`. |
| `Device.ResizeZone(zoneName string, newSize uint32) error` | Changes the number of LEDs assigned to a zone. The server must accept the new size. |
| `Device.NewSegment(zoneName string) (*SegmentsCollection, error)` | Creates a segment collection for a zone. Requires protocol 5. |
| `Device.ClearSegments(zoneName string) error` | Removes all segments from a zone. Requires protocol 5. |
| `Device.AddSegment(segments SegmentsCollection) error` | Sends a complete segment layout to the server. Requires protocol 5; the collection LED count must match the zone, and its non-empty segments must cover the zone contiguously from LED 0. Invalid layouts are rejected before any segment packet is queued. |
| `SegmentsCollection.AddItem(segmentName string, segmentLen int32) error` | Adds a segment while enforcing the zone LED capacity. |
| `SegmentsCollection.UpdateItem(segmentIdx int32, segmentLen int32) error` | Changes a segment length if the collection stays within its LED capacity, then recalculates contiguous start indices. |
| `SegmentsCollection.RemoveItem(segmentIdx int32) error` | Removes a segment and recalculates contiguous start indices. |
| `SegmentsCollection.RecalculateStartIndices()` | Recalculates all segment start positions. |

`AddItem`, `UpdateItem`, and `RemoveItem` can be used while constructing or editing a layout. Before calling `AddSegment`, make sure the segments have no gaps or overlaps, start at LED 0, and their total LED count exactly matches both `SegmentsCollection.NumLeds` and the zone's current LED count. `NewSegment` initializes the collection with the zone's current LED count.

Typical segment workflow:

```go
segments, err := device.NewSegment("Zone 1")
if err != nil {
    return err
}
if err := segments.AddItem("Left", 30); err != nil {
    return err
}
if err := segments.AddItem("Right", 30); err != nil {
    return err
}
if err := device.AddSegment(*segments); err != nil {
    return err
}
```

## Profiles

Profile methods are supported only by protocol versions >=2.

| Method | Description |
| --- | --- |
| `Client.GetProfiles() ([]string, error)` | Returns available profile names. |
| `Client.LoadProfile(profileName string) error` | Loads a profile and refreshes all controller state. |
| `Client.SaveProfile(profileName string) error` | Saves the current controller state to a profile. |
| `Client.DeleteProfile(profileName string) error` | Deletes a profile. |

## Public Data Types

| Type | Purpose |
| --- | --- |
| `Client` | Manages the server connection, controller cache, devices, and queued writes. |
| `Device` | Provides LED, zone, mode, and segment operations for one controller. |
| `RGBController` | Cached controller metadata, LEDs, zones, modes, colors, and flags. |
| `RGBColor` | RGB color with `R`, `G`, and `B` channels (`uint8`). |
| `Mode` | Mode metadata, ranges, colors, and capability flags. |
| `ModeOptions` | Values applied by mode updates, including `SaveToDevice`. |
| `Zone` | Zone metadata and LED count. |
| `LED` | LED name and server index. |
| `Segment` | A contiguous LED range within a zone. |
| `SegmentsCollection` | Collection of segments prepared for a zone. |
| `SegmentItem` | Segment plus its collection index. |
| `Plugin` | Installed plugin metadata. |
| `Effect` | Effects Plugin effect metadata. |
| `QueueWriteOptions` | Queue behavior options, currently `SkipRefresh`. |
| `PacketHeader` | Header metadata for an OpenRGB packet. |
| `PacketType` | Identifies an OpenRGB protocol packet when using `QueueWrite`. |
| `DeviceType` | Identifies a controller category for device discovery. |
| `Direction` | Identifies a mode direction. |
| `ColorMode` | Identifies how a mode obtains its colors. |
| `Speed` | Identifies a conventional mode speed preset. |
| `Flags` | Reports which capabilities a mode supports. |

## Enums and Constants

### Device Types

`MOTHERBOARD`, `DRAM`, `GPU`, `COOLER`, `LED_STRIP`, `KEYBOARD`, `MOUSE`, `MOUSEMAT`, `HEADSET`, `HEADSET_STAND`, `GAMEPAD`, `LIGHT`, `SPEAKER`, `VIRTUAL`, `STORAGE`, `PC_CASE`, `MICROPHONE`, `ACCESSORY`, `KEYPAD`, `LAPTOP`, `MONITOR`, `UNKNOWN`.

### Directions

`LEFT`, `RIGHT`, `UP`, `DOWN`, `HORIZONTAL`, `VERTICAL`.

### Color Modes

`NONE`, `PER_LED`, `MODE_SPECIFIC`, `RANDOM`.

### Speed Presets

`SLOW`, `MEDIUM`, `FAST`. These types retain their numeric protocol values and implement `String()` for readable output. Unknown values are formatted as `Unknown(n)`.

### Color Conversion Helpers

| Function | Description |
| --- | --- |
| `ConvertToRGBColor(color uint32) RGBColor` | Converts an OpenRGB packed color value to an RGB color. |
| `ConvertRGBtoUint32(color RGBColor) uint32` | Converts an RGB color to OpenRGB's packed color format. |

## Mode Capability Checks

`Mode` and `Flags` provide the following capability methods:

- `SupportsSpeed()`
- `SupportsBrightness()`
- `SupportsDirectionLR()`
- `SupportsDirectionUD()`
- `SupportsDirectionHV()`
- `SupportsUniqueLEDColor()`
- `SupportsModeColor()`
- `SupportsRandomColor()`

Example:

```go
for _, mode := range controller.Modes {
    if mode.SupportsBrightness() {
        fmt.Println("brightness-capable mode:", mode.Name)
    }
}
```

## Quick Start

Enable the OpenRGB SDK server in OpenRGB. It listens on port `6742` by default. Add the client package to your Go module:

```sh
go get github.com/sb0-0dev/openrgb-golang/client
```

This complete program discovers controllers, prints their names, and sets one controller's LEDs to a color. Replace `My Controller` with a name printed by the program:

```go
package main

import (
    "errors"
    "fmt"
    "log"

    openrgb "github.com/sb0-0dev/openrgb-golang/client"
)

func main() {
    if err := run(); err != nil {
        log.Fatal(err)
    }
}

func run() (err error) {
    client, err := openrgb.NewClient("localhost", "6742")
    if err != nil {
        return err
    }
    defer func() {
        err = errors.Join(err, client.Close())
    }()

    devices, err := client.DiscoverDevices()
    if err != nil {
        return err
    }
    for name := range devices {
        fmt.Println(name)
    }

    device, ok := devices["My Controller"]
    if !ok {
        return fmt.Errorf("controller %q was not found", "My Controller")
    }
    if err := device.SetCustomMode(); err != nil {
        return err
    }
    if err := device.SetColor(openrgb.RGBColor{R: 255, G: 32, B: 96}); err != nil {
        return err
    }
    return client.FlushWrites()
}
```

The client supports protocol versions up to 5. `FlushWrites` waits for queued writes and automatic refreshes and reports local send errors.
