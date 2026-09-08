# Loudest Office

This repo holds all the code required for the audio setup in our office. Our office already had the nickname "loud office" because we aren't exactly quiet and like to listen to music.

We've now decided to live up to our name and go the extra mile to become the *loudest* office. And because we are an office full of nerds and engineers, we don't want to do anything half-baked.

The idea is to have a Raspberry Pi running Music Assistant, so that everyone in the office can control the music.

We will then use the HiFiBerry DAC output to feed the signal to the CB10 speaker. We will also listen on the ADC input so that we can do fancy visualizations on the matrix panel.


## Hardware

* 1x [QSC CB10](https://www.qscaudio.com/products-solutions/loudspeakers/portable/powered/portable-pa/cb-battery-powered-loundspeaker/cb10/) speaker
* 2x cheap microphones to torture our coworkers with extra bad karaoke sessions
* 1x [Raspberry Pi 5](https://www.raspberrypi.com/products/raspberry-pi-5/)
* 1x [HiFiBerry Studio DAC/ADC XLR](https://www.hifiberry.com/shop/boards/hifiberry-studio-dacadc-xlr/)
* 1x [Hub75 Matrix Panel](https://www.bastelgarage.ch/rgb-p3-matrix-panel-64x32-hub75)
* 1x [ESP32](https://www.espressif.com/en/products/socs/esp32) to control the matrix panel


## Software

* Music Assistant

## Configuring the Pi

### Requirements
* podman
* uv
* working avahi (mDNS) setup

