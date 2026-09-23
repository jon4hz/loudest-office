// Package alert is the event bubble: it scrolls the last queued event's text
// right to left, coloured by level. The controller owns expiry and
// queueing; alert only draws what the last bubble.Event told it.
package alert

import (
	"encoding/json"
	"fmt"
	"image/color"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

// colours maps a level to its text colour; an unknown level draws as info.
var colours = map[string]color.RGBA{
	"info":     {0, 160, 255, 255},
	"warning":  {255, 160, 0, 255},
	"critical": {255, 0, 0, 255},
}

// Settings is the scroll speed, in pixels per second.
type Settings struct {
	Speed float64 `json:"speed"`
}

// Alert scrolls the current event's text across the panel.
type Alert struct {
	w, h   int
	frame  [][]color.RGBA
	text   string
	level  string
	offset float64
	set    Settings
}

var _ bubble.Bubble = (*Alert)(nil)

// New returns a new Alert bubble at the default 30 px/s.
func New() *Alert { return &Alert{set: Settings{Speed: 30}} }

func (a *Alert) Name() string      { return "alert" }
func (a *Alert) Kind() bubble.Kind { return bubble.EventKind }

// Frame is the current picture, rows top to bottom.
func (a *Alert) Frame() [][]color.RGBA { return a.frame }

func (a *Alert) Settings() any { return a.set }

// Configure patches the scroll speed, rejecting anything not > 0.
func (a *Alert) Configure(raw json.RawMessage) error {
	out, err := bubble.Patch(a.set, raw)
	if err != nil {
		return err
	}
	if out.Speed <= 0 {
		return fmt.Errorf("speed must be > 0, got %v", out.Speed)
	}
	a.set = out
	return nil
}

func (a *Alert) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		a.w, a.h = msg.W, msg.H
		a.frame = bubble.NewFrame(msg.W, msg.H)
	case bubble.Event:
		if msg.Text != a.text {
			a.offset = 0
		}
		a.text, a.level = msg.Text, msg.Level
	case bubble.Tick:
		a.draw(msg.Dt)
	}
	return nil
}

// draw clears the frame, advances the scroll when the text does not fit and
// paints it. A text that fits stays centred and does not scroll.
func (a *Alert) draw(dt time.Duration) {
	if a.w == 0 || a.h == 0 {
		return
	}
	for _, row := range a.frame {
		clear(row)
	}
	scale := 1
	if a.h >= 14 {
		scale = 2
	}
	tw := bubble.TextWidth(a.text, scale)
	x := (a.w - tw) / 2
	if tw > a.w {
		a.offset += a.set.Speed * dt.Seconds()
		if a.offset > float64(a.w+tw) {
			a.offset = 0
		}
		x = a.w - int(a.offset)
	}
	y := (a.h - 7*scale) / 2
	c, ok := colours[a.level]
	if !ok {
		c = colours["info"]
	}
	bubble.DrawText(a.frame, x, y, a.text, c, scale)
}
