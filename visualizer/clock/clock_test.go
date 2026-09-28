package clock

import (
	"image/color"
	"testing"
	"time"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

// lit counts the frame's non-transparent pixels.
func lit(frame [][]color.RGBA) int {
	n := 0
	for _, row := range frame {
		for _, px := range row {
			if px.A != 0 {
				n++
			}
		}
	}
	return n
}

// litColumns returns, per row, which x positions are lit, as a compact string.
func litColumns(frame [][]color.RGBA) []string {
	var out []string
	for _, row := range frame {
		s := make([]byte, len(row))
		for x, px := range row {
			s[x] = '.'
			if px.A != 0 {
				s[x] = '#'
			}
		}
		out = append(out, string(s))
	}
	return out
}

func framesEqual(a, b [][]color.RGBA) bool {
	if len(a) != len(b) {
		return false
	}
	for y := range a {
		if len(a[y]) != len(b[y]) {
			return false
		}
		for x := range a[y] {
			if a[y][x] != b[y][x] {
				return false
			}
		}
	}
	return true
}

func TestClockMatchesHandDrawnFrameOnEvenSecond(t *testing.T) {
	c := New()
	c.Update(bubble.Resize{W: 64, H: 32})
	now := time.Date(2024, 1, 1, 15, 4, 0, 0, time.UTC)
	c.Update(bubble.Tick{Now: now})

	want := bubble.NewFrame(64, 32)
	s := "15:04"
	scale := 2
	tw := bubble.TextWidth(s, scale)
	x := (64 - tw) / 2
	y := (32 - 7*scale) / 2
	bubble.DrawText(want, x, y, s, color.RGBA{153, 153, 153, 255}, scale)

	got := c.Frame()
	if !framesEqual(got, want) {
		t.Fatalf("frame mismatch at 15:04:00\ngot:  %v\nwant: %v", litColumns(got), litColumns(want))
	}
}

func TestClockHasFewerLitPixelsOnOddSecond(t *testing.T) {
	c := New()
	c.Update(bubble.Resize{W: 64, H: 32})

	c.Update(bubble.Tick{Now: time.Date(2024, 1, 1, 15, 4, 0, 0, time.UTC)})
	evenLit := lit(c.Frame())

	c.Update(bubble.Tick{Now: time.Date(2024, 1, 1, 15, 4, 1, 0, time.UTC)})
	oddLit := lit(c.Frame())

	if oddLit >= evenLit {
		t.Fatalf("odd-second lit pixels = %d, want fewer than even-second %d", oddLit, evenLit)
	}
}

func TestClockGuardsZeroSize(t *testing.T) {
	c := New()
	c.Update(bubble.Resize{W: 0, H: 0})
	c.Update(bubble.Tick{Now: time.Now()}) // must not panic
}

func TestClockNameKindSettings(t *testing.T) {
	c := New()
	if c.Name() != "clock" {
		t.Fatalf("Name() = %q, want clock", c.Name())
	}
	if c.Kind() != bubble.Idle {
		t.Fatalf("Kind() = %q, want idle", c.Kind())
	}
	if _, ok := c.Settings().(struct{}); !ok {
		t.Fatalf("Settings() = %T, want struct{}{}", c.Settings())
	}
	if err := c.Configure([]byte(`{"foo":1}`)); err == nil {
		t.Fatal("clock should reject any settings field")
	}
	if err := c.Configure([]byte(`{}`)); err != nil {
		t.Fatalf("clock should accept an empty object: %v", err)
	}
}
