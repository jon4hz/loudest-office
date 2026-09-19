// Packet parser of the Pi <-> ESP32 panel protocol. No Arduino includes, so it
// also compiles on the host (see test/parser_test.cpp).
//
//   A5 5A | type | seq | len u16 | payload | crc u16     little-endian,
//   crc = CRC-16/CCITT-FALSE over type..payload
#pragma once
#include <stddef.h>
#include <stdint.h>

#ifndef PANEL_W
#define PANEL_W 64
#endif
#ifndef PANEL_H
#define PANEL_H 32
#endif

// MSG_ prefix: the ESP32 ROM headers already have a STATUS.
enum : uint8_t { MSG_HELLO = 1, MSG_INFO, MSG_FRAME, MSG_CONFIG, MSG_STATUS, MSG_BLANK };

inline uint16_t crc16(uint16_t crc, uint8_t b) {
  crc ^= (uint16_t)b << 8;
  for (int i = 0; i < 8; i++) crc = crc & 0x8000 ? (crc << 1) ^ 0x1021 : crc << 1;
  return crc;
}

struct Parser {
  static const size_t MAX = 2 + PANEL_W * PANEL_H * 2; // a FRAME payload

  uint8_t type, seq;
  uint16_t len;
  uint8_t payload[MAX];
  uint32_t crcErr = 0;

  // feed returns true when b completed a valid packet; type, seq, len and
  // payload then describe it until the next call.
  bool feed(uint8_t b) {
    switch (state) {
    case MAGIC1:
      if (b == 0xA5) state = MAGIC2;
      return false;
    case MAGIC2:
      state = b == 0x5A ? HEADER : b == 0xA5 ? MAGIC2 : MAGIC1;
      n = 0;
      crc = 0xFFFF;
      return false;
    case HEADER:
      crc = crc16(crc, b);
      hdr[n++] = b;
      if (n < 4) return false;
      type = hdr[0], seq = hdr[1], len = hdr[2] | hdr[3] << 8;
      n = 0;
      state = len > MAX ? MAGIC1 : len ? PAYLOAD : CRC;
      return false;
    case PAYLOAD:
      crc = crc16(crc, b);
      payload[n++] = b;
      if (n == len) n = 0, state = CRC;
      return false;
    case CRC:
      hdr[n++] = b;
      if (n < 2) return false;
      state = MAGIC1;
      if ((hdr[0] | hdr[1] << 8) == crc) return true;
      crcErr++;
      return false;
    }
    return false;
  }

private:
  enum { MAGIC1, MAGIC2, HEADER, PAYLOAD, CRC } state = MAGIC1;
  uint8_t hdr[4];
  size_t n = 0;
  uint16_t crc = 0;
};
