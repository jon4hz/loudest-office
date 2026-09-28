package life

import (
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
	"github.com/jon4hz/loudest-office/visualizer/palette"
)

func TestLifeBlinkerRotates(t *testing.T) {
	l := New()
	musictest.Sized(l, 5, 5)
	l.stepLife()                                                // allocates the grid
	l.life[2][1], l.life[2][2], l.life[2][3] = true, true, true // horizontal blinker
	l.stepLife()
	for y := range 5 {
		for x := range 5 {
			want := x == 2 && y >= 1 && y <= 3 // vertical now
			if l.life[y][x] != want {
				t.Fatalf("cell %d,%d alive=%v", x, y, l.life[y][x])
			}
		}
	}
	l.stepLife()
	if !l.life[2][1] || !l.life[2][3] || l.life[1][2] {
		t.Fatal("blinker did not rotate back")
	}
}

func TestLifeWrapsAtTheEdges(t *testing.T) {
	l := New()
	musictest.Sized(l, 5, 5)
	l.stepLife()
	l.life[0][0], l.life[0][1], l.life[0][4] = true, true, true // a blinker across the left edge
	l.stepLife()
	if !l.life[4][0] || !l.life[1][0] || !l.life[0][0] {
		t.Fatal("blinker across the seam did not become vertical")
	}
}

func TestLifeIsSeededByTheSpectrum(t *testing.T) {
	// 4 bands, 16 columns: 2.15 kHz is band 2, columns 8..11.
	a := dsp.NewAnalyzer(1, 4, 44100, 1024, 0, false)
	l := New()
	musictest.Sized(l, 16, 8)
	loudSeen := false
	loud := musictest.Sine(1024, 2150)
	for range 40 {
		musictest.Feed(a, l, loud)
		fr := l.Frame()
		for x := 0; x < 16; x++ {
			if fr[7][x].A != 0 && x >= 8 && x < 12 {
				loudSeen = true
			}
		}
	}
	if !loudSeen {
		t.Fatal("the loud band never seeded its columns")
	}

	a = dsp.NewAnalyzer(1, 4, 44100, 1024, 0, false)
	l = New()
	musictest.Sized(l, 16, 8)
	silence := make([]float32, 1024)
	for range 40 {
		musictest.Feed(a, l, silence)
		for y, row := range l.Frame() {
			for x, px := range row {
				if px.A != 0 {
					t.Fatalf("silence seeded a cell at %d,%d", x, y)
				}
			}
		}
	}
	musictest.Sized(l, 3, 1) // resize must not panic
	a.SetBands(8)
	musictest.Feed(a, l, silence)
}

func TestLifeUsesPeakColourWhenBarsAreHidden(t *testing.T) {
	p, _ := palette.ByName("peaks") // bars hidden, peaks blue
	l := New()
	musictest.Sized(l, 3, 3)
	l.Bands = 1
	l.Palette = p
	l.stepLife()
	l.life[1][1] = true
	l.draw()
	if l.Frame()[1][1] != palette.Blue {
		t.Fatalf("cell should take the peak colour, got %v", l.Frame()[1][1])
	}
}

func TestLifeSurvivesShrinkingResize(t *testing.T) {
	a := dsp.NewAnalyzer(2, 32, 44100, 1024, 0, false)
	l := New()
	musictest.Sized(l, 40, 20)
	blk := musictest.Sine(1024, 440)
	a.Add([][]float32{blk, blk})
	l.Update(bubble.Tick{Signal: a.Take(), Dt: music.Step})
	l.life[l.H-1][l.W-1] = true // a cell in the far corner of the old grid
	musictest.Sized(l, 20, 10)  // must not panic
	a.Add([][]float32{blk, blk})
	l.Update(bubble.Tick{Signal: a.Take(), Dt: music.Step})
	if len(l.life) != 10 || len(l.life[0]) != 20 {
		t.Fatalf("life grid %dx%d after resize", len(l.life[0]), len(l.life))
	}
}

func TestConfigureRejectsUnknownPalettesAndPicksOnlyTheConfiguredOne(t *testing.T) {
	l := New()
	if err := l.Configure([]byte(`{"palettes":["nope"]}`)); err == nil {
		t.Fatal("unknown palette accepted")
	}
	if err := l.Configure([]byte(`{"palettes":["outrun"]}`)); err != nil {
		t.Fatalf("known palette rejected: %v", err)
	}
	for range 200 {
		l.Update(bubble.Activate{})
		if l.Palette.Name != "outrun" {
			t.Fatalf("picked %q, want only outrun", l.Palette.Name)
		}
	}
}
