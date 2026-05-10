package server

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestWSFrame_EncodeShort(t *testing.T) {
	got := wsEncode("hello")
	if len(got) != 7 {
		t.Fatalf("len = %d, want 7", len(got))
	}
	if got[0] != 0x81 {
		t.Errorf("byte0 = %#x, want 0x81", got[0])
	}
	if got[1] != 0x05 {
		t.Errorf("byte1 = %#x, want 0x05", got[1])
	}
	if string(got[2:]) != "hello" {
		t.Errorf("payload = %q, want %q", string(got[2:]), "hello")
	}
}

func TestWSFrame_EncodeMedium(t *testing.T) {
	payload := strings.Repeat("a", 200)
	got := wsEncode(payload)
	if len(got) != 4+200 {
		t.Fatalf("len = %d, want %d", len(got), 4+200)
	}
	if got[0] != 0x81 {
		t.Errorf("byte0 = %#x, want 0x81", got[0])
	}
	if got[1] != 126 {
		t.Errorf("byte1 = %d, want 126", got[1])
	}
	if n := binary.BigEndian.Uint16(got[2:4]); n != 200 {
		t.Errorf("ext length = %d, want 200", n)
	}
	if string(got[4:]) != payload {
		t.Errorf("payload mismatch")
	}
}

func TestWSFrame_EncodeLong(t *testing.T) {
	payload := strings.Repeat("b", 70000)
	got := wsEncode(payload)
	if len(got) != 10+70000 {
		t.Fatalf("len = %d, want %d", len(got), 10+70000)
	}
	if got[0] != 0x81 {
		t.Errorf("byte0 = %#x, want 0x81", got[0])
	}
	if got[1] != 127 {
		t.Errorf("byte1 = %d, want 127", got[1])
	}
	if n := binary.BigEndian.Uint64(got[2:10]); n != 70000 {
		t.Errorf("ext length = %d, want 70000", n)
	}
	if string(got[10:]) != payload {
		t.Errorf("payload mismatch")
	}
}

func TestWSFrame_DecodeUnmasked(t *testing.T) {
	frame := []byte{0x81, 0x05, 'h', 'e', 'l', 'l', 'o'}
	op, payload := wsReadFrame(bytes.NewReader(frame))
	if op != 1 {
		t.Errorf("opcode = %d, want 1", op)
	}
	if string(payload) != "hello" {
		t.Errorf("payload = %q, want %q", string(payload), "hello")
	}
}

func TestWSFrame_DecodeMasked(t *testing.T) {
	mask := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	plain := []byte("ping")
	masked := make([]byte, len(plain))
	for i, b := range plain {
		masked[i] = b ^ mask[i%4]
	}
	frame := []byte{0x81, byte(0x80 | len(plain))}
	frame = append(frame, mask...)
	frame = append(frame, masked...)

	op, payload := wsReadFrame(bytes.NewReader(frame))
	if op != 1 {
		t.Errorf("opcode = %d, want 1", op)
	}
	if string(payload) != "ping" {
		t.Errorf("payload = %q, want %q", string(payload), "ping")
	}
}

func TestWSFrame_DecodeClose(t *testing.T) {
	frame := []byte{0x88, 0x00}
	op, payload := wsReadFrame(bytes.NewReader(frame))
	if op != 8 {
		t.Errorf("opcode = %d, want 8", op)
	}
	if len(payload) != 0 {
		t.Errorf("payload len = %d, want 0", len(payload))
	}
}

func TestWSFrame_DecodeShortRead(t *testing.T) {
	frame := []byte{0x81}
	op, payload := wsReadFrame(bytes.NewReader(frame))
	if op != 8 {
		t.Errorf("opcode = %d, want 8 on short read", op)
	}
	if len(payload) != 0 {
		t.Errorf("payload len = %d, want 0", len(payload))
	}
}

func TestWSHandshake_Accept(t *testing.T) {
	got := wsAccept("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Errorf("wsAccept = %q, want %q", got, want)
	}
}

func TestWSFrame_DecodeOversized(t *testing.T) {
	frame := []byte{0x81, 0x7F}
	ext := make([]byte, 8)
	binary.BigEndian.PutUint64(ext, uint64(maxFrameSize)+1)
	frame = append(frame, ext...)
	op, payload := wsReadFrame(bytes.NewReader(frame))
	if op != 8 {
		t.Errorf("opcode = %d, want 8 on oversize frame", op)
	}
	if len(payload) != 0 {
		t.Errorf("payload len = %d, want 0", len(payload))
	}
}

func TestWSFrame_DecodeOversized_NegativeWraparound(t *testing.T) {
	frame := []byte{0x81, 0x7F}
	ext := make([]byte, 8)
	binary.BigEndian.PutUint64(ext, ^uint64(0))
	frame = append(frame, ext...)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("wsReadFrame panicked on uint64 max length: %v", r)
		}
	}()
	op, _ := wsReadFrame(bytes.NewReader(frame))
	if op != 8 {
		t.Errorf("opcode = %d, want 8", op)
	}
}
