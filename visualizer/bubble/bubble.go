// Package bubble defines the interface every visualizer mode implements
// ("bubbles", peers behind one interface, tagged music/idle/event), the
// messages the controller sends them and the drawing helpers they share:
// frame allocation, the half-block terminal renderer and a generic JSON
// settings patcher. See font.go for the bitmap font and DrawText.
package bubble

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/dsp"
)

// Kind says what a bubble is for. The controller picks the active kind by
// priority (event > pin > interlude > music > idle > blank) and loops within
// it by weight.
type Kind string

const (
	Music     Kind = "music"
	Idle      Kind = "idle"
	EventKind Kind = "event"
	Track     Kind = "track" // in no loop: shown as an interlude or through the pin
)

// Bubble is the interface every mode implements.
type Bubble interface {
	Name() string
	Kind() Kind
	Update(tea.Msg) tea.Cmd
	Frame() [][]color.RGBA
	Settings() any
	Configure(json.RawMessage) error
}

// Tick drives one frame at the controller's fixed clock.
type Tick struct {
	Signal dsp.Signal
	Dt     time.Duration
	Now    time.Time
}

// Resize carries the panel size in pixels.
type Resize struct{ W, H int }

// Activate tells the newly active bubble to choose its own variation.
type Activate struct{}

// Event is a queued alert.
type Event struct {
	ID      string    `json:"id"`
	Text    string    `json:"text"`
	Level   string    `json:"level"`
	Expires time.Time `json:"expires"`
}

// Levels ranks event severity; the index is the rank.
var Levels = []string{"info", "warning", "critical"}

// Interlude is a bubble's request to the controller: show the bubble called
// Name for this long, then carry on. The controller ignores it under a pin
// and from a bubble of weight 0.
type Interlude struct {
	Name string
	For  time.Duration
}

// NewFrame allocates a w x h frame, rows top to bottom, every pixel off.
func NewFrame(w, h int) [][]color.RGBA {
	frame := make([][]color.RGBA, h)
	for y := range frame {
		frame[y] = make([]color.RGBA, w)
	}
	return frame
}

// Fit scales frame to fill w x h pixels, keeping its aspect ratio: nearest
// neighbour, by a whole factor when growing so pixels stay crisp. An empty
// frame or a zero box comes back as is.
func Fit(frame [][]color.RGBA, w, h int) [][]color.RGBA {
	fh := len(frame)
	if fh == 0 || len(frame[0]) == 0 || w <= 0 || h <= 0 {
		return frame
	}
	fw := len(frame[0])
	s := min(float64(w)/float64(fw), float64(h)/float64(fh))
	if s >= 1 {
		s = math.Floor(s)
	}
	ow, oh := max(1, int(float64(fw)*s)), max(1, int(float64(fh)*s))
	out := NewFrame(ow, oh)
	for y := range out {
		src := frame[y*fh/oh]
		for x := range out[y] {
			out[y][x] = src[x*fw/ow]
		}
	}
	return out
}

// Render paints frame two pixels per cell using the upper half block:
// foreground is the upper pixel, background the lower one.
func Render(frame [][]color.RGBA) string {
	h := len(frame)
	var w int
	if h > 0 {
		w = len(frame[0])
	}
	var sb strings.Builder
	for y := 0; y < h; y += 2 {
		var prevFg, prevBg color.RGBA
		first := true
		for x := 0; x < w; x++ {
			fg := frame[y][x]
			var bg color.RGBA
			if y+1 < h {
				bg = frame[y+1][x]
			}
			if first || fg != prevFg {
				fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm", fg.R, fg.G, fg.B)
			}
			if first || bg != prevBg {
				fmt.Fprintf(&sb, "\x1b[48;2;%d;%d;%dm", bg.R, bg.G, bg.B)
			}
			sb.WriteString("▀")
			prevFg, prevBg, first = fg, bg, false
		}
		sb.WriteString("\x1b[0m")
		if y+2 < h {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// Patch decodes raw onto a deep copy of cur and returns it, so a failed
// patch never touches the caller's live settings. Unknown fields in raw are
// an error. Empty or null raw returns the deep copy unchanged.
func Patch[T any](cur T, raw json.RawMessage) (T, error) {
	var zero T
	buf, err := json.Marshal(cur)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(buf, &out); err != nil {
		return zero, err
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return out, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return zero, err
	}
	if dec.More() {
		return zero, fmt.Errorf("unexpected data after JSON value")
	}
	return out, nil
}
