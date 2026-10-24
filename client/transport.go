// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	maxPacketPayload   = 64 * 1024 * 1024
	packetWriteTimeout = 5 * time.Second
	packetHeaderSize   = 16
)

// PacketHeader is the 16-byte header that prefixes every OpenRGB packet.
// Its fields are serialized in little-endian order after the four-byte ORGB magic.
type PacketHeader struct {
	Magic  string
	DevIdx uint32
	PktID  uint32
	Size   uint32
}

// connection represents a TCP connection to an OpenRGB server.
type connection struct {
	conn net.Conn
}

// Connect opens a TCP connection to an OpenRGB server.
func Connect(host, port string) (*connection, error) {
	address := net.JoinHostPort(host, port)

	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to connect to OpenRGB server at %s: %w",
			address, err,
		)
	}

	return &connection{conn: conn}, nil
}

// Disconnect closes the TCP connection immediately.
func (c *connection) Disconnect() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// SetReadDeadline sets the read deadline for the underlying TCP connection.
func (c *connection) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SendPacket writes one complete OpenRGB packet, including its header.
// A write failure should be treated as a connection failure because part of
// the packet may already have been written to the TCP stream.
func (c *connection) SendPacket(devIdx, pktID uint32, payload []byte) error {
	if c == nil || c.conn == nil {
		return errors.New("connection is nil")
	}

	if len(payload) > maxPacketPayload {
		return fmt.Errorf(
			"packet payload size %d exceeds maximum %d",
			len(payload), maxPacketPayload,
		)
	}

	packet := make([]byte, packetHeaderSize+len(payload))
	copy(packet[:4], "ORGB")
	binary.LittleEndian.PutUint32(packet[4:8], devIdx)
	binary.LittleEndian.PutUint32(packet[8:12], pktID)
	binary.LittleEndian.PutUint32(packet[12:16], uint32(len(payload)))
	copy(packet[packetHeaderSize:], payload)

	if err := c.conn.SetWriteDeadline(time.Now().Add(packetWriteTimeout)); err != nil {
		return fmt.Errorf("failed to set packet write deadline: %w", err)
	}
	defer c.conn.SetWriteDeadline(time.Time{})

	for len(packet) > 0 {
		n, err := c.conn.Write(packet)
		if n > 0 {
			packet = packet[n:]
		}
		if err != nil {
			return fmt.Errorf("failed to write packet: %w", err)
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}

	return nil
}

// ReadPacket reads one complete OpenRGB packet.
//
// With no timeout, the read blocks until a packet arrives.
// A positive timeout limits the time spent reading the packet.
//
// A timeout before any header bytes are received is recoverable. If the
// connection times out after consuming part of a packet, the stream cannot
// safely be resumed with another ReadPacket call.
func (c *connection) ReadPacket(timeout ...time.Duration) (*PacketHeader, []byte, error) {
	if c == nil || c.conn == nil {
		return nil, nil, errors.New("connection is nil")
	}

	if err := c.conn.SetReadDeadline(time.Time{}); err != nil {
		return nil, nil, fmt.Errorf("failed to clear read deadline: %w", err)
	}

	if len(timeout) > 0 && timeout[0] > 0 {
		if err := c.conn.SetReadDeadline(time.Now().Add(timeout[0])); err != nil {
			return nil, nil, fmt.Errorf("failed to set read deadline: %w", err)
		}
	}

	headerBytes := make([]byte, packetHeaderSize)
	n, err := io.ReadFull(c.conn, headerBytes)
	if err != nil {
		if n == 0 && isTimeout(err) {
			return nil, nil, err
		}
		if n > 0 {
			return nil, nil, fmt.Errorf(
				"incomplete packet header (%d/%d bytes): %w",
				n, packetHeaderSize, err,
			)
		}
		return nil, nil, err
	}

	header := &PacketHeader{
		Magic:  string(headerBytes[:4]),
		DevIdx: binary.LittleEndian.Uint32(headerBytes[4:8]),
		PktID:  binary.LittleEndian.Uint32(headerBytes[8:12]),
		Size:   binary.LittleEndian.Uint32(headerBytes[12:16]),
	}

	if header.Magic != "ORGB" {
		return nil, nil, fmt.Errorf("invalid magic: %q", header.Magic)
	}
	if header.Size > maxPacketPayload {
		return nil, nil, fmt.Errorf(
			"packet payload size %d exceeds maximum %d",
			header.Size, maxPacketPayload,
		)
	}

	if header.Size == 0 {
		return header, nil, nil
	}

	payload := make([]byte, int(header.Size))
	n, err = io.ReadFull(c.conn, payload)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"incomplete packet payload (%d/%d bytes): %w",
			n, len(payload), err,
		)
	}

	return header, payload, nil
}

// isTimeout reports whether err represents a network timeout.
func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
