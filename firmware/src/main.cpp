// HUB75 panel driver: shows the FRAMEs the Pi streams over USB serial.
#include <Arduino.h>
#include <ESP32-HUB75-MatrixPanel-I2S-DMA.h>

#include "proto.h"

#ifndef SERIAL_BAUD
#define SERIAL_BAUD 921600
#endif

#ifndef PANEL_MAX_BRIGHTNESS // lower it for a panel without its own supply
#define PANEL_MAX_BRIGHTNESS 255
#endif

static const uint16_t FW_VERSION = 1;
static const uint32_t NO_SIGNAL_MS = 2000;

static MatrixPanel_I2S_DMA *panel;
static Parser parser;
static uint16_t framesOk, seqGaps;
static uint8_t lastSeq, fps;
static uint32_t lastFrame, lastStatus;
static bool live; // a FRAME is on the panel

static void send(uint8_t type, const uint8_t *p, uint16_t n) {
  static uint8_t seq;
  const uint8_t h[6] = {0xA5, 0x5A, type, seq++, (uint8_t)n, (uint8_t)(n >> 8)};
  uint16_t crc = 0xFFFF;
  for (int i = 2; i < 6; i++) crc = crc16(crc, h[i]);
  for (int i = 0; i < n; i++) crc = crc16(crc, p[i]);
  const uint8_t t[2] = {(uint8_t)crc, (uint8_t)(crc >> 8)};
  Serial.write(h, 6);
  Serial.write(p, n);
  Serial.write(t, 2);
}

// Red, green and blue bars: wrong colours or a garbled picture mean wrong wiring.
static void bootPattern() {
  for (int y = 0; y < PANEL_H; y++)
    for (int x = 0; x < PANEL_W; x++) {
      int bar = x * 3 / PANEL_W;
      panel->drawPixelRGB888(x, y, bar == 0 ? 255 : 0, bar == 1 ? 255 : 0, bar == 2 ? 255 : 0);
    }
  panel->flipDMABuffer();
}

// A small red cross in the corner.
static void noSignal() {
  panel->clearScreen();
  for (int i = 0; i < 5; i++) {
    panel->drawPixelRGB888(1 + i, 1 + i, 96, 0, 0);
    panel->drawPixelRGB888(5 - i, 1 + i, 96, 0, 0);
  }
  panel->flipDMABuffer();
}

static void handle() {
  if (parser.type != MSG_HELLO) seqGaps += (uint8_t)(parser.seq - lastSeq - 1);
  lastSeq = parser.seq;

  switch (parser.type) {
  case MSG_HELLO: {
    const uint8_t info[8] = {PANEL_W & 0xff, PANEL_W >> 8, PANEL_H & 0xff, PANEL_H >> 8,
                             1 /* RGB565 */, 60, FW_VERSION & 0xff, FW_VERSION >> 8};
    send(MSG_INFO, info, sizeof info);
    break;
  }
  case MSG_FRAME: {
    if (parser.len != Parser::MAX || parser.payload[0] != 0) break;
    const uint8_t *px = parser.payload + 2;
    for (int y = 0; y < PANEL_H; y++)
      for (int x = 0; x < PANEL_W; x++, px += 2) panel->drawPixel(x, y, (uint16_t)(px[0] | px[1] << 8));
    panel->flipDMABuffer();
    framesOk++, fps++;
    lastFrame = millis();
    live = true;
    break;
  }
  case MSG_CONFIG: // gamma and rotation are not used yet
    if (parser.len >= 1) panel->setBrightness8(min<uint8_t>(parser.payload[0], PANEL_MAX_BRIGHTNESS));
    break;
  case MSG_BLANK:
    panel->clearScreen();
    panel->flipDMABuffer();
    live = false;
    break;
  }
}

void setup() {
  Serial.setRxBufferSize(16384); // a bit more than two frames
  Serial.begin(SERIAL_BAUD);

  HUB75_I2S_CFG cfg(PANEL_W, PANEL_H, 1);
  cfg.double_buff = true;
#ifdef PANEL_PINS // R1,G1,B1,R2,G2,B2,A,B,C,D,E,LAT,OE,CLK if not wired to the library defaults
  cfg.gpio = {PANEL_PINS};
#endif
#ifdef PANEL_DRIVER // panels with an FM6126A or ICN2038S need an init sequence
  cfg.driver = HUB75_I2S_CFG::PANEL_DRIVER;
#endif
#ifdef PANEL_LINE_DECODER // row addressing; TYPE595 or SM5266P if only one row ever lights up
  cfg.line_decoder = HUB75_I2S_CFG::PANEL_LINE_DECODER;
#endif
#ifdef PANEL_CLKPHASE // 0 if the picture is shifted by a column or ghosts
  cfg.clkphase = PANEL_CLKPHASE;
#endif
  panel = new MatrixPanel_I2S_DMA(cfg);
  panel->begin();
  panel->setBrightness8(64);
  bootPattern();
}

void loop() {
  static uint8_t buf[512];
  int n = Serial.available();
  if (n > 0) {
    n = Serial.read(buf, min(n, (int)sizeof buf));
    for (int i = 0; i < n; i++)
      if (parser.feed(buf[i])) handle();
  }

  uint32_t now = millis();
  if (live && now - lastFrame > NO_SIGNAL_MS) {
    noSignal();
    live = false;
  }
  if (now - lastStatus >= 1000) {
    const uint16_t crcErr = parser.crcErr;
    const uint8_t status[8] = {(uint8_t)framesOk, (uint8_t)(framesOk >> 8), (uint8_t)crcErr, (uint8_t)(crcErr >> 8),
                               (uint8_t)seqGaps, (uint8_t)(seqGaps >> 8), fps, 0xFF};
    send(MSG_STATUS, status, sizeof status);
    fps = 0;
    lastStatus = now;
  }
}
