// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

// PacketType identifies an OpenRGB protocol packet.
type PacketType int

// DeviceType identifies the kind of RGB controller device.
type DeviceType int

// Direction identifies the direction supported by a lighting mode.
type Direction int

// ColorMode identifies how a mode obtains its colors.
type ColorMode int

// Speed identifies a conventional lighting speed value.
type Speed int

// client flags
const (
    NET_CLIENT_FLAG_SUPPORTS_RGBCONTROLLER   uint32 = 1 << 0
    NET_CLIENT_FLAG_SUPPORTS_LOGMANAGER      uint32 = 0 << 1
    NET_CLIENT_FLAG_SUPPORTS_PROFILEMANAGER  uint32 = 1 << 2
    NET_CLIENT_FLAG_SUPPORTS_PLUGINMANAGER   uint32 = 1 << 3
    NET_CLIENT_FLAG_SUPPORTS_SETTINGSMANAGER uint32 = 0 << 4
    NET_CLIENT_FLAG_REQUEST_LOCAL_CLIENT     uint32 = 1 << 16
)

const (
	// Packet types
	PKT_REQUEST_CONTROLLER_COUNT      PacketType = 0
	PKT_REQUEST_CONTROLLER_DATA       PacketType = 1
	PKT_REQUEST_PROTOCOL_VERSION      PacketType = 40
	PKT_SET_CLIENT_NAME               PacketType = 50
	PKT_SET_CLIENT_FLAGS              PacketType = 52
	PKT_RCV_SERVER_FLAGS              PacketType = 53
	PKT_DEVICE_LIST_UPDATED           PacketType = 100
	PKT_REQUEST_RESCAN_DEVICES        PacketType = 140
	PKT_REQUEST_PROFILE_LIST          PacketType = 150
	PKT_REQUEST_SAVE_PROFILE          PacketType = 151
	PKT_REQUEST_LOAD_PROFILE          PacketType = 152
	PKT_REQUEST_DELETE_PROFILE        PacketType = 153
	PKT_REQUEST_PLUGIN_LIST           PacketType = 200
	PKT_REQUEST_PLUGIN_SPECIFIC       PacketType = 201
	PKT_RGBCONTROLLER_RESIZEZONE      PacketType = 1000
	PKT_RGBCONTROLLER_CLEAR_SEGMENTS  PacketType = 1001
	PKT_RGBCONTROLLER_ADD_SEGMENT     PacketType = 1002
	PKT_RGBCONTROLLER_UPDATELEDS      PacketType = 1050
	PKT_RGBCONTROLLER_UPDATEZONELEDS  PacketType = 1051
	PKT_RGBCONTROLLER_UPDATESINGLELED PacketType = 1052
	PKT_RGBCONTROLLER_SETCUSTOMMODE   PacketType = 1110
	PKT_RGBCONTROLLER_UPDATEMODE      PacketType = 1101
	PKT_RGBCONTROLLER_SAVEMODE        PacketType = 1102
	// Effect packet IDs are interpreted inside PKT_REQUEST_PLUGIN_SPECIFIC
	// payloads, so their values intentionally overlap top-level packet IDs.
	PKT_REQUEST_EFFECT_LIST    PacketType = 0
	PKT_REQUEST_ENABLE_EFFECT  PacketType = 20
	PKT_REQUEST_DISABLE_EFFECT PacketType = 21
	// Mode flags
	HAS_SPEED      modeFlags = 1 << 0
	HAS_DIR_LR     modeFlags = 1 << 1
	HAS_DIR_UD     modeFlags = 1 << 2
	HAS_DIR_HV     modeFlags = 1 << 3
	HAS_BRIGHTNESS modeFlags = 1 << 4
	HAS_UNQ_COLOR  modeFlags = 1 << 5
	HAS_MODE_COLOR modeFlags = 1 << 6
	HAS_RAND_COLOR modeFlags = 1 << 7
	// Device types
	MOTHERBOARD   DeviceType = 0
	DRAM          DeviceType = 1
	GPU           DeviceType = 2
	COOLER        DeviceType = 3
	LED_STRIP     DeviceType = 4
	KEYBOARD      DeviceType = 5
	MOUSE         DeviceType = 6
	MOUSEMAT      DeviceType = 7
	HEADSET       DeviceType = 8
	HEADSET_STAND DeviceType = 9
	GAMEPAD       DeviceType = 10
	LIGHT         DeviceType = 11
	SPEAKER       DeviceType = 12
	VIRTUAL       DeviceType = 13
	STORAGE       DeviceType = 14
	PC_CASE       DeviceType = 15
	MICROPHONE    DeviceType = 16
	ACCESSORY     DeviceType = 17
	KEYPAD        DeviceType = 18
	LAPTOP        DeviceType = 19
	MONITOR       DeviceType = 20
	UNKNOWN       DeviceType = 21
	// mode directions
	LEFT       Direction = 0
	RIGHT      Direction = 1
	UP         Direction = 2
	DOWN       Direction = 3
	HORIZONTAL Direction = 4
	VERTICAL   Direction = 5
	// color modes
	NONE          ColorMode = 0
	PER_LED       ColorMode = 1
	MODE_SPECIFIC ColorMode = 2
	RANDOM        ColorMode = 3
	// speeds
	SLOW   Speed = 0
	MEDIUM Speed = 1
	FAST   Speed = 2
)