// Package tronbyt is an idle bubble: the visualizer plays the part of a
// Tronbyt device. While shown it pulls the device's next image from a Tronbyt
// server (GET /{device_id}/next, an animated WebP), plays it for the dwell
// time the server names and then asks for the next one. Every request
// advances the server's app rotation, so it only polls while it is shown.
package tronbyt

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"net/http"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gen2brain/webp" // libwebp as WASM: animations without cgo

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

const (
	defaultDwell = 15 * time.Second // the server's own default
	retry        = 10 * time.Second // after a failed fetch
	defaultDelay = 100 * time.Millisecond
	maxBody      = 1 << 20
)

// dim is the placeholder's colour, the clock's.
var dim = color.RGBA{153, 153, 153, 255}

var client = &http.Client{Timeout: 10 * time.Second}

// fetched is the answer to one fetch.
type fetched struct {
	images []image.Image
	delays []time.Duration
	dwell  time.Duration
	err    error
}

// Tronbyt shows what a Tronbyt server renders for one device.
type Tronbyt struct {
	url   string
	w, h  int
	frame [][]color.RGBA

	images   []image.Image
	delays   []time.Duration
	cur      int
	shown    time.Duration // how long cur has been up
	left     time.Duration // until the next fetch
	fetching bool
}

var _ bubble.Bubble = (*Tronbyt)(nil)

// New returns a Tronbyt bubble that pulls from url, a device's /next.
func New(url string) *Tronbyt { return &Tronbyt{url: url} }

func (t *Tronbyt) Name() string      { return "tronbyt" }
func (t *Tronbyt) Kind() bubble.Kind { return bubble.Idle }

// Frame is the current picture, rows top to bottom.
func (t *Tronbyt) Frame() [][]color.RGBA { return t.frame }

// Settings is trivial: what is shown is configured on the Tronbyt server.
func (t *Tronbyt) Settings() any { return struct{}{} }

func (t *Tronbyt) Configure(raw json.RawMessage) error {
	_, err := bubble.Patch(struct{}{}, raw)
	return err
}

func (t *Tronbyt) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		t.w, t.h = msg.W, msg.H
		t.frame = bubble.NewFrame(msg.W, msg.H)
		t.draw()
	case bubble.Activate:
		if len(t.images) == 0 || t.left <= 0 {
			return t.fetch()
		}
	case bubble.Tick:
		if len(t.images) > 1 {
			for t.shown += msg.Dt; t.shown >= t.delays[t.cur]; t.cur = (t.cur + 1) % len(t.images) {
				t.shown -= t.delays[t.cur]
			}
		}
		t.draw()
		if t.left -= msg.Dt; t.left <= 0 {
			return t.fetch()
		}
	case fetched:
		t.fetching = false
		if t.left = retry; msg.err == nil { // an error keeps the last picture
			t.images, t.delays, t.left = msg.images, msg.delays, msg.dwell
			t.cur, t.shown = 0, 0
		}
	}
	return nil
}

// fetch pulls the next image off the frame loop; nil while one is in flight.
func (t *Tronbyt) fetch() tea.Cmd {
	if t.fetching {
		return nil
	}
	t.fetching = true
	url := t.url
	return func() tea.Msg {
		f, err := get(url)
		f.err = err
		return f
	}
}

func get(url string) (fetched, error) {
	resp, err := client.Get(url)
	if err != nil {
		return fetched{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fetched{}, fmt.Errorf("tronbyt: %s", resp.Status)
	}
	anim, err := webp.DecodeAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fetched{}, err
	}
	if len(anim.Image) == 0 {
		return fetched{}, fmt.Errorf("tronbyt: an image without frames")
	}
	f := fetched{images: anim.Image, dwell: defaultDwell}
	if s, err := strconv.Atoi(resp.Header.Get("Tronbyt-Dwell-Secs")); err == nil && s >= 1 {
		f.dwell = time.Duration(s) * time.Second
	}
	for i := range anim.Image {
		d := defaultDelay
		if i < len(anim.Delay) && anim.Delay[i] > 0 {
			d = time.Duration(anim.Delay[i]) * time.Millisecond
		}
		f.delays = append(f.delays, d)
	}
	return f, nil
}

// draw copies the current image top-left into the frame, clipped, not
// scaled; before the first image it says so, so that a wrong URL shows.
func (t *Tronbyt) draw() {
	if t.w == 0 || t.h == 0 {
		return
	}
	for _, row := range t.frame {
		clear(row)
	}
	if len(t.images) == 0 {
		const s = "tronbyt?"
		bubble.DrawText(t.frame, (t.w-bubble.TextWidth(s, 1))/2, (t.h-7)/2, s, dim, 1)
		return
	}
	img := t.images[t.cur]
	b := img.Bounds().Intersect(image.Rect(0, 0, t.w, t.h).Add(img.Bounds().Min))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			t.frame[y-b.Min.Y][x-b.Min.X] = color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
		}
	}
}
