package alert

import (
	"image/color"
	"testing"
	"time"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

// leftmostLit returns the leftmost lit column across every row, or -1 if the
// frame is blank.
func leftmostLit(frame [][]color.RGBA) int {
	if len(frame) == 0 {
		return -1
	}
	for x := 0; x < len(frame[0]); x++ {
		for _, row := range frame {
			if row[x].A != 0 {
				return x
			}
		}
	}
	return -1
}

// firstLitColour returns the colour of the first lit pixel found scanning
// top to bottom, left to right.
func firstLitColour(frame [][]color.RGBA) color.RGBA {
	for _, row := range frame {
		for _, px := range row {
			if px.A != 0 {
				return px
			}
		}
	}
	return color.RGBA{}
}

func longText() string { return "THIS IS A LONG ALERT MESSAGE THAT SCROLLS" }

func TestAlertScrollsLeftAtConfiguredSpeed(t *testing.T) {
	a := New()
	a.Update(bubble.Resize{W: 64, H: 32})
	a.Update(bubble.Event{ID: "1", Text: longText(), Level: "info"})

	a.Update(bubble.Tick{Dt: time.Second})
	first := leftmostLit(a.Frame())
	a.Update(bubble.Tick{Dt: time.Second})
	second := leftmostLit(a.Frame())

	if first-second != 30 {
		t.Fatalf("leftmost lit column moved by %d, want 30 (first=%d, second=%d)", first-second, first, second)
	}
}

func TestAlertWrapsAfterScrollingOff(t *testing.T) {
	a := New()
	a.Update(bubble.Resize{W: 64, H: 32})
	a.Update(bubble.Event{Text: longText(), Level: "info"})

	tw := bubble.TextWidth(longText(), 2)
	maxOffset := float64(64 + tw)
	n := int(maxOffset/30) + 1 // first tick where 30*n > maxOffset

	for i := 0; i < n; i++ {
		a.Update(bubble.Tick{Dt: time.Second})
	}
	if a.offset != 0 {
		t.Fatalf("offset = %v after %d ticks, want 0 (wrapped back to the right edge)", a.offset, n)
	}
}

func TestAlertEventRestartsOffsetOnlyWhenTextChanges(t *testing.T) {
	a := New()
	a.Update(bubble.Resize{W: 64, H: 32})
	a.Update(bubble.Event{Text: longText(), Level: "info"})
	a.Update(bubble.Tick{Dt: time.Second})

	if a.offset == 0 {
		t.Fatal("offset did not advance for scrolling text")
	}
	before := a.offset

	a.Update(bubble.Event{Text: longText(), Level: "info"}) // same text
	if a.offset != before {
		t.Fatalf("same text reset the offset: got %v, want %v", a.offset, before)
	}

	a.Update(bubble.Event{Text: "SOMETHING COMPLETELY DIFFERENT HERE TOO", Level: "warning"})
	if a.offset != 0 {
		t.Fatalf("new text did not restart at the right edge: offset = %v", a.offset)
	}
}

func TestAlertShortTextIsCenteredAndDoesNotScroll(t *testing.T) {
	a := New()
	a.Update(bubble.Resize{W: 64, H: 32})
	a.Update(bubble.Event{Text: "HI", Level: "info"})

	for range 5 {
		a.Update(bubble.Tick{Dt: time.Second})
		if a.offset != 0 {
			t.Fatalf("offset = %v, want 0 for text that fits", a.offset)
		}
	}
}

func TestAlertColoursByLevel(t *testing.T) {
	cases := map[string]color.RGBA{
		"info":     {0, 160, 255, 255},
		"warning":  {255, 160, 0, 255},
		"critical": {255, 0, 0, 255},
	}
	for level, want := range cases {
		a := New()
		a.Update(bubble.Resize{W: 64, H: 32})
		a.Update(bubble.Event{Text: "HI", Level: level})
		a.Update(bubble.Tick{Dt: 0})
		if got := firstLitColour(a.Frame()); got != want {
			t.Fatalf("%s: colour = %v, want %v", level, got, want)
		}
	}
}

func TestAlertUnknownLevelDrawsInfoColour(t *testing.T) {
	a := New()
	a.Update(bubble.Resize{W: 64, H: 32})
	a.Update(bubble.Event{Text: "HI", Level: "bogus"})
	a.Update(bubble.Tick{Dt: 0})

	want := color.RGBA{0, 160, 255, 255}
	if got := firstLitColour(a.Frame()); got != want {
		t.Fatalf("colour = %v, want info %v", got, want)
	}
}

func TestAlertGuardsZeroSize(t *testing.T) {
	a := New()
	a.Update(bubble.Resize{W: 0, H: 0})
	a.Update(bubble.Event{Text: "HI", Level: "info"})
	a.Update(bubble.Tick{Dt: time.Second}) // must not panic
}

func TestAlertConfigureRejectsNonPositiveSpeed(t *testing.T) {
	a := New()
	if a.set.Speed != 30 {
		t.Fatalf("default speed = %v, want 30", a.set.Speed)
	}
	if err := a.Configure([]byte(`{"speed":0}`)); err == nil {
		t.Fatal("speed 0 accepted")
	}
	if err := a.Configure([]byte(`{"speed":-5}`)); err == nil {
		t.Fatal("negative speed accepted")
	}
	if a.set.Speed != 30 {
		t.Fatalf("speed changed by a rejected patch: %v", a.set.Speed)
	}
	if err := a.Configure([]byte(`{"speed":10}`)); err != nil {
		t.Fatalf("valid speed rejected: %v", err)
	}
	if a.set.Speed != 10 {
		t.Fatalf("speed = %v, want 10", a.set.Speed)
	}
}

func TestAlertNameKindSettings(t *testing.T) {
	a := New()
	if a.Name() != "alert" {
		t.Fatalf("Name() = %q, want alert", a.Name())
	}
	if a.Kind() != bubble.EventKind {
		t.Fatalf("Kind() = %q, want event", a.Kind())
	}
	if _, ok := a.Settings().(Settings); !ok {
		t.Fatalf("Settings() = %T, want Settings", a.Settings())
	}
}
