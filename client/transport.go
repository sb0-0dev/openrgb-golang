package openrgb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// PacketHeader is the 16-byte header that prefixes every OpenRGB packet.
type PacketHeader struct {
	Magic  string
	DevIdx uint32
	PktID  uint32
	Size   uint32
}

// TCP connection structure
type connection struct {
	conn net.Conn
}

const maxPacketPayload = 64 * 1024 * 1024
const packetWriteTimeout = 5 * time.Second

// SetReadDeadline sets the read deadline for the connection
func (c *connection) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// Connect opens a TCP connection to an OpenRGB server.
func Connect(host string, port string) (*connection, error) {
	host = host + ":" + port
	conn, err := net.Dial("tcp", host)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to OpenRGB server at %s: %w", host, err)
	}
	return &connection{conn: conn}, nil

}

// Disconnect closes the TCP connection immediately.
func (c *connection) Disconnect() error {
	return c.conn.Close()
}

// SendPacket writes one OpenRGB packet to the server.
func (c *connection) SendPacket(devIdx, pktID uint32, payload []byte) error {
	buf := new(bytes.Buffer)

	// Magic "ORGB"
	buf.Write([]byte("ORGB"))

	// Header fields
	if err := binary.Write(buf, binary.LittleEndian, devIdx); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, pktID); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(payload))); err != nil {
		return err
	}

	// Payload
	if payload != nil {
		buf.Write(payload)
	}

	// Set a fresh deadline for each write. A single connection-wide deadline
	// would expire while the client remains connected and make later requests fail.
	if err := c.conn.SetWriteDeadline(time.Now().Add(packetWriteTimeout)); err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}
	defer c.conn.SetWriteDeadline(time.Time{})

	written, err := c.conn.Write(buf.Bytes())
	if err != nil {
		return err
	}
	if written != buf.Len() {
		return io.ErrShortWrite
	}
	return nil
}

// ReadPacket reads one packet from the server. With no timeout it blocks until a
// packet arrives; a positive timeout limits the read duration.
func (c *connection) ReadPacket(timeout ...time.Duration) (*PacketHeader, []byte, error) {
	headerBytes := make([]byte, 16)
	if err := c.conn.SetReadDeadline(time.Time{}); err != nil {
		return nil, nil, fmt.Errorf("failed to clear read deadline: %w", err)
	}
	if len(timeout) > 0 && timeout[0] > 0 {
		if err := c.conn.SetReadDeadline(time.Now().Add(timeout[0])); err != nil {
			return nil, nil, fmt.Errorf("failed to set read deadline: %w", err)
		}
	}

	// ReadFull
	if _, err := io.ReadFull(c.conn, headerBytes); err != nil {
		return nil, nil, err
	}

	header := &PacketHeader{
		Magic:  string(headerBytes[0:4]),
		DevIdx: binary.LittleEndian.Uint32(headerBytes[4:8]),
		PktID:  binary.LittleEndian.Uint32(headerBytes[8:12]),
		Size:   binary.LittleEndian.Uint32(headerBytes[12:16]),
	}

	// Validate magic
	if header.Magic != "ORGB" {
		return nil, nil, fmt.Errorf("invalid magic: %q", header.Magic)
	}
	if header.Size > maxPacketPayload {
		return nil, nil, fmt.Errorf("packet payload size %d exceeds maximum %d", header.Size, maxPacketPayload)
	}

	payload := make([]byte, header.Size)

	if header.Size > 0 {
		if _, err := io.ReadFull(c.conn, payload); err != nil {
			return nil, nil, err
		}
	}

	return header, payload, nil
}
