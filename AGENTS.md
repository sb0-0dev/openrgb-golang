# Agent guide

## Project

This repository is `github.com/sb0-0dev/openrgb-golang`, a Go client library for the OpenRGB TCP SDK. The public import path is `github.com/sb0-0dev/openrgb-golang/client`; its Go package name is `openrgb`. The module currently declares Go 1.26.1. The project is pre-release and its public API may change.

The client currently supports OpenRGB SDK protocol versions 0–5. The companion SDK specification `also describes protocol v6 and later behavior; do not assume those features are implemented here. In particular, v6 changes controller addressing to unique IDs and introduces additional manager/callback packets. Check implementation and explicit protocol guards before adding a feature.

## Repository map

- `client/client.go`: Client lifecycle, connection setup, protocol negotiation, controller/device caches, synchronized requests, queued writes, refresh and shutdown behavior.
- `client/transport.go`: TCP packet framing and byte-level transport. SDK packets use the `ORGB` magic, 16-byte header and little-endian integer fields.
- `client/controller.go`: Controller and mode/zone data structures plus parsing of controller payloads.
- `client/device.go`: Device-facing LED, zone, segment and mode operations. Most state-changing operations enqueue writes.
- `client/plugins.go`: plugin/effect requests (protocol 4+).
- `client/types.go`: packet IDs, protocol enums and SDK constants.
- `client/utils.go`: packet encoders/decoders, shared parsing helpers and color/segment utilities.
- `client/helpers.go`: convenience methods layered on Client and Device.
- `documentation/client_reference.md`: method reference and protocol requirements.
- `examples/test_client/main.go`: runnable usage showcase.
- Tests are colocated with implementation in `client/*_test.go`; transport tests use local TCP/pipe test servers rather than requiring physical RGB hardware.

## Important behavior and design constraints

- Prefer the higher-level `Client` and `Device` methods for application behavior. Keep packet details in transport/serialization and operation code.
- `NewClient` establishes the connection, negotiates the SDK protocol version and primes controller state. The default SDK port is 6742.
- Refresh/discovery operations are synchronous requests. Many commands that mutate server state have no command-specific reply, so a refresh reads the server's reported state afterward; it is not an acknowledgement for a particular write.
- Queued writes are asynchronous and throttled to roughly one every 17 ms. `FlushWrites` waits for queued writes and any automatic refresh work. `Close` is the preferred shutdown path and drains work before closing.
- `Client.AutoRefresh` defaults to true. Respect its coalescing behavior when changing write/refresh scheduling.
- Shared connection writes and request/response transactions are serialized. Preserve synchronization invariants around `socketMu`, `requestMu`, queue lifecycle and cache locks.
- Packet parsing must handle truncated and malformed payloads with errors; avoid unchecked indexing and unbounded allocations from server-provided lengths. The transport currently caps payloads at 64 MiB.
- Check protocol-version requirements before sending packets unavailable in earlier SDK versions. Keep the quick reference and README feature claims aligned with guards and implementation.
- OpenRGB modes can require switching to custom mode before direct LED color writes. Device/zone/LED indexes and mode capabilities come from server-reported controller data.

## Development workflow

From this repository root:

- Format: `gofmt -w client`
- Tests: `go test ./...`
- Static checks: `go vet ./...`

The contribution guide asks for tests alongside behavior changes and GoDoc for public API. Tests are hermetic where practical and commonly use fake SDK servers; follow the fixtures in `client/test_helpers_test.go` and `client/transport_test.go`. Do not require an actual OpenRGB server or hardware in unit tests.

When implementing protocol fields, verify the exact field widths, ordering, null-termination/length conventions, and minimum protocol version in the SDK specification and the existing encoder/decoder helpers. Add or update serialization tests, version-guard tests, and user-facing reference documentation as applicable.

## Licensing

The project is GPL-2.0-or-later. Preserve SPDX headers on Go source files and do not introduce dependencies or copied code without checking compatibility.
