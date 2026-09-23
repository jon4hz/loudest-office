// Package clock is the idle bubble: HH:MM centred on the panel, blinking the
// colon every second so the digits never move.
package clock

import (
	"encoding/json"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

// dim is white at 60%, so an idle panel is not glaring.
var dim = color.RGBA{153, 153, 153, 255}

// Clock shows the time of day.
type Clock struct {
	w, h  int
	frame [][]color.RGBA
}

var _ bubble.Bubble = (*Clock)(nil)

// New returns a new Clock bubble.
func New() *Clock { return &Clock{} }

func (c *Clock) Name() string      { return "clock" }
func (c *Clock) Kind() bubble.Kind { return bubble.Idle }

// Frame is the current picture, rows top to bottom.
func (c *Clock) Frame() [][]color.RGBA { return c.frame }

// Settings is trivial: the clock has nothing to configure.
func (c *Clock) Settings() any { return struct{}{} }

func (c *Clock) Configure(raw json.RawMessage) error {
	_, err := bubble.Patch(struct{}{}, raw)
	return err
}

func (c *Clock) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		c.w, c.h = msg.W, msg.H
		c.frame = bubble.NewFrame(msg.W, msg.H)
	case bubble.Tick:
		c.draw(msg.Now)
	}
	return nil
}

// draw clears the frame and centres HH:MM, blanking the colon on odd
// seconds so the digits do not shift.
func (c *Clock) draw(now time.Time) {
	if c.w == 0 || c.h == 0 {
		return
	}
	for _, row := range c.frame {
		clear(row)
	}
	s := now.Format("15:04")
	if now.Second()%2 == 1 {
		s = strings.Replace(s, ":", " ", 1)
	}
	scale := 1
	if bubble.TextWidth(s, 2) <= c.w && c.h >= 14 {
		scale = 2
	}
	x := (c.w - bubble.TextWidth(s, scale)) / 2
	y := (c.h - 7*scale) / 2
	bubble.DrawText(c.frame, x, y, s, dim, scale)
}
