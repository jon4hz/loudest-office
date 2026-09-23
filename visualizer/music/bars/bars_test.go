package bars

import (
	"encoding/json"
	"image/color"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/dsp"
	"github.com/jon4hz/loudest-office/visualizer/music/musictest"
	"github.com/jon4hz/loudest-office/visualizer/palette"
)

// newBars returns a Bars sized sources x bands, allocated and framed at
// w x h, bypassing Activate so tests can poke b.bars directly and call
// b.draw() without waiting for a Tick to discover the shape.
func newBars(sources, bands, w, h int) *Bars {
	b := New()
	b.sources, b.Bands = sources, bands
	b.Resize(w, h)
	b.alloc()
	return b
}

func TestBarsDrawsAndHoldsPeaks(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	b := newBars(1, 8, 16, 8)
	b.peakHold, b.peakFall, b.fall = 2, 0.5, 1
	musictest.Feed(a, b, musictest.Sine(256, 1000))
	f := b.Frame()
	if len(f) != 8 || len(f[0]) != 16 {
		t.Fatalf("frame %dx%d", len(f[0]), len(f))
	}
	if musictest.Lit(f) == 0 {
		t.Fatal("nothing drawn")
	}
	peakBefore := append([]float32(nil), b.peaks[0]...)
	silence := make([]float32, 256)
	musictest.Feed(a, b, silence) // bars drop, hold 2
	musictest.Feed(a, b, silence) // hold 1
	for i := range peakBefore {
		if b.peaks[0][i] != peakBefore[i] {
			t.Fatalf("peak %d moved during hold", i)
		}
	}
	musictest.Feed(a, b, silence) // hold 0
	musictest.Feed(a, b, silence) // falls
	moved := false
	for i := range peakBefore {
		if b.peaks[0][i] < peakBefore[i] {
			moved = true
		}
	}
	if !moved {
		t.Fatal("peak never fell")
	}
}

func TestBarsLayouts(t *testing.T) {
	for _, tc := range []struct {
		layout    Layout
		top, last string // first and last frame row with ch0 band0 and ch1 band0 full
	}{
		{Stacked, "###.............", "............###."},
		{SideBySide, "##......##......", "##......##......"},
		{Mirrored, "......####......", "......####......"},
	} {
		b := newBars(2, 4, 16, 8)
		b.layout = tc.layout
		b.bars[0][0], b.bars[1][0] = 1, 1
		if tc.layout == Stacked {
			b.bars[1][0], b.bars[1][3] = 0, 0.25 // one pixel of ch1's top band, bottom right
		}
		b.draw()
		rows := musictest.LitColumns(b.Frame())
		if rows[0] != tc.top || rows[7] != tc.last {
			t.Errorf("%v:\n%s", tc.layout, strings.Join(rows, "\n"))
		}
	}
}

func TestBarsHMirrorMeetsAtCentreLine(t *testing.T) {
	b := newBars(2, 4, 16, 8)
	b.layout = HMirrored
	b.bars[0][0], b.bars[1][0] = 0.5, 0.5 // ch0 grows up from the centre, ch1 down
	b.draw()
	want := []string{
		"................",
		"................",
		"###.............",
		"###.............",
		"###.............",
		"###.............",
		"................",
		"................",
	}
	if got := musictest.LitColumns(b.Frame()); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s", strings.Join(got, "\n"))
	}
}

// TestBarsStereoMirrorAndHMirrorUnchanged is a characterization test: it was
// written and confirmed passing against bars.go before the mono/stereo
// pane-vs-source rework, with non-symmetric per-source band values (not the
// 0.5/0.5 of TestBarsHMirrorMeetsAtCentreLine) so a wrong flip could not pass
// by accident. Stereo must render exactly as it did before the rework.
func TestBarsStereoMirrorAndHMirrorUnchanged(t *testing.T) {
	b := newBars(2, 4, 16, 8)
	b.layout = Mirrored
	b.bars[0][0], b.bars[1][3] = 1, 1 // ch0 band0, ch1 band3: no symmetry between channels
	b.draw()
	want := "......##......##"
	for _, row := range musictest.LitColumns(b.Frame()) {
		if row != want {
			t.Fatalf("mirrored: got %q, want %q", row, want)
		}
	}

	b = newBars(2, 4, 16, 8)
	b.layout = HMirrored
	b.bars[0][0], b.bars[1][3] = 1, 1
	b.draw()
	rows := musictest.LitColumns(b.Frame())
	wantTop, wantBottom := "###.............", "............###."
	for _, row := range rows[:4] {
		if row != wantTop {
			t.Fatalf("hmirrored top: got %q, want %q", row, wantTop)
		}
	}
	for _, row := range rows[4:] {
		if row != wantBottom {
			t.Fatalf("hmirrored bottom: got %q, want %q", row, wantBottom)
		}
	}
}

func TestBarsSideLayoutsMeetAtCentre(t *testing.T) {
	// 3 bands of width 2 in an 8-wide half leave 2 px over; they must not end up in the middle.
	for _, tc := range []struct {
		layout Layout
		want   string
	}{
		{Mirrored, "......####......"},
		{SideBySide, "..##....##......"},
	} {
		b := newBars(2, 3, 16, 8)
		b.layout = tc.layout
		b.bars[0][0], b.bars[1][0] = 1, 1
		b.draw()
		if got := musictest.LitColumns(b.Frame())[0]; got != tc.want {
			t.Errorf("%v: got %q", tc.layout, got)
		}
	}
}

func TestBarsRelativePaletteScalesToBar(t *testing.T) {
	p, _ := palette.ByName("bi pride")
	b := newBars(1, 1, 1, 10)
	b.Palette = p
	b.bars[0][0] = 0.5 // 5 px tall: the whole flag must fit in those 5 rows
	b.draw()
	f := b.Frame()
	magenta, purple, blue := color.RGBA{0xD6, 0x02, 0x70, 255}, color.RGBA{0x9B, 0x4F, 0x96, 255}, color.RGBA{0x00, 0x38, 0xA8, 255}
	want := []color.RGBA{magenta, magenta, purple, blue, blue} // bottom to top
	for y, w := range want {
		if got := f[9-y][0]; got != w {
			t.Fatalf("row %d from bottom: got %v want %v", y, got, w)
		}
	}
	if f[4][0].A != 0 {
		t.Fatal("row above the bar should be off")
	}
}

func TestBarsDriftRollsColoursAcrossBands(t *testing.T) {
	p, ok := palette.ByName("drift")
	if !ok || !p.Animate {
		t.Fatal("drift should exist and be animated")
	}
	b := newBars(1, 4, 4, 2)
	b.Palette = p
	for i := range b.bars[0] {
		b.bars[0][i] = 1
	}
	b.draw()
	before := append([]color.RGBA(nil), b.Frame()[1]...)
	b.Steps = 8 // one step
	b.draw()
	after := b.Frame()[1]
	if after[0] != before[1] || after[3] != before[0] {
		t.Fatalf("colours did not roll by one band:\n%v\n%v", before, after)
	}
}

func TestBarsEveryPaletteDrawsInEveryLayout(t *testing.T) {
	for _, p := range palette.Palettes {
		for l := Stacked; l < Layout(len(Layouts)); l++ {
			b := newBars(2, 4, 16, 8)
			b.layout, b.Palette = l, p
			for ch := range b.bars {
				for i := range b.bars[ch] {
					b.bars[ch][i], b.peaks[ch][i] = 0.5, 1.5 // a flying peak past the top
				}
			}
			b.draw() // must not panic
		}
	}
}

func TestBarsFlyingPeaksLaunchAndRearm(t *testing.T) {
	a := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
	b := newBars(1, 8, 16, 8)
	b.peakHold, b.fall, b.peakStyle = 1, 1, Flying
	musictest.Feed(a, b, musictest.Sine(256, 1000))
	loud := 0
	for i, p := range b.peaks[0] {
		if p > b.peaks[0][loud] {
			loud = i
		}
	}
	start := b.peaks[0][loud]
	silence := make([]float32, 256)
	flew, rearmed := false, false
	for i := 0; i < 200 && !rearmed; i++ {
		musictest.Feed(a, b, silence)
		p := b.peaks[0][loud]
		if p > 1 {
			flew = true
		}
		if flew && p == 0 {
			rearmed = true
		}
		if !flew && p < start {
			t.Fatalf("block %d: flying peak fell to %v", i, p)
		}
		b.draw() // peaks above the frame must not panic
	}
	if !flew || !rearmed {
		t.Fatalf("flew=%v rearmed=%v", flew, rearmed)
	}
	if b.peakStyle != Flying {
		t.Fatal("peakStyle field")
	}
	if s, ok := ParsePeakStyle("none"); !ok || s != NoPeaks {
		t.Fatal("ParsePeakStyle")
	}
}

func TestBarsBeatPeaksFlyOnADrop(t *testing.T) {
	quiet := musictest.Sine(256, 1000)
	for i := range quiet {
		quiet[i] *= 0.32 // a plain hit (measured 1.63x the average), between the hit and scatter lines
	}
	loud := musictest.Sine(256, 1000)
	silence := make([]float32, 256)
	run := func(style PeakStyle) bool {
		a := dsp.NewAnalyzer(1, 8, 44100, 256, 0, false)
		b := newBars(1, 8, 16, 8)
		b.peakHold, b.fall, b.peakStyle = 5, 0.05, style
		for range 300 { // settle the ~2 s running energy average on a quiet passage
			musictest.Feed(a, b, quiet)
		}
		musictest.Feed(a, b, loud) // the drop
		for range 60 {
			musictest.Feed(a, b, silence)
			for _, p := range b.peaks[0] {
				if p > 1 {
					return true
				}
			}
		}
		return false
	}
	if !run(Beat) {
		t.Fatal("beat style: peaks did not fly after the drop")
	}
	if run(Falling) {
		t.Fatal("falling style: peaks must never fly")
	}
	if s, ok := ParsePeakStyle("beat"); !ok || s != Beat {
		t.Fatal("ParsePeakStyle beat")
	}
}

func TestBarsBeatChaosOnABigDrop(t *testing.T) {
	a := dsp.NewAnalyzer(1, 32, 44100, 256, 0, false)
	b := newBars(1, 32, 64, 8) // 32 bands: all picking "up" is a 1 in 3 million chance
	b.peakHold, b.fall, b.peakStyle = 5, 0.05, Beat
	for range 300 {
		musictest.Feed(a, b, musictest.Noise(256, 0.05))
	}
	musictest.Feed(a, b, musictest.Noise(256, 1)) // a big drop: well over 1.7x the average
	up, down, drifted := false, false, false
	silence := make([]float32, 256)
	for range 40 {
		musictest.Feed(a, b, silence)
		for i := range b.peaks[0] {
			up = up || b.peaks[0][i] > 1
			down = down || b.peaks[0][i] < 0
			drifted = drifted || b.px[0][i] != 0
		}
		b.draw() // peaks off-screen or in other columns must not panic
	}
	if !up || !down || !drifted {
		t.Fatalf("chaos burst: up=%v down=%v drifted=%v", up, down, drifted)
	}
}

func TestBarsPeakNeverDrawnInsideABar(t *testing.T) {
	p, _ := palette.ByName("white") // white bars, red peak
	b := newBars(1, 2, 2, 10)
	b.Palette = p
	// band 0: peak below its own bar; band 1: a tall bar that band 0's peak drifts into
	b.bars[0][0], b.peaks[0][0] = 0.8, 0.3
	b.bars[0][1] = 0.9
	b.draw()
	for y, row := range b.Frame() {
		if row[0] == palette.Red || row[1] == palette.Red {
			t.Fatalf("row %d: peak drawn inside a bar: %v", y, row)
		}
	}
	b.px[0][0] = 1 // drift into column 1, still below that bar's top
	b.draw()
	for y, row := range b.Frame() {
		if row[1] == palette.Red {
			t.Fatalf("row %d: drifted peak drawn inside the neighbouring bar", y)
		}
	}
	b.peaks[0][0], b.px[0][0] = 0.95, 0 // above its own bar: must show
	b.draw()
	if b.Frame()[0][0] != palette.Red {
		t.Fatal("peak above the bar should be drawn")
	}
}

func TestBarsTrailsFadeInsteadOfClearing(t *testing.T) {
	p, _ := palette.ByName("white")
	b := newBars(1, 1, 1, 4)
	b.Palette, b.trails, b.peakStyle = p, true, NoPeaks
	b.bars[0][0] = 1
	b.draw()
	b.bars[0][0] = 0
	b.draw()
	px := b.Frame()[0][0]
	if px.A == 0 || px.R == 255 {
		t.Fatalf("pixel should be dimmed, not cleared: %v", px)
	}
	for range 100 {
		b.draw()
	}
	if b.Frame()[0][0].A != 0 {
		t.Fatal("trail never died")
	}
	b.trails = false
	b.bars[0][0] = 1
	b.draw()
	b.bars[0][0] = 0
	b.draw()
	if b.Frame()[0][0].A != 0 {
		t.Fatal("trails off: pixel should be cleared")
	}
}

func TestActivateClearsThePreviousFrame(t *testing.T) {
	b := newBars(1, 4, 4, 2)
	b.bars[0][0] = 1
	b.draw()
	if musictest.Lit(b.Frame()) == 0 {
		t.Fatal("setup: nothing drawn")
	}
	b.Update(bubble.Activate{})
	if n := musictest.Lit(b.Frame()); n != 0 {
		t.Fatalf("frame lit pixels = %d right after Activate, want 0: a re-activation must not show the previous picture for one frame", n)
	}
}

func TestBarsActivatePicksFromConfiguredLayouts(t *testing.T) {
	b := New()
	if err := b.Configure(json.RawMessage(`{"layouts":["mirror","hmirror"]}`)); err != nil {
		t.Fatal(err)
	}
	for range 5000 {
		b.Update(bubble.Activate{})
		if b.layout != Mirrored && b.layout != HMirrored {
			t.Fatalf("layout %v not in the configured list", b.layout)
		}
	}
}

func TestBarsActivateTrailsChance(t *testing.T) {
	b := New()
	if err := b.Configure(json.RawMessage(`{"trails":0.05}`)); err != nil {
		t.Fatal(err)
	}
	on := 0
	for range 5000 {
		b.Update(bubble.Activate{})
		if b.trails {
			on++
		}
	}
	if on < 150 || on > 350 { // about 5%
		t.Fatalf("trails on in %d of 5000, want about 250", on)
	}
}

func TestBarsActivateNeverEndsWithHiddenBarsAndNoPeaks(t *testing.T) {
	b := New()
	if err := b.Configure(json.RawMessage(`{"palettes":["peaks"],"peaks":["none"]}`)); err != nil {
		t.Fatal(err)
	}
	for range 5000 {
		b.Update(bubble.Activate{})
		if b.peakStyle == NoPeaks {
			t.Fatal("picked the peaks palette with no peaks: nothing would be drawn")
		}
	}
}

func TestBarsConfigureRejectsBadValues(t *testing.T) {
	b := New()
	if err := b.Configure(json.RawMessage(`{"layouts":["diagonal"]}`)); err == nil {
		t.Fatal("unknown layout should error")
	}
	if err := b.Configure(json.RawMessage(`{"trails":2}`)); err == nil {
		t.Fatal("trails out of range should error")
	}
}

func TestBarsKeysCycleLayoutPeakStyleAndTrails(t *testing.T) {
	b := New()
	key := func(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: rune(s[0]), Text: s} }

	if b.layout != Stacked {
		t.Fatalf("starting layout %v, want Stacked", b.layout)
	}
	for l := Stacked; l < Layout(len(Layouts)); l++ {
		want := (l + 1) % Layout(len(Layouts))
		b.Update(key("l"))
		if b.layout != want {
			t.Fatalf("after %v: layout %v, want %v", l, b.layout, want)
		}
	}

	if b.peakStyle != Falling {
		t.Fatalf("starting peak style %v, want Falling", b.peakStyle)
	}
	for s := Falling; s <= NoPeaks; s++ {
		want := (s + 1) % (NoPeaks + 1)
		b.Update(key("p"))
		if b.peakStyle != want {
			t.Fatalf("after %v: peak style %v, want %v", s, b.peakStyle, want)
		}
	}

	before := b.trails
	b.Update(key("t"))
	if b.trails == before {
		t.Fatal("t should toggle trails")
	}
	b.Update(key("t"))
	if b.trails != before {
		t.Fatal("t should toggle trails back")
	}
}

// TestBarsMonoHMirrorReflectsSameState covers a mono signal in HMirrored: two
// panes always, and with one source both panes must draw the very same
// state (not two independent physics runs), so the bottom half is a pixel-
// exact vertical reflection of the top half.
func TestBarsMonoHMirrorReflectsSameState(t *testing.T) {
	b := newBars(1, 4, 16, 8)
	b.layout = HMirrored
	b.bars[0] = []float32{1, 0.5, 0.25, 0.75} // non-symmetric: a wrong flip cannot pass by accident
	b.draw()
	f := b.Frame()
	if musictest.Lit(f) == 0 {
		t.Fatal("nothing drawn")
	}
	h := len(f)
	for r := 0; r < h/2; r++ {
		if !slices.Equal(f[r], f[h-1-r]) {
			t.Fatalf("row %d != mirrored row %d:\n%v\n%v", r, h-1-r, f[r], f[h-1-r])
		}
	}
	if musictest.Lit(f[:h/2]) == 0 || musictest.Lit(f[h/2:]) == 0 {
		t.Fatal("one half is empty")
	}
}

// TestBarsMonoMirrorReflectsSameState covers a mono signal in Mirrored: two
// panes always, one source, drawn twice with the right one flipped, band 0
// at the centre.
func TestBarsMonoMirrorReflectsSameState(t *testing.T) {
	b := newBars(1, 4, 16, 8)
	b.layout = Mirrored
	b.bars[0] = []float32{1, 0.6, 0.3, 0.1} // band 0 tallest and non-symmetric
	b.draw()
	f := b.Frame()
	if musictest.Lit(f) == 0 {
		t.Fatal("nothing drawn")
	}
	w := len(f[0])
	for y, row := range f {
		for x := 0; x < w/2; x++ {
			if row[x] != row[w-1-x] {
				t.Fatalf("row %d: column %d != mirrored column %d: %v", y, x, w-1-x, row)
			}
		}
	}
	if f[0][w/2-1].A == 0 || f[0][w/2].A == 0 {
		t.Fatal("band 0 (the tallest) should reach the centre at the top row")
	}
	left, right := musictest.Lit(cols(f, 0, w/2)), musictest.Lit(cols(f, w/2, w))
	if left == 0 || right == 0 {
		t.Fatal("one half is empty")
	}
}

// cols returns frame's columns [from, to) as their own frame, for Lit to count.
func cols(frame [][]color.RGBA, from, to int) [][]color.RGBA {
	out := make([][]color.RGBA, len(frame))
	for y, row := range frame {
		out[y] = row[from:to]
	}
	return out
}

// TestBarsSingleUsesMix covers a stereo signal in Single: one source, its
// levels are sig.Mix, not either channel.
func TestBarsSingleUsesMix(t *testing.T) {
	sig := dsp.Signal{
		Levels: [][]float32{{0.9, 0.1}, {0.1, 0.9}}, // L and R differ: using either would fail this test
		Mix:    []float32{0.4, 0.7},
	}
	b := New()
	b.Resize(16, 8)
	b.layout, b.peakStyle = Single, NoPeaks
	b.Update(bubble.Tick{Signal: sig, Dt: musictest.Step})
	if len(b.bars) != 1 {
		t.Fatalf("sources = %d, want 1 for a single full-width spectrum", len(b.bars))
	}
	b.draw()
	f := b.Frame()
	barW := b.W / b.Bands // one pane spans the full frame width
	want0 := int(sig.Mix[0]*float32(b.H) + 0.5)
	want1 := int(sig.Mix[1]*float32(b.H) + 0.5)
	if got := strings.Count(musictest.Column(f, 0), "#"); got != want0 {
		t.Fatalf("band 0 height = %d, want %d (from Mix, not Levels)", got, want0)
	}
	if got := strings.Count(musictest.Column(f, barW), "#"); got != want1 {
		t.Fatalf("band 1 height = %d, want %d (from Mix, not Levels)", got, want1)
	}
}

// TestBarsMonoStackedAndSideMatchSingle covers Stacked and SideBySide with a
// mono signal: two identical copies would be pointless, so they fall back to
// one pane and must render pixel-identically to Single.
func TestBarsMonoStackedAndSideMatchSingle(t *testing.T) {
	sig := dsp.Signal{Levels: [][]float32{{0.9, 0.3, 0.6, 0.1}}, Mix: []float32{0.9, 0.3, 0.6, 0.1}}
	frame := func(l Layout) [][]color.RGBA {
		b := New()
		b.Resize(16, 8)
		b.layout = l
		b.Update(bubble.Tick{Signal: sig, Dt: musictest.Step})
		return b.Frame()
	}
	want := frame(Single)
	for _, l := range []Layout{Stacked, SideBySide} {
		got := frame(l)
		for y := range got {
			if !slices.Equal(got[y], want[y]) {
				t.Fatalf("%v row %d differs from Single:\n%v\n%v", l, y, got[y], want[y])
			}
		}
	}
}

// TestBarsCycleLayoutsNeverPanics drives every layout in turn, for a mono and
// a stereo signal, through the "l" key while ticking: a layout switch changes
// the source count (Single has one, the others len(sig.Levels)), and that
// reshape must never panic or leave a stale, empty frame.
func TestBarsCycleLayoutsNeverPanics(t *testing.T) {
	key := tea.KeyPressMsg{Code: 'l', Text: "l"}
	sigs := map[string]dsp.Signal{
		"mono":   {Levels: [][]float32{{0.9, 0.3, 0.6, 0.1}}, Mix: []float32{0.9, 0.3, 0.6, 0.1}},
		"stereo": {Levels: [][]float32{{0.9, 0.1}, {0.1, 0.9}}, Mix: []float32{0.5, 0.5}},
	}
	for name, sig := range sigs {
		b := New()
		b.Resize(16, 8)
		for range len(Layouts) * 2 {
			b.Update(key)
			b.Update(bubble.Tick{Signal: sig, Dt: musictest.Step})
			if musictest.Lit(b.Frame()) == 0 {
				t.Fatalf("%s, layout %v: empty frame", name, b.layout)
			}
		}
	}
}
