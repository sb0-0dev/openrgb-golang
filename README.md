# openrgb-golang

A Golang implementation of an OpenRGB client library to interface with the [OpenRGB SDK](https://gitlab.com/CalcProgrammer1/OpenRGB#openrgb-sdk).

This client aims to make RGB device control simple, efficient, and reliable.The concurrent architecture separates socket communication, packet dispatching, and background writes, while graceful shutdown and configurable refresh behavior give developers control over the client lifecycle.

## Requirements

- Go 1.26.1 or newer.
- OpenRGB must be running with its SDK server enabled.
- The SDK server must be reachable at the host and port passed to `NewClient`; the example below uses `127.0.0.1:6742`.
- This client supports OpenRGB SDK protocol versions up to 5. Protocol version 6 will be supported in a future release.

## Install

Add the client package to your Go module:

```sh
go get github.com/sb0-0dev/openrgb-golang/client
```

This library is pre-release, so its API may change.

## Usage

The library exposes a protocol client for OpenRGB servers.

### Example

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
    client, err := openrgb.NewClient("127.0.0.1", "6742")
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

Most state-changing commands in the supported protocol versions have no command-specific response. If you need to inspect the server-reported state afterward, refresh it with `Client.RefreshController` or `Device.RefreshDevice`, then read it with `GetController` or the relevant device getter. `AutoRefresh` performs this refresh for supported queued writes by default, and `FlushWrites` waits for queued writes and their automatic refreshes. A refresh shows the latest reported state; it is not tied to a particular command.

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
- Queued writes are sent at most once every 17 ms (about 60 per second), so bursts are spread out to protect the server and RGB hardware. Only queued writes are throttled; synchronous requests such as refreshes and rescans are not.
- `Client.AutoRefresh` defaults to `true` and coalesces rapid per-controller refreshes to avoid noisy update storms.

## Documentation

See the [Client API Quick Reference](documentation/client_reference.md) for method signatures, protocol-version requirements, and additional examples.

A quick start example can also be found [here](examples/test_client/main.go).

A "Read the Docs" page will be provided in the near future.

## Troubleshooting

- If `NewClient` cannot connect, confirm OpenRGB is running, the SDK server is enabled, and the host and port are correct. For remote connections, check that the network permits the SDK port.
- If a device lookup fails, use `DiscoverDevices` and pass the exact device name returned by OpenRGB.
- If direct color updates do not take effect, switch the device to custom mode with `SetCustomMode` first.

## Status

🚧 Work in progress

## Copyright

Copyright © 2026 sb0-0dev

Licensed under GPL-2.0-or-later. See [LICENSE](LICENSE).

## Warranty

This software is provided "as is", without warranty of any kind, express or implied, including but not limited to the warranties of merchantability, fitness for a particular purpose and noninfringement. In no event shall the authors or copyright holders be liable for any claim, damages or other liability, whether in an action of contract, tort or otherwise, arising from, out of or in connection with the software or the use or other dealings in the software.

## Acknowledgements

This project depends on communication with OpenRGB, an open-source RGB lighting control application.
Thanks to Adam Honse and the OpenRGB development team for developing and maintaining the OpenRGB project.