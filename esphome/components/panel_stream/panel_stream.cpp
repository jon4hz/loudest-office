#ifdef USE_ESP32

#include "panel_stream.h"

#include <algorithm>
#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <lwip/sockets.h>

#include "esphome/core/hal.h"
#include "esphome/core/log.h"

namespace esphome::panel_stream {

static const char *const TAG = "panel_stream";
static const uint16_t FW_VERSION = 1;
static const uint32_t NO_SIGNAL_MS = 2000;

void PanelStream::setup() {
  this->listen_fd_ = ::socket(AF_INET, SOCK_STREAM, 0);
  int on = 1;
  struct sockaddr_in addr {};
  addr.sin_family = AF_INET;
  addr.sin_port = htons(this->port_);
  addr.sin_addr.s_addr = htonl(INADDR_ANY);
  if (this->listen_fd_ < 0 || ::setsockopt(this->listen_fd_, SOL_SOCKET, SO_REUSEADDR, &on, sizeof on) < 0 ||
      ::bind(this->listen_fd_, (struct sockaddr *) &addr, sizeof addr) < 0 || ::listen(this->listen_fd_, 1) < 0 ||
      ::fcntl(this->listen_fd_, F_SETFL, O_NONBLOCK) < 0) {
    ESP_LOGE(TAG, "listen on port %u: errno %d", this->port_, errno);
    this->mark_failed();
    return;
  }
  this->display_->set_brightness(std::min<uint8_t>(64, this->max_brightness_));
  this->show_(BOOT);
}

void PanelStream::dump_config() {
  ESP_LOGCONFIG(TAG, "Panel stream:\n  Port: %u\n  Max brightness: %u", this->port_, this->max_brightness_);
}

void PanelStream::loop() {
  // A new connection replaces the old one: a restarted host is never locked
  // out by a socket that is half open on this side.
  int fd = ::accept(this->listen_fd_, nullptr, nullptr);
  if (fd >= 0) {
    this->drop_client_();
    int on = 1;
    ::fcntl(fd, F_SETFL, O_NONBLOCK);
    ::setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &on, sizeof on);
    this->client_fd_ = fd;
    this->parser_.reset();
    this->parser_.crcErr = 0;
    this->frames_ok_ = this->seq_gaps_ = 0;
    this->high_freq_.start();  // the default 16 ms loop would cap the frame rate
    ESP_LOGI(TAG, "client connected");
  }

  if (this->client_fd_ >= 0) {
    uint8_t buf[1024];
    for (int i = 0; i < 8; i++) {  // at most two frames per loop, the other components want to run too
      int n = ::recv(this->client_fd_, buf, sizeof buf, 0);
      if (n > 0) {
        for (int j = 0; j < n; j++)
          if (this->parser_.feed(buf[j]))
            this->handle_();
        continue;
      }
      if (n == 0 || (errno != EWOULDBLOCK && errno != EAGAIN))
        this->drop_client_();
      break;
    }
  }

  uint32_t now = millis();
  if (this->what_ == LIVE && now - this->last_frame_ > NO_SIGNAL_MS)
    this->show_(NO_SIGNAL);
  if (this->client_fd_ >= 0 && now - this->last_status_ >= 1000) {
    const uint16_t crc_err = this->parser_.crcErr;
    const uint8_t status[8] = {(uint8_t) this->frames_ok_, (uint8_t) (this->frames_ok_ >> 8),
                               (uint8_t) crc_err,          (uint8_t) (crc_err >> 8),
                               (uint8_t) this->seq_gaps_,  (uint8_t) (this->seq_gaps_ >> 8),
                               this->fps_,                 0xFF};
    this->send_(MSG_STATUS, status, sizeof status);
    ESP_LOGD(TAG, "frames %u, crc errors %u, seq gaps %u, %u fps", this->frames_ok_, crc_err, this->seq_gaps_,
             this->fps_);
    this->fps_ = 0;
    this->last_status_ = now;
  }
}

void PanelStream::handle_() {
  if (this->parser_.type != MSG_HELLO)
    this->seq_gaps_ += (uint8_t) (this->parser_.seq - this->last_seq_ - 1);
  this->last_seq_ = this->parser_.seq;

  switch (this->parser_.type) {
    case MSG_HELLO: {
      const uint8_t info[8] = {PANEL_W & 0xff, PANEL_W >> 8, PANEL_H & 0xff, PANEL_H >> 8,
                               1 /* RGB565 */, 60,           FW_VERSION & 0xff, FW_VERSION >> 8};
      this->send_(MSG_INFO, info, sizeof info);
      break;
    }
    case MSG_FRAME:
      if (this->parser_.len != Parser::MAX || this->parser_.payload[0] != 0)
        break;
      memcpy(this->frame_, this->parser_.payload + 2, sizeof this->frame_);
      this->frames_ok_++, this->fps_++;
      this->last_frame_ = millis();
      this->show_(LIVE);
      break;
    case MSG_CONFIG:  // gamma and rotation are not used yet
      if (this->parser_.len >= 1)
        this->display_->set_brightness(std::min(this->parser_.payload[0], this->max_brightness_));
      break;
    case MSG_BLANK:
      this->show_(BLANK);
      break;
  }
}

void PanelStream::show_(What what) {
  this->what_ = what;
  this->display_->update();  // runs the lambda, which calls draw(), then flips the buffers
}

void PanelStream::draw(display::Display &it) {
  switch (this->what_) {
    case BOOT:  // red, green and blue bars: wrong colours or a garbled picture mean a wrong pin map
      it.filled_rectangle(0, 0, PANEL_W / 3, PANEL_H, Color(255, 0, 0));
      it.filled_rectangle(PANEL_W / 3, 0, PANEL_W / 3, PANEL_H, Color(0, 255, 0));
      it.filled_rectangle(2 * (PANEL_W / 3), 0, PANEL_W - 2 * (PANEL_W / 3), PANEL_H, Color(0, 0, 255));
      break;
    case LIVE:
      it.draw_pixels_at(0, 0, PANEL_W, PANEL_H, this->frame_, display::COLOR_ORDER_RGB, display::COLOR_BITNESS_565,
                        false /* the wire is little-endian */, 0, 0, 0);
      break;
    case NO_SIGNAL:  // a small red cross in the corner
      it.fill(Color::BLACK);
      for (int i = 0; i < 5; i++) {
        it.draw_pixel_at(1 + i, 1 + i, Color(96, 0, 0));
        it.draw_pixel_at(5 - i, 1 + i, Color(96, 0, 0));
      }
      break;
    case BLANK:
      it.fill(Color::BLACK);
      break;
  }
}

void PanelStream::send_(uint8_t type, const uint8_t *payload, uint16_t n) {
  uint8_t pkt[6 + 8 + 2];  // INFO and STATUS carry 8 bytes
  if (this->client_fd_ < 0 || n > 8)
    return;
  pkt[0] = 0xA5, pkt[1] = 0x5A, pkt[2] = type, pkt[3] = this->tx_seq_++, pkt[4] = (uint8_t) n, pkt[5] = n >> 8;
  memcpy(pkt + 6, payload, n);
  uint16_t crc = 0xFFFF;
  for (int i = 2; i < 6 + n; i++)
    crc = ::crc16(crc, pkt[i]);  // proto.h's global crc16, not esphome::crc16 from core/helpers.h
  pkt[6 + n] = (uint8_t) crc, pkt[7 + n] = crc >> 8;
  // 16 bytes into an empty send buffer: a short or failed write means the peer is gone
  if (::send(this->client_fd_, pkt, 8 + n, 0) != 8 + n)
    this->drop_client_();
}

void PanelStream::drop_client_() {
  if (this->client_fd_ < 0)
    return;
  ::close(this->client_fd_);
  this->client_fd_ = -1;
  this->high_freq_.stop();
  ESP_LOGI(TAG, "client gone");
  if (this->what_ == LIVE)
    this->show_(NO_SIGNAL);
}

}  // namespace esphome::panel_stream

#endif
