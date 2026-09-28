// Host check of the packet parser: g++ -std=c++17 -o parser_test parser_test.cpp && ./parser_test
// The golden packets are the ones visualizer/proto/proto_test.go encodes.
#include <cassert>
#include <cstdio>

#include "../src/proto.h"

static int feed(Parser &p, const uint8_t *b, size_t n) {
  int packets = 0;
  for (size_t i = 0; i < n; i++) packets += p.feed(b[i]);
  return packets;
}

int main() {
  static Parser p;
  const uint8_t hello[] = {0xa5, 0x5a, 0x01, 0x00, 0x00, 0x00, 0x74, 0xf2};
  const uint8_t config[] = {0xa5, 0x5a, 0x04, 0x07, 0x03, 0x00, 0x40, 0x16, 0x00, 0xe3, 0xa2};
  const uint8_t garbage[] = {0x00, 0xa5, 0xa5, 0x13};

  assert(feed(p, garbage, sizeof garbage) == 0);
  assert(feed(p, hello, sizeof hello) == 1 && p.type == MSG_HELLO && p.len == 0);
  assert(feed(p, config, sizeof config) == 1);
  assert(p.type == MSG_CONFIG && p.seq == 7 && p.len == 3 && p.payload[0] == 64);

  uint8_t bad[sizeof config];
  for (size_t i = 0; i < sizeof bad; i++) bad[i] = config[i];
  bad[6] ^= 0xff;
  assert(feed(p, bad, sizeof bad) == 0 && p.crcErr == 1);

  const uint8_t oversized[] = {0xa5, 0x5a, 0x03, 0x00, 0xff, 0xff}; // len 65535 > MAX
  assert(feed(p, oversized, sizeof oversized) == 0);
  assert(feed(p, hello, sizeof hello) == 1); // recovered

  // a new TCP client must not inherit half a packet from the last one
  static Parser q;
  assert(feed(q, config, sizeof config / 2) == 0);
  q.reset();
  assert(feed(q, hello, sizeof hello) == 1 && q.crcErr == 0);

  puts("ok");
}
