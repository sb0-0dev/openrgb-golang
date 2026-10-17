# OpenRGB-Go

A Golang implementation of an OpenRGB client library to interface with the [OpenRGB SDK](https://gitlab.com/CalcProgrammer1/OpenRGB#openrgb-sdk).

## Requirements

- Go 1.26.1 or newer.
- OpenRGB must be running with its SDK server enabled.
- The SDK server must be reachable at the host and port passed to `NewClient`; the example below uses `127.0.0.1:6742`.

## Install

Add the client package to your Go module:

```sh
go get github.com/sb0-0dev/openrgb-go/client
```

This library is pre-release, so its API may change.

## Usage

The library exposes a protocol client for OpenRGB servers.

### Example

```go
package main

import (
    "fmt"
    "log"

    openrgb "github.com/sb0-0dev/openrgb-go/client"
)

func main() {
    if err := run(); err != nil {
        log.Fatal(err)
    }
}

func run() error {
    client, err := openrgb.NewClient("127.0.0.1", "6742")
    if err != nil {
        return err
    }
    defer client.Close()

    devices, err := client.DiscoverDevices()
    if err != nil {
        return err
    }

    for name := range devices {
        log.Printf("Found OpenRGB device: %s", name)
    }

    deviceName := "Keyboard" // Replace with a name printed above.
    device, ok := devices[deviceName]
    if !ok {
        return fmt.Errorf("device %q not found", deviceName)
    }
    if err := device.SetCustomMode(); err != nil {
        return err
    }
    if err := device.SetColor(openrgb.RGBColor{R: 255, G: 0, B: 64}); err != nil {
        return err
    }
    return client.FlushWrites()
}
```

`DiscoverDevices` refreshes the controller list and returns devices keyed by their OpenRGB names. Direct LED updates require custom mode; `SetCustomMode` enables that control before setting the color.

The library follows the OpenRGB SDK protocol closely. Most application code should use the higher-level `Client` and `Device` APIs instead of sending raw packets directly.

### Supported features

- Discover controllers and look up devices by name or device type.
- Read controller, LED, zone, segment, and lighting-mode information.
- Update colors for a whole device, individual LEDs, zones, or segments.
- Set lighting modes and their supported speed, brightness, direction, and color options.
- Resize zones and manage LED segments; segment operations require protocol version 5.
- List, load, save, and delete profiles; profile operations require protocol version 2 or newer.
- List plugins and effects, and enable or disable effects; effect operations require protocol version 4 or newer.
- Queue asynchronous writes, automatically refresh controller state, and flush or gracefully close the client.

### Lifecycle and concurrency

- `Close()` is the preferred shutdown path; it waits for queued writes and refreshes to complete and then closes the TCP connection.
- `FlushWrites()` waits for queued writes and auto-refreshes without closing the client.
- `QueueWrite` is intentionally non-blocking at the enqueue step; if the queue is full it returns an error instead of blocking the caller.
- `Client.AutoRefresh` defaults to `true` and coalesces rapid per-controller refreshes to avoid noisy update storms.

## Documentation

See the [Client API Quick Reference](documentation/client_reference.md) for method signatures, protocol-version requirements, and additional examples.

A more detailed quick start example can also be found [here] examples\test_client\main.go. 


A "Read the Docs" page will be provided in the near future.

## Troubleshooting

- If `NewClient` cannot connect, confirm OpenRGB is running, the SDK server is enabled, and the host and port are correct. For remote connections, check that the network permits the SDK port.
- If a device lookup fails, use `DiscoverDevices` and pass the exact device name returned by OpenRGB.
- If direct color updates do not take effect, switch the device to custom mode with `SetCustomMode` first.

## Status

🚧 Work in progress — not yet released.

## Copyright

Copyright © 2026 sb0-0dev
All rights reserved.

## Acknowledgements

This project depends on communication with OpenRGB, an open-source RGB lighting control application.
Thanks to Adam Honse and the OpenRGB development team for developing and maintaining the OpenRGB project.