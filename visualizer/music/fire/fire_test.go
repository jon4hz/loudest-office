package fire

import (
	"strings"
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
)

func TestFireSeedsFromTheBandsAndClimbs(t *testing.T) {
	a := dsp.NewAnalyzer(1, 1, 44100, 256, 0, false)
	f := New()
	musictest.Sized(f, 4, 8)
	loud := musictest.Sine(256, 1000)
	top, bottom := false, false
	for range 30 {
		musictest.Feed(a, f, loud)
		bottom = bottom || strings.Contains(musictest.Column(f.Frame(), 1), "#")
		fr := f.Frame()
		top = top || fr[0][0].A != 0 || fr[0][1].A != 0 || fr[0][2].A != 0 || fr[0][3].A != 0
	}
	if !bottom || f.Frame()[7][1].A == 0 {
		t.Fatal("bottom row not burning")
	}
	if !top {
		t.Fatal("a full-level band never reached the top")
	}
	silence := make([]float32, 256)
	for range 40 {
		musictest.Feed(a, f, silence)
	}
	if strings.Contains(musictest.Column(f.Frame(), 1), "#") {
		t.Fatal("still burning after silence")
	}
	musictest.Sized(f, 3, 2) // resize must not panic
	a.SetBands(8)
	musictest.Feed(a, f, loud)
}

func TestFireIgnoresThePalette(t *testing.T) {
	f := New()
	if _, ok := f.Settings().(struct{}); !ok {
		t.Fatalf("Settings() = %T, want struct{}{}", f.Settings())
	}
	if err := f.Configure([]byte(`{"palettes":["outrun"]}`)); err == nil {
		t.Fatal("fire should reject any settings field")
	}
	if err := f.Configure([]byte(`{}`)); err != nil {
		t.Fatalf("fire should accept an empty object: %v", err)
	}
}
