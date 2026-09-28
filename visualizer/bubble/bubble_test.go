package bubble

import (
	"encoding/json"
	"image/color"
	"reflect"
	"strings"
	"testing"
)

func TestRenderNewlineCount(t *testing.T) {
	frame := NewFrame(40, 20)
	if lines := strings.Count(Render(frame), "\n"); lines != 9 {
		t.Fatalf("got %d newlines, want 9", lines)
	}
}

func TestNewFrameAllOff(t *testing.T) {
	frame := NewFrame(3, 2)
	if len(frame) != 2 || len(frame[0]) != 3 {
		t.Fatalf("frame %dx%d, want 3x2", len(frame[0]), len(frame))
	}
	for _, row := range frame {
		for _, px := range row {
			if px != (color.RGBA{}) {
				t.Fatalf("pixel not zero: %v", px)
			}
		}
	}
}

type patchTarget struct {
	L []string `json:"l"`
}

func TestPatchDeepCopiesAndRejectsUnknownFields(t *testing.T) {
	cur := patchTarget{L: []string{"a", "b"}}

	patched, err := Patch(cur, json.RawMessage(`{"l":["zzz"]}`))
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !reflect.DeepEqual(patched.L, []string{"zzz"}) {
		t.Fatalf("patched.L = %v, want [zzz]", patched.L)
	}
	if !reflect.DeepEqual(cur.L, []string{"a", "b"}) {
		t.Fatalf("original mutated: cur.L = %v, want [a b]", cur.L)
	}

	if _, err := Patch(cur, json.RawMessage(`{"nope":1}`)); err == nil {
		t.Fatal("unknown field should error")
	}

	for _, raw := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("null")} {
		out, err := Patch(cur, raw)
		if err != nil {
			t.Fatalf("empty/null raw: %v", err)
		}
		if !reflect.DeepEqual(out.L, cur.L) {
			t.Fatalf("empty/null raw: got %v, want %v", out.L, cur.L)
		}
	}
}

func TestPatchRejectsTrailingDataButAcceptsTrailingWhitespace(t *testing.T) {
	cur := patchTarget{L: []string{"a", "b"}}

	if _, err := Patch(cur, json.RawMessage(`{"l":["zzz"]} x`)); err == nil {
		t.Fatal("trailing garbage after the JSON value should error")
	}
	if !reflect.DeepEqual(cur.L, []string{"a", "b"}) {
		t.Fatalf("cur.L = %v after a rejected patch, want unchanged [a b]", cur.L)
	}

	out, err := Patch(cur, json.RawMessage("{\"l\":[\"zzz\"]}\n\t \n"))
	if err != nil {
		t.Fatalf("trailing whitespace should be accepted: %v", err)
	}
	if !reflect.DeepEqual(out.L, []string{"zzz"}) {
		t.Fatalf("out.L = %v, want [zzz]", out.L)
	}
}

// litPixels returns the set of coordinates with a non-zero alpha pixel.
func litPixels(frame [][]color.RGBA) map[[2]int]bool {
	set := map[[2]int]bool{}
	for y, row := range frame {
		for x, px := range row {
			if px.A != 0 {
				set[[2]int{x, y}] = true
			}
		}
	}
	return set
}

func TestDrawTextLightsExactGlyphPixels(t *testing.T) {
	frame := NewFrame(10, 10)
	red := color.RGBA{255, 0, 0, 255}
	DrawText(frame, 0, 0, "1", red, 1)

	want := map[[2]int]bool{}
	for _, xy := range [][2]int{{2, 0}, {1, 1}, {2, 1}, {2, 2}, {2, 3}, {2, 4}, {2, 5}, {1, 6}, {2, 6}, {3, 6}} {
		want[xy] = true
	}
	if got := litPixels(frame); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDrawTextDoesNotPanicAtEdgesOrUnknownRunes(t *testing.T) {
	frame := NewFrame(10, 10)
	c := color.RGBA{255, 255, 255, 255}
	DrawText(frame, -3, 0, "A", c, 1) // off the left edge
	DrawText(frame, 8, 0, "A", c, 1)  // runs past the right edge
	DrawText(frame, 0, 0, "é", c, 1)  // unknown rune ('é')
}

func TestDrawTextScaleMultipliesLitPixels(t *testing.T) {
	c := color.RGBA{255, 255, 255, 255}
	f1 := NewFrame(10, 10)
	DrawText(f1, 0, 0, "1", c, 1)
	f2 := NewFrame(20, 20)
	DrawText(f2, 0, 0, "1", c, 2)
	n1, n2 := len(litPixels(f1)), len(litPixels(f2))
	if n2 != 4*n1 {
		t.Fatalf("scale 2 lit %d pixels, want %d (4x scale 1's %d)", n2, 4*n1, n1)
	}
}

func TestTextWidth(t *testing.T) {
	if w := TextWidth("AB", 1); w != 11 {
		t.Fatalf("TextWidth(AB, 1) = %d, want 11", w)
	}
	if w := TextWidth("", 1); w != 0 {
		t.Fatalf("TextWidth(\"\", 1) = %d, want 0", w)
	}
}

func TestDrawTextFoldsDiacritics(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	got, want := NewFrame(90, 7), NewFrame(90, 7)
	DrawText(got, 0, 0, "Züri Wést ßø’", white, 1)
	DrawText(want, 0, 0, "Zuri West SSo'", white, 1)
	if !reflect.DeepEqual(got, want) {
		t.Error("folded text draws other pixels than its ASCII spelling")
	}
	if got, want := TextWidth("ß", 1), TextWidth("SS", 1); got != want {
		t.Errorf("TextWidth(ß) = %d, want %d", got, want)
	}
}

func TestFit(t *testing.T) {
	frame := [][]color.RGBA{{{1, 0, 0, 255}, {0, 2, 0, 255}}} // 2x1
	if out := Fit(frame, 7, 3); len(out) != 3 || len(out[0]) != 6 || out[2][2] != frame[0][0] || out[0][3] != frame[0][1] {
		t.Fatalf("up: %dx%d", len(out[0]), len(out))
	}
	if out := Fit(frame, 1, 1); len(out) != 1 || len(out[0]) != 1 {
		t.Fatalf("down: %dx%d", len(out[0]), len(out))
	}
	if out := Fit(nil, 5, 5); out != nil {
		t.Fatal("empty frame changed")
	}
}
