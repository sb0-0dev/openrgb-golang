// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"encoding/binary"
	"testing"
)

// TestEffectPluginRequests verifies that effect plugin requests are correctly serialized and handled by the server.
func TestEffectPluginRequests(t *testing.T) {
	client, server := newProtocolTestClient(t, 5)
	client.plugins["OpenRGB Effects Plugin"] = &Plugin{ID: 23}
	serverDone := make(chan error, 1)
	go func() {
		// Effect operations are nested in plugin-specific packets; the plugin ID
		// belongs in the packet header and the effect operation in its payload.
		requestPayload := make([]byte, 4)
		binary.LittleEndian.PutUint32(requestPayload, uint32(PKT_REQUEST_EFFECT_LIST))
		if err := checkProtocolTestPacket(server, 23, PKT_REQUEST_PLUGIN_SPECIFIC, requestPayload); err != nil {
			serverDone <- err
			return
		}
		response := newPacketEncoder(0)
		response.uint32(0)
		response.uint32(0)
		response.uint16(1)
		appendProtocolTestString(response, "Wave")
		appendProtocolTestString(response, "Traveling colors")
		response.writeBytes([]byte{1})
		payload := response.getPayload()
		// Plugin-specific replies prefix their plugin data with a four-byte length.
		binary.LittleEndian.PutUint32(payload[4:8], uint32(len(payload)-4))
		if err := sendProtocolTestPacket(server, 23, PKT_REQUEST_PLUGIN_SPECIFIC, payload); err != nil {
			serverDone <- err
			return
		}
		for _, request := range []struct {
			packetType PacketType
			name       string
		}{
			{PKT_REQUEST_ENABLE_EFFECT, "Wave"},
			{PKT_REQUEST_DISABLE_EFFECT, "Wave"},
		} {
			name := append([]byte(request.name), 0)
			wantPayload := binary.LittleEndian.AppendUint32(nil, uint32(request.packetType))
			wantPayload = binary.LittleEndian.AppendUint16(wantPayload, uint16(len(name)))
			wantPayload = append(wantPayload, name...)
			if err := checkProtocolTestPacket(server, 23, PKT_REQUEST_PLUGIN_SPECIFIC, wantPayload); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()

	effects, err := client.GetEffectList()
	if err != nil || len(effects) != 1 || effects[0] != (Effect{Name: "Wave", Description: "Traveling colors", ID: 0, Enabled: true}) {
		t.Fatalf("GetEffectList() = %+v, %v", effects, err)
	}
	if err := client.SetEffect("Wave", true); err != nil {
		t.Fatalf("SetEffect(enable) error: %v", err)
	}
	if err := client.SetEffect("Wave", false); err != nil {
		t.Fatalf("SetEffect(disable) error: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}

// TestPluginMethodsRequireProtocol verifies that plugin methods enforce the minimum protocol version requirement.
func TestPluginMethodsRequireProtocol(t *testing.T) {
	client := &Client{protocol: 3}
	if _, err := client.GetEffectList(); err == nil {
		t.Error("GetEffectList() succeeded below protocol 4")
	}
	if err := client.SetEffect("Wave", true); err == nil {
		t.Error("SetEffect() succeeded below protocol 4")
	}
}
