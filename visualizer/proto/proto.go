// Package proto is the packet protocol between the Pi and the ESP32 that
// drives the panel, see docs/superpowers/specs/2026-09-18-panel-streaming-design.md.
//
//	A5 5A | type | seq | len u16 | payload | crc u16
//
// Little-endian; the CRC is CRC-16/CCITT-FALSE over type..payload.
package proto

import (
	"encoding/binary"
	"image/color"
)

const (
	Hello  byte = 0x01 // Pi→ESP, empty; the ESP answers Info
	Info   byte = 0x02 // ESP→Pi, see InfoMsg
	Frame  byte = 0x03 // Pi→ESP, see FramePayload
	Config byte = 0x04 // Pi→ESP, see ConfigPayload
	Status byte = 0x05 // ESP→Pi, once a second, see StatusMsg
	Blank  byte = 0x06 // Pi→ESP, empty; clears the panel
)

// CRC16 is CRC-16/CCITT-FALSE: poly 0x1021, init 0xFFFF.
func CRC16(p []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range p {
		crc ^= uint16(b) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// Encode appends one packet to dst.
func Encode(dst []byte, typ, seq byte, payload []byte) []byte {
	start := len(dst)
	dst = append(dst, 0xA5, 0x5A, typ, seq)
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(payload)))
	dst = append(dst, payload...)
	return binary.LittleEndian.AppendUint16(dst, CRC16(dst[start+2:]))
}

type Packet struct {
	Type, Seq byte
	Payload   []byte
}

// Decoder finds packets in a byte stream. Anything that is not a valid packet
// (boot messages, a corrupt packet) is skipped.
type Decoder struct {
	Max       int // longest payload accepted, 0 = any
	CRCErrors int

	magic int    // magic bytes seen, 0..2
	buf   []byte // type, seq, len, payload, crc
}

// Feed consumes p and returns the packets completed by it.
func (d *Decoder) Feed(p []byte) []Packet {
	var out []Packet
	for _, b := range p {
		switch d.magic {
		case 0:
			if b == 0xA5 {
				d.magic = 1
			}
			continue
		case 1:
			switch b {
			case 0x5A:
				d.magic, d.buf = 2, d.buf[:0]
			case 0xA5:
			default:
				d.magic = 0
			}
			continue
		}
		d.buf = append(d.buf, b)
		if len(d.buf) < 4 {
			continue
		}
		n := int(binary.LittleEndian.Uint16(d.buf[2:]))
		if d.Max > 0 && n > d.Max {
			d.magic = 0
			continue
		}
		if len(d.buf) < 4+n+2 {
			continue
		}
		if CRC16(d.buf[:4+n]) == binary.LittleEndian.Uint16(d.buf[4+n:]) {
			out = append(out, Packet{d.buf[0], d.buf[1], append([]byte(nil), d.buf[4:4+n]...)})
		} else {
			d.CRCErrors++
		}
		d.magic = 0
	}
	return out
}

// InfoMsg is what the ESP reports about itself.
type InfoMsg struct {
	W, H    uint16 // logical canvas
	PixFmts byte   // bit 0 = RGB565
	MaxFPS  byte
	FW      uint16
}

func ParseInfo(p []byte) (InfoMsg, bool) {
	if len(p) < 8 {
		return InfoMsg{}, false
	}
	le := binary.LittleEndian
	return InfoMsg{le.Uint16(p), le.Uint16(p[2:]), p[4], p[5], le.Uint16(p[6:])}, true
}

// StatusMsg holds the ESP's counters; they wrap at 65535.
type StatusMsg struct {
	FramesOK uint16 `json:"frames_ok"`
	CRCErr   uint16 `json:"crc_err"`
	SeqGaps  uint16 `json:"seq_gaps"`
	FPS      byte   `json:"fps"`
	TempC    byte   `json:"temp_c"` // 0xFF = no sensor
}

func ParseStatus(p []byte) (StatusMsg, bool) {
	if len(p) < 8 {
		return StatusMsg{}, false
	}
	le := binary.LittleEndian
	return StatusMsg{le.Uint16(p), le.Uint16(p[2:]), le.Uint16(p[4:]), p[6], p[7]}, true
}

// ConfigPayload sets the brightness; gamma stays 2.2 and rotation 0.
func ConfigPayload(brightness byte) []byte { return []byte{brightness, 22, 0} }

// FramePayload appends a Frame payload: pixfmt RGB565, no flags, then the
// pixels row by row. Alpha is ignored, like in the terminal view.
func FramePayload(dst []byte, frame [][]color.RGBA) []byte {
	dst = append(dst, 0, 0)
	for _, row := range frame {
		for _, c := range row {
			dst = binary.LittleEndian.AppendUint16(dst, uint16(c.R>>3)<<11|uint16(c.G>>2)<<5|uint16(c.B>>3))
		}
	}
	return dst
}
