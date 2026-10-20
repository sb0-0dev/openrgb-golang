// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"testing"
)

// TestPacketTypeValues verifies that the PacketType constants have the expected numeric values.
func TestPacketTypeValues(t *testing.T) {
	for packetType, want := range map[PacketType]uint32{
		PKT_REQUEST_CONTROLLER_COUNT: 0,
		PKT_REQUEST_PROTOCOL_VERSION: 40,
		PKT_DEVICE_LIST_UPDATED:      100,
		PKT_REQUEST_RESCAN_DEVICES:   140,
		PKT_REQUEST_PROFILE_LIST:     150,
		PKT_REQUEST_PLUGIN_LIST:      200,
		PKT_REQUEST_PLUGIN_SPECIFIC:  201,
		PKT_RGBCONTROLLER_RESIZEZONE: 1000,
		PKT_RGBCONTROLLER_UPDATELEDS: 1050,
	} {
		if got := uint32(packetType); got != want {
			t.Errorf("packet type %d = %d, want %d", packetType, got, want)
		}
	}
}
