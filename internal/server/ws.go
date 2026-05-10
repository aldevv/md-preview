// Package server implements the md-preview HTTP+WebSocket preview server.
package server

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"io"
)

// wsMagic is the WebSocket GUID from RFC 6455, used to derive the
// Sec-WebSocket-Accept response header from the client's nonce.
const wsMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// maxFrameSize caps the payload size of an inbound WS frame. The server is
// loopback-only and only ever exchanges tiny JSON commands, so anything
// larger is malformed or hostile. A bound is required because the 64-bit
// length form would otherwise overflow int and panic make([]byte).
const maxFrameSize = 1 << 20 // 1 MiB

// wsAccept computes the Sec-WebSocket-Accept response value for a given
// Sec-WebSocket-Key request header.
func wsAccept(key string) string {
	sum := sha1.Sum([]byte(key + wsMagic))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// wsEncode encodes a UTF-8 text payload as a single unmasked WebSocket
// frame (FIN=1, opcode=0x1, MASK=0). The length encoding uses 7-bit,
// 7+16-bit, or 7+64-bit forms per RFC 6455 §5.2.
func wsEncode(message string) []byte {
	data := []byte(message)
	n := len(data)
	switch {
	case n <= 125:
		out := make([]byte, 2+n)
		out[0] = 0x81
		out[1] = byte(n)
		copy(out[2:], data)
		return out
	case n <= 65535:
		out := make([]byte, 4+n)
		out[0] = 0x81
		out[1] = 126
		binary.BigEndian.PutUint16(out[2:4], uint16(n))
		copy(out[4:], data)
		return out
	default:
		out := make([]byte, 10+n)
		out[0] = 0x81
		out[1] = 127
		binary.BigEndian.PutUint64(out[2:10], uint64(n))
		copy(out[10:], data)
		return out
	}
}

// recvExact reads exactly n bytes from r. Returns whatever was read on EOF
// or read error so callers can detect short reads via len(buf) < n.
func recvExact(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

// wsReadFrame reads a single WebSocket frame from r and returns
// (opcode, payload). On any error, short read, or oversized frame it
// returns (8, nil) so the caller treats it as a close.
func wsReadFrame(r io.Reader) (byte, []byte) {
	header, err := recvExact(r, 2)
	if err != nil || len(header) < 2 {
		return 8, nil
	}
	opcode := header[0] & 0x0F
	length := uint64(header[1] & 0x7F)
	switch length {
	case 126:
		ext, err := recvExact(r, 2)
		if err != nil || len(ext) < 2 {
			return 8, nil
		}
		length = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext, err := recvExact(r, 8)
		if err != nil || len(ext) < 8 {
			return 8, nil
		}
		length = binary.BigEndian.Uint64(ext)
	}
	if length > maxFrameSize {
		return 8, nil
	}

	masked := header[1]&0x80 != 0
	var mask []byte
	if masked {
		m, err := recvExact(r, 4)
		if err != nil || len(m) < 4 {
			return 8, nil
		}
		mask = m
	}

	payload, err := recvExact(r, int(length))
	if err != nil || uint64(len(payload)) < length {
		return 8, nil
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload
}
