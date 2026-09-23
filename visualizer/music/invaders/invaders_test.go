package invaders

import (
	"image/color"
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
	"github.com/jon4hz/loudest-office/visualizer/palette"
)

// 4 bands on a 16x16 frame: bands are 4 px wide, 2.15 kHz is band 2 (columns
// 8..10) and the only band with a level.
func loudBand2(t *testing.T) (*dsp.Analyzer, *Invaders) {
	t.Helper()
	a := dsp.NewAnalyzer(1, 4, 44100, 1024, 0, false)
	in := New()
	musictest.Sized(in, 16, 16)
	loud := musictest.Sine(1024, 2150)
	for range 50 {
		musictest.Feed(a, in, loud)
	}
	return a, in
}

func TestBarsHangFromTheTop(t *testing.T) {
	_, in := loudBand2(t)
	frame := in.Frame()
	if frame[0][8].A == 0 {
		t.Fatalf("loud band should paint from the top row:\n%v", musictest.LitColumns(frame))
	}
	if frame[0][0].A != 0 {
		t.Fatalf("silent band painted:\n%v", musictest.LitColumns(frame))
	}
	if frame[13][8].A != 0 { // rows 0..12 are the field, 13 the gap, 14..15 the ship
		t.Fatalf("bar must stay clear of the ship:\n%v", musictest.LitColumns(frame))
	}
}

func TestShipShootsThePeakBackToTheBar(t *testing.T) {
	a, in := loudBand2(t)
	if in.peaks[2] < 0.5 {
		t.Fatalf("peak of the loud band is %v, want it hanging low", in.peaks[2])
	}
	silence := make([]float32, 1024)
	shot := false
	for i := 0; i < 200 && in.peaks[2] > 0; i++ {
		musictest.Feed(a, in, silence)
		for _, s := range in.shots {
			shot = shot || s.band == 2
		}
	}
	if !shot {
		t.Fatal("ship never fired at the hanging peak")
	}
	if in.peaks[2] != 0 { // far faster than the creep: only a hit gets it there
		t.Fatalf("peak never shot back to the bar, still at %v", in.peaks[2])
	}
}

func TestShipPrioritisesTheHighestLevel(t *testing.T) {
	in := New()
	musictest.Sized(in, 16, 16)
	silent := bubble.Tick{Signal: dsp.Signal{Mix: make([]float32, 4)}, Dt: music.Step}
	in.Update(silent)
	in.peaks[0], in.peaks[3] = 0.5, 0.9
	in.ship = in.centre(1) // nearer the lower peak
	for i := 0; i < 200 && in.peaks[3] > 0; i++ {
		in.Update(silent)
		if in.peaks[0] < 0.4 {
			t.Fatal("the lower peak was shot first")
		}
	}
	for i := 0; i < 200 && in.peaks[0] > 0; i++ {
		in.Update(silent)
	}
	if in.peaks[3] != 0 || in.peaks[0] != 0 {
		t.Fatalf("peaks left hanging: %v", in.peaks)
	}
}

func TestShipShootsWhileMoving(t *testing.T) {
	in := New()
	musictest.Sized(in, 16, 16)
	silent := bubble.Tick{Signal: dsp.Signal{Mix: make([]float32, 4)}, Dt: music.Step}
	in.Update(silent)
	in.peaks[1], in.peaks[3] = 0.5, 0.9
	in.ship = in.centre(0)
	for i := 0; i < 200 && in.ship != in.centre(3); i++ {
		in.Update(silent)
	}
	if in.peaks[1] != 0 { // creep alone would leave it near 0.5
		t.Fatalf("passed under band 1 without shooting its peak, still at %v", in.peaks[1])
	}
}

func TestTinyFrameDoesNotPanic(t *testing.T) {
	in := New()
	a := musictest.Dropped(in)
	musictest.Sized(in, 3, 1)
	a.SetBands(2)
	musictest.Feed(a, in, make([]float32, 256))
	in.Update(bubble.Activate{})
	musictest.Feed(a, in, make([]float32, 256))
}

// board is a 4-band 16x16 game with one peak left hanging and kills hits
// already made; play runs silent steps until stop says so.
func board(kills int) (in *Invaders, play func(stop func() bool)) {
	in = New()
	musictest.Sized(in, 16, 16)
	silent := bubble.Tick{Signal: dsp.Signal{Mix: make([]float32, 4)}, Dt: music.Step}
	in.Update(silent)
	in.peaks[2], in.kills = 0.9, kills
	return in, func(stop func() bool) {
		for i := 0; i < 400 && !stop(); i++ {
			in.Update(silent)
		}
	}
}

func TestClearingTheBoardWins(t *testing.T) {
	in, play := board(3) // the last peak is the 4th hit on 4 bands
	play(func() bool { return in.win > 0 })
	if in.win == 0 {
		t.Fatal("shot the last peak, no win screen")
	}
}

func TestALoneHitDoesNotWin(t *testing.T) {
	in, play := board(0)
	play(func() bool { return in.peaks[2] == 0 })
	if in.peaks[2] != 0 || in.win != 0 {
		t.Fatalf("peak %v, win %d: want the peak shot and no win", in.peaks[2], in.win)
	}
}

func TestWinScreenShowsFireworksThenTheGameResumes(t *testing.T) {
	in, play := board(3)
	play(func() bool { return in.win > 0 })
	rockets := false // the text sits on rows 4..10 of 16, a fresh rocket below
	play(func() bool {
		rockets = rockets || musictest.Lit(in.Frame()[12:]) > 0
		return in.win == 0
	})
	if !rockets {
		t.Fatal("no fireworks on the win screen in silence")
	}
	if in.win != 0 || in.kills != 0 {
		t.Fatalf("win screen never ended: win %d, kills %d", in.win, in.kills)
	}
	if musictest.Lit(in.Frame()[15:]) != 3 {
		t.Fatalf("the ship should be back:\n%v", musictest.LitColumns(in.Frame()))
	}
}

// A relative palette fits its whole gradient into every bar, mirrored with
// it: fire's dark base on the top row, its white tip on the bar's low end.
func TestRelativePaletteIsMirroredWithTheBar(t *testing.T) {
	a, in := loudBand2(t)
	in.Palette, _ = palette.ByName("fire")
	for range 10 { // let the full bar fall back to a partial one
		musictest.Feed(a, in, make([]float32, 1024))
	}
	barH := int(in.bars[2]*13 + 0.5)
	if barH < 2 || barH > 12 {
		t.Fatalf("bar is %d rows, want it inside the 13-row field", barH)
	}
	frame := in.Frame()
	if base := (color.RGBA{128, 0, 0, 255}); frame[0][8] != base {
		t.Fatalf("top row is %v, want fire's base %v", frame[0][8], base)
	}
	if frame[barH-1][8] != palette.White {
		t.Fatalf("bar's low end is %v, want fire's white tip", frame[barH-1][8])
	}
}

// An upright palette is not mirrored: ukraine keeps its blue on top.
func TestUprightPaletteIsNotMirrored(t *testing.T) {
	a, in := loudBand2(t)
	in.Palette, _ = palette.ByName("ukraine")
	for range 10 {
		musictest.Feed(a, in, make([]float32, 1024))
	}
	barH := int(in.bars[2]*13 + 0.5)
	frame := in.Frame()
	blue, yellow := (color.RGBA{0, 87, 183, 255}), (color.RGBA{255, 213, 0, 255})
	if barH < 2 || frame[0][8] != blue || frame[barH-1][8] != yellow {
		t.Fatalf("bar of %d rows runs %v to %v, want blue on top, yellow below", barH, frame[0][8], frame[barH-1][8])
	}
}
