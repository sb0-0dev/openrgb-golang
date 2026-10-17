# OpenRGB Effects Plugin SDK documentation

OpenRGB provides a network-base Software Development Kit (SDK) interface for third-party software application to integrate with OpenRGB.
For more details, refer to the [OpenRGB SDK docs](https://gitlab.com/CalcProgrammer1/OpenRGB/-/blob/master/Documentation/OpenRGBSDK.md).

The OpenRGB SDK contains a [plugin-specific command](https://gitlab.com/CalcProgrammer1/OpenRGB/-/blob/master/Documentation/OpenRGBSDK.md#net_packet_id_plugin_specific), which is implemented by the effects plugin.
This document details the format of the OpenRGB Effects plugin SDK.

## Protocol Versions

| Protocol Version | OpenRGBEffectsPlugin Release | Description                                                                                                    |
| ---------------- | ---------------------------- | -------------------------------------------------------------------------------------------------------------- |
| 0                | 0.x                          | Initial version                                                                                                |

## Packet format

Every command must be sent using the [NET_PACKET_ID_PLUGIN_SPECIFIC](https://gitlab.com/CalcProgrammer1/OpenRGB/-/blob/master/Documentation/OpenRGBSDK.md#net_packet_id_plugin_specific) packet.
Everything that is described here is sent in the `data` field of an `OpenRGB` packet.

## Header

This header is sent and received with every message.

| Size          | Format            | Name              | Description                   |
| ------------- | ----------------- | ----------------- | ----------------------------- |
| 4             | unsigned int      | fx_pkt_id         | Effects plugin packet ID      |
| Variable      | packet-specific   | fx_pkt_data       | Device Index                  |

See the table below for packet ids

## Packet IDs

| Value | Name                                                | Description                                      | Protocol Version |
| ----- | ----------------------------------------------------| ------------------------------------------------ | ---------------- |
| 0     | [REQUEST_EFFECT_LIST](#request_effect_list)         | Request [Effect List](#effect-list) data block   | 0                |
| 20    | [START_EFFECT](#start_effect--stop_effect)          | Start an effect                                  | 0                |
| 41    | [STOP_EFFECT](#start_effect--stop_effect)           | Stop an effect                                   | 0                |

## Effect List

| Size                              | Format                    | Name                | Protocol Version | Description                              |
| --------------------------------- | ------------------------- | ------------------- | ---------------- | -----------------------------------------|
| 4                                 | unsigned int              | data_size           | 0                | size of all data in packet               |
| 2                                 | unsigned short            | num_effects         | 0                | Number of effects values in packet       |
| num_effects * size[Effect]        | char[mode_name_len]       | mode_name           | 0                | Effects, see [Effect data](#effect-data) |

## Effect Data

| Size                | Format                    | Name                | Protocol Version | Description                                                     |
| ------------------- | ------------------------- | ------------------- | ---------------- | --------------------------------------------------------------- |
| 2                   | unsigned short            | effect_name_len     | 0                | Length of effect name string, including null termination        |
| effect_name_len     | char[effect_name_len]     | effect_name         | 0                | Effect name string value, including null termination            |
| 2                   | unsigned short            | effect_desc_len     | 0                | Length of effect description string, including null termination |
| effect_desc_len     | char[effect_desc_len]     | effect_desc         | 0                | Effect description string value, including null termination     |
| 1                   | boolean (unsigned byte)   | effect_enabled      | 0                | Effect enabled value.                                           |

## REQUEST_EFFECT_LIST

Request a list of currently existing effects. This command contains no `fx_pkt_data`
This command responds with an [Effect List](#effect-list)

## START_EFFECT / #STOP_EFFECT

Starts an effect that is available by name. This command sends no response.

| Size                | Format                    | Name                | Protocol Version | Description                                                     |
| ------------------- | ------------------------- | ------------------- | ---------------- | --------------------------------------------------------------- |
| 2                   | unsigned short            | effect_name_len     | 0                | Length of effect name string, including null termination        |
| effect_name_len     | char[effect_name_len]     | effect_name         | 0                | Effect name string value, including null termination            |
