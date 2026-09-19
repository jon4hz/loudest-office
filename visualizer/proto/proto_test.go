package proto

import (
	"bytes"
	"image/color"
	"testing"
)

func TestCRC16CheckValue(t *testing.T) {
	if got := CRC16([]byte("123456789")); got != 0x29B1 {
		t.Fatalf("CRC16 = %#x, want 0x29b1", got)
	}
}

// The same bytes are checked by firmware/test/parser_test.cpp.
func TestEncodeGolden(t *testing.T) {
	hello := []byte{0xa5, 0x5a, 0x01, 0x00, 0x00, 0x00, 0x74, 0xf2}
	if got := Encode(nil, Hello, 0, nil); !bytes.Equal(got, hello) {
		t.Fatalf("hello = % x", got)
	}
	config := []byte{0xa5, 0x5a, 0x04, 0x07, 0x03, 0x00, 0x40, 0x16, 0x00, 0xe3, 0xa2}
	if got := Encode(nil, Config, 7, ConfigPayload(64)); !bytes.Equal(got, config) {
		t.Fatalf("config = % x", got)
	}
}

func TestDecoderResyncs(t *testing.T) {
	var stream []byte
	stream = append(stream, 0x00, 0xa5, 0xa5, 0x13) // boot garbage, including a lone magic byte
	stream = Encode(stream, Info, 1, []byte{64, 0, 32, 0, 1, 60, 1, 0})
	bad := Encode(nil, Status, 2, make([]byte, 8))
	bad[7] ^= 0xff // corrupt payload
	stream = append(stream, bad...)
	stream = Encode(stream, Blank, 3, nil)

	var d Decoder
	var got []Packet
	for _, b := range stream { // byte at a time: packets may straddle reads
		got = append(got, d.Feed([]byte{b})...)
	}
	if len(got) != 2 || got[0].Type != Info || got[1].Type != Blank || got[1].Seq != 3 {
		t.Fatalf("packets = %+v", got)
	}
	if d.CRCErrors != 1 {
		t.Fatalf("CRCErrors = %d, want 1", d.CRCErrors)
	}
	info, ok := ParseInfo(got[0].Payload)
	if !ok || info != (InfoMsg{W: 64, H: 32, PixFmts: 1, MaxFPS: 60, FW: 1}) {
		t.Fatalf("info = %+v %v", info, ok)
	}
}

func TestDecoderDropsOversized(t *testing.T) {
	d := Decoder{Max: 8}
	if got := d.Feed(Encode(nil, Frame, 0, make([]byte, 9))); len(got) != 0 {
		t.Fatalf("oversized packet accepted: %+v", got)
	}
	if got := d.Feed(Encode(nil, Blank, 1, nil)); len(got) != 1 {
		t.Fatalf("decoder did not recover: %+v", got)
	}
}

func TestParseStatus(t *testing.T) {
	s, ok := ParseStatus([]byte{0x2b, 0x00, 0x01, 0x00, 0x02, 0x00, 43, 0xff})
	if !ok || s != (StatusMsg{FramesOK: 43, CRCErr: 1, SeqGaps: 2, FPS: 43, TempC: 0xff}) {
		t.Fatalf("status = %+v %v", s, ok)
	}
	if _, ok := ParseStatus([]byte{1, 2, 3}); ok {
		t.Fatal("short status accepted")
	}
}

func TestFramePayloadRGB565(t *testing.T) {
	frame := [][]color.RGBA{{{R: 255, A: 255}, {G: 255}}, {{B: 255}, {R: 255, G: 255, B: 255}}}
	want := []byte{0, 0, 0x00, 0xf8, 0xe0, 0x07, 0x1f, 0x00, 0xff, 0xff}
	if got := FramePayload(nil, frame); !bytes.Equal(got, want) {
		t.Fatalf("payload = % x", got)
	}
}
