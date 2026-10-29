// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import "fmt"

// Plugin describes an installed OpenRGB plugin.
type Plugin struct {
	Name        string `json:"name"`
	ID          int    `json:"id"`
	Description string `json:"description"`
	Protocol    int    `json:"protocol"`
	Version     string `json:"version"`
}

// Effect describes an effect exposed by the OpenRGB Effects Plugin.
type Effect struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ID          int    `json:"id"`
	Enabled     bool   `json:"enabled"`
}

// GetPluginList returns installed plugins and refreshes the client's plugin cache.
// Plugin requests are synchronous because the protocol requires a matching response.

// GetPluginList retrieves the list of plugins from the OpenRGB server and caches them in the client.
func (c *Client) GetPluginList() ([]Plugin, error) {
	if err := c.requireProtocol(4, "GetPluginList"); err != nil {
		return nil, err
	}
	// send request for plugin list
	_, payload, err := c.requestPacket(0, PKT_REQUEST_PLUGIN_LIST, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get plugin list: %w", err)
	}

	//decode the plugin list from the payload
	parser, err := lengthPrefixedDecoder(payload, 0, "plugin list")
	if err != nil {
		return nil, err
	}
	pluginCount := parser.uint16()
	plugins := make([]Plugin, pluginCount)
	for i := range pluginCount {
		plugin := Plugin{}
		plugin.Name = parser.varStr()
		plugin.Description = parser.varStr()
		plugin.Version = parser.varStr()
		plugin.ID = int(parser.uint32())
		plugin.Protocol = int(parser.uint32())
		plugins[i] = plugin
	}
	if err := parser.Err(); err != nil {
		return nil, fmt.Errorf("invalid plugin list: %w", err)
	}
	// Cache plugins in client
	c.mu.Lock()
	c.plugins = make(map[string]*Plugin)
	for i := range plugins {
		c.plugins[plugins[i].Name] = &plugins[i]
	}
	c.mu.Unlock()
	return plugins, nil
}

// GetEffectList retrieves the list of effects from the OpenRGB server.
func (c *Client) GetEffectList() ([]Effect, error) {
	if err := c.requireProtocol(4, "GetEffectList"); err != nil {
		return nil, err
	}
	// send request for effect list
	encoder := newPacketEncoder(c.protocol)
	encoder.uint32(uint32(PKT_REQUEST_EFFECT_LIST))
	c.mu.RLock()
	EffectPlugin := c.plugins["OpenRGB Effects Plugin"]
	c.mu.RUnlock()
	if EffectPlugin == nil {
		return nil, fmt.Errorf("effects plugin not found")
	}
	_, payload, err := c.requestPacket(uint32(EffectPlugin.ID), PKT_REQUEST_PLUGIN_SPECIFIC, encoder.getPayload())
	if err != nil {
		return nil, fmt.Errorf("failed to get effect list: %w", err)
	}

	// decode the effect list from the payload
	parser, err := lengthPrefixedDecoder(payload, 4, "effect list")
	if err != nil {
		return nil, err
	}
	effectCount := parser.uint16()
	effects := make([]Effect, effectCount)
	for i := range effectCount {
		effect := Effect{}
		effect.Name = parser.varStr()
		effect.Description = parser.varStr()
		effect.ID = int(i)
		effect.Enabled = intToBool(parser.Byte())
		effects[i] = effect
	}
	if err := parser.Err(); err != nil {
		return nil, fmt.Errorf("invalid effect list: %w", err)
	}
	return effects, nil
}

// SetEffect enables or disables a specific effect on the OpenRGB server.
func (c *Client) SetEffect(effectName string, enabled bool) error {
	if err := c.requireProtocol(4, "SetEffect"); err != nil {
		return err
	}
	payload := newPacketEncoder(c.protocol)
	packetType := PKT_REQUEST_DISABLE_EFFECT
	if enabled {
		packetType = PKT_REQUEST_ENABLE_EFFECT
	}
	payload.uint32(uint32(packetType))
	payload.uint16(uint16(len(effectName) + 1))
	payload.writeStringNullTerminated(effectName)

	c.mu.RLock()
	EffectPlugin := c.plugins["OpenRGB Effects Plugin"]
	c.mu.RUnlock()
	if EffectPlugin == nil {
		return fmt.Errorf("effects plugin not found")
	}
	if err := c.sendPacket(uint32(EffectPlugin.ID), PKT_REQUEST_PLUGIN_SPECIFIC, payload.getPayload()); err != nil {
		return fmt.Errorf("failed to set effect %s: %w", effectName, err)
	}
	return nil
}
