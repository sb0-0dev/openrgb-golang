// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestConnectionSendAndReadPacket(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	connection := &connection{conn: client}
	wantPayload := []byte{1, 2, 3, 4}
	readResult := make(chan struct {
		header  *PacketHeader
		payload []byte
		err     error
	}, 1)
	go func() {
		headerBytes := make([]byte, 16)
		_, err := io.ReadFull(server, headerBytes)
		if err != nil {
			readResult <- struct {
				header  *PacketHeader
				payload []byte
				err     error
			}{err: err}
			return
		}
		payload := make([]byte, binary.LittleEndian.Uint32(headerBytes[12:16]))
		_, err = io.ReadFull(server, payload)
		readResult <- struct {
			header  *PacketHeader
			payload []byte
			err     error
		}{
			header: &PacketHeader{
				Magic:  string(headerBytes[:4]),
				DevIdx: binary.LittleEndian.Uint32(headerBytes[4:8]),
				PktID:  binary.LittleEndian.Uint32(headerBytes[8:12]),
				Size:   binary.LittleEndian.Uint32(headerBytes[12:16]),
			},
			payload: payload,
		}
	}()

	if err := connection.SendPacket(7, uint32(PKT_RGBCONTROLLER_UPDATELEDS), wantPayload); err != nil {
		t.Fatalf("SendPacket returned error: %v", err)
	}
	result := <-readResult
	if result.err != nil {
		t.Fatalf("server read returned error: %v", result.err)
	}
	if result.header.Magic != "ORGB" || result.header.DevIdx != 7 || result.header.PktID != uint32(PKT_RGBCONTROLLER_UPDATELEDS) || result.header.Size != 4 {
		t.Fatalf("unexpected header: %+v", result.header)
	}
	if string(result.payload) != string(wantPayload) {
		t.Fatalf("unexpected payload: %v", result.payload)
	}
}

func TestConnectionReadPacketRejectsInvalidMagic(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	result := make(chan error, 1)
	go func() {
		_, _, err := (&connection{conn: client}).ReadPacket(time.Second)
		result <- err
	}()
	if _, err := server.Write([]byte("BAD!" + strings.Repeat("\x00", 12))); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := <-result; err == nil || err.Error() != `invalid magic: "BAD!"` {
		t.Fatalf("expected invalid magic error, got %v", err)
	}
}

func TestConnectionReadPacketRejectsOversizedPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	result := make(chan error, 1)
	go func() {
		_, _, err := (&connection{conn: client}).ReadPacket(time.Second)
		result <- err
	}()
	header := make([]byte, 16)
	copy(header, "ORGB")
	binary.LittleEndian.PutUint32(header[12:16], maxPacketPayload+1)
	if _, err := server.Write(header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := <-result; err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("ReadPacket() error = %v, want oversized payload error", err)
	}
}

func TestConnectionSetReadDeadline(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	deadline := time.Now().Add(time.Second)
	if err := (&connection{conn: clientConn}).SetReadDeadline(deadline); err != nil {
		t.Fatalf("SetReadDeadline() returned error: %v", err)
	}
}
