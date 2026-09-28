// TCP endpoint of the panel protocol (see proto.h): what firmware/src/main.cpp
// does on the UART, as an ESPHome component in front of a hub75 display.
#pragma once
#ifdef USE_ESP32

#include "esphome/components/display/display.h"
#include "esphome/components/hub75/hub75_component.h"
#include "esphome/core/component.h"
#include "esphome/core/helpers.h"

#include "proto.h"

namespace esphome::panel_stream {

class PanelStream : public Component {
 public:
  void set_display(hub75::HUB75Display *display) { this->display_ = display; }
  void set_port(uint16_t port) { this->port_ = port; }
  void set_max_brightness(uint8_t b) { this->max_brightness_ = b; }

  void setup() override;
  void loop() override;
  void dump_config() override;
  float get_setup_priority() const override { return setup_priority::AFTER_WIFI; }

  // For the display lambda: paints what the panel shows right now.
  void draw(display::Display &it);

 protected:
  enum What : uint8_t { BOOT, LIVE, NO_SIGNAL, BLANK };

  void show_(What what);
  void handle_();
  void send_(uint8_t type, const uint8_t *payload, uint16_t n);
  void drop_client_();

  hub75::HUB75Display *display_{nullptr};
  uint16_t port_{7090};
  uint8_t max_brightness_{255};

  int listen_fd_{-1};
  int client_fd_{-1};
  HighFrequencyLoopRequester high_freq_;

  Parser parser_;
  uint8_t frame_[PANEL_W * PANEL_H * 2];
  What what_{BOOT};
  uint16_t frames_ok_{0}, seq_gaps_{0};
  uint8_t last_seq_{0}, tx_seq_{0}, fps_{0};
  uint32_t last_frame_{0}, last_status_{0};
};

}  // namespace esphome::panel_stream

#endif
