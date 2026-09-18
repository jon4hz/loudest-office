package spectrum

import (
	"image/color"
	"math"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func sine(n int, hz float64) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / 44100))
	}
	return s
}

func TestModelDrawsBarsAndHoldsPeaks(t *testing.T) {
	m := New(Channels(1), Bands(8), FFTSize(256), Size(16, 8), PeakHold(2), PeakFall(0.5), FallSpeed(1))
	m, _ = m.Update(SamplesMsg{sine(256, 1000)})
	f := m.Frame()
	if len(f) != 8 || len(f[0]) != 16 {
		t.Fatalf("frame %dx%d", len(f[0]), len(f))
	}
	lit := 0
	for _, row := range f {
		for _, px := range row {
			if px.A != 0 {
				lit++
			}
		}
	}
	if lit == 0 {
		t.Fatal("nothing drawn")
	}
	peakBefore := append([]float32(nil), m.peaks[0]...)
	silence := make([]float32, 256)
	m, _ = m.Update(SamplesMsg{silence}) // bars drop, hold 2
	m, _ = m.Update(SamplesMsg{silence}) // hold 1
	for b := range peakBefore {
		if m.peaks[0][b] != peakBefore[b] {
			t.Fatalf("peak %d moved during hold", b)
		}
	}
	m, _ = m.Update(SamplesMsg{silence}) // hold 0
	m, _ = m.Update(SamplesMsg{silence}) // falls
	moved := false
	for b := range peakBefore {
		if m.peaks[0][b] < peakBefore[b] {
			moved = true
		}
	}
	if !moved {
		t.Fatal("peak never fell")
	}
}

func TestModelFollowsWindowSize(t *testing.T) {
	m := New()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	if f := m.Frame(); len(f) != 20 || len(f[0]) != 40 {
		t.Fatalf("frame %dx%d", len(f[0]), len(f))
	}
	if lines := strings.Count(m.View(), "\n"); lines != 9 {
		t.Fatalf("view has %d newlines, want 9", lines)
	}
	m.SetBands(16)
	if m.NumBands() != 16 || len(m.bars[0]) != 16 {
		t.Fatal("SetBands did not resize")
	}
}

func TestAutoGain(t *testing.T) {
	quiet := sine(256, 1000)
	for i := range quiet {
		quiet[i] *= 0.01 // -40 dBFS -> level 0.33 without gain
	}
	m := New(Channels(1), Bands(8), FFTSize(256), Size(16, 8), AutoGain(true))
	for range 400 {
		m, _ = m.Update(SamplesMsg{quiet})
	}
	if mx := maxLevel(m.bars[0]); mx < 0.6 {
		t.Fatalf("auto gain never lifted a quiet signal: %v", mx)
	}
	for range 60 {
		m, _ = m.Update(SamplesMsg{sine(256, 1000)}) // full scale: gain must back off fast
	}
	if m.agc > 0 {
		t.Fatalf("gain still boosted at full scale: %v dB", m.agc)
	}
	silence := make([]float32, 256)
	before := m.agc
	for range 100 {
		m, _ = m.Update(SamplesMsg{silence})
	}
	if m.agc != before {
		t.Fatalf("gain moved during silence: %v -> %v", before, m.agc)
	}
}

func maxLevel(v []float32) float32 {
	var mx float32
	for _, x := range v {
		mx = max(mx, x)
	}
	return mx
}

// litColumns returns, per row, which x positions are lit, as a compact string.
func litColumns(m Model) []string {
	var out []string
	for _, row := range m.Frame() {
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

func TestLayouts(t *testing.T) {
	for _, tc := range []struct {
		layout    Layout
		top, last string // first and last frame row with ch0 band0 and ch1 band0 full
	}{
		{Stacked, "###.............", "............###."},
		{SideBySide, "##......##......", "##......##......"},
		{Mirrored, "......####......", "......####......"},
	} {
		m := New(Channels(2), Bands(4), FFTSize(256), Size(16, 8), WithLayout(tc.layout))
		m.bars[0][0], m.bars[1][0] = 1, 1
		if tc.layout == Stacked {
			m.bars[1][0], m.bars[1][3] = 0, 0.25 // one pixel of ch1's top band, bottom right
		}
		m.draw()
		rows := litColumns(m)
		if rows[0] != tc.top || rows[7] != tc.last {
			t.Errorf("%v:\n%s", tc.layout, strings.Join(rows, "\n"))
		}
	}
}

func TestHMirrorMeetsAtCentreLine(t *testing.T) {
	m := New(Channels(2), Bands(4), FFTSize(256), Size(16, 8), WithLayout(HMirrored))
	m.bars[0][0], m.bars[1][0] = 0.5, 0.5 // ch0 grows up from the centre, ch1 down
	m.draw()
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
	if got := litColumns(m); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s", strings.Join(got, "\n"))
	}
}

func TestSideLayoutsMeetAtCentre(t *testing.T) {
	// 3 bands of width 2 in an 8-wide half leave 2 px over; they must not end up in the middle.
	for _, tc := range []struct {
		layout Layout
		want   string
	}{
		{Mirrored, "......####......"},
		{SideBySide, "..##....##......"},
	} {
		m := New(Channels(2), Bands(3), FFTSize(256), Size(16, 8), WithLayout(tc.layout))
		m.bars[0][0], m.bars[1][0] = 1, 1
		m.draw()
		if got := litColumns(m)[0]; got != tc.want {
			t.Errorf("%v: got %q", tc.layout, got)
		}
	}
}

func TestRelativePaletteScalesToBar(t *testing.T) {
	p, _ := PaletteByName("bi")
	m := New(Channels(1), Bands(1), FFTSize(256), Size(1, 10), WithPalette(p))
	m.bars[0][0] = 0.5 // 5 px tall: the whole flag must fit in those 5 rows
	m.draw()
	f := m.Frame()
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

func TestDriftRollsColoursAcrossBands(t *testing.T) {
	p, ok := PaletteByName("drift")
	if !ok || !p.Animate {
		t.Fatal("drift should exist and be animated")
	}
	m := New(Channels(1), Bands(4), FFTSize(256), Size(4, 2), WithPalette(p))
	for b := range m.bars[0] {
		m.bars[0][b] = 1
	}
	m.draw()
	before := append([]color.RGBA(nil), m.Frame()[1]...)
	m.tick = 8 // one step
	m.draw()
	after := m.Frame()[1]
	if after[0] != before[1] || after[3] != before[0] {
		t.Fatalf("colours did not roll by one band:\n%v\n%v", before, after)
	}
}

func TestEveryPaletteDrawsInEveryLayout(t *testing.T) {
	for _, p := range Palettes {
		for l := Stacked; l <= HMirrored; l++ {
			m := New(Channels(2), Bands(4), FFTSize(256), Size(16, 8), WithLayout(l), WithPalette(p))
			for ch := range m.bars {
				for b := range m.bars[ch] {
					m.bars[ch][b], m.peaks[ch][b] = 0.5, 1.5 // a flying peak past the top
				}
			}
			m.draw() // must not panic
		}
	}
}

func TestFlyingPeaksLaunchAndRearm(t *testing.T) {
	m := New(Channels(1), Bands(8), FFTSize(256), Size(16, 8), PeakHold(1), FallSpeed(1), WithPeakStyle(Flying))
	m, _ = m.Update(SamplesMsg{sine(256, 1000)})
	loud := 0
	for b, p := range m.peaks[0] {
		if p > m.peaks[0][loud] {
			loud = b
		}
	}
	start := m.peaks[0][loud]
	silence := make([]float32, 256)
	flew, rearmed := false, false
	for i := 0; i < 200 && !rearmed; i++ {
		m, _ = m.Update(SamplesMsg{silence})
		p := m.peaks[0][loud]
		if p > 1 {
			flew = true
		}
		if flew && p == 0 {
			rearmed = true
		}
		if !flew && p < start {
			t.Fatalf("block %d: flying peak fell to %v", i, p)
		}
		m.draw() // peaks above the frame must not panic
	}
	if !flew || !rearmed {
		t.Fatalf("flew=%v rearmed=%v", flew, rearmed)
	}
	if m.PeakStyle() != Flying {
		t.Fatal("PeakStyle getter")
	}
	if s, ok := ParsePeakStyle("none"); !ok || s != NoPeaks {
		t.Fatal("ParsePeakStyle")
	}
}

func TestBeatPeaksFlyOnADrop(t *testing.T) {
	quiet := sine(256, 1000)
	for i := range quiet {
		quiet[i] *= 0.32 // a plain hit (measured 1.63x the average), between the hit and scatter lines
	}
	loud := sine(256, 1000)
	silence := make([]float32, 256)
	run := func(style PeakStyle) bool {
		m := New(Channels(1), Bands(8), FFTSize(256), Size(16, 8), PeakHold(5), FallSpeed(0.05), WithPeakStyle(style))
		for range 300 { // settle the ~2 s running energy average on a quiet passage
			m, _ = m.Update(SamplesMsg{quiet})
		}
		m, _ = m.Update(SamplesMsg{loud}) // the drop
		for range 60 {
			m, _ = m.Update(SamplesMsg{silence})
			for _, p := range m.peaks[0] {
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

// noise is deterministic broadband noise so every band gets a level.
func noise(n int, amp float32) []float32 {
	s := make([]float32, n)
	x := uint32(12345)
	for i := range s {
		x = x*1664525 + 1013904223
		s[i] = amp * (float32(x>>8)/float32(1<<24)*2 - 1)
	}
	return s
}

func TestBeatChaosOnABigDrop(t *testing.T) {
	m := New(Channels(1), Bands(32), FFTSize(256), Size(64, 8), PeakHold(5), FallSpeed(0.05), WithPeakStyle(Beat)) // 32 bands: all picking "up" is a 1 in 3 million chance
	for range 300 {
		m, _ = m.Update(SamplesMsg{noise(256, 0.05)})
	}
	m, _ = m.Update(SamplesMsg{noise(256, 1)}) // a big drop: well over 1.7x the average
	up, down, drifted := false, false, false
	silence := make([]float32, 256)
	for range 40 {
		m, _ = m.Update(SamplesMsg{silence})
		for b := range m.peaks[0] {
			up = up || m.peaks[0][b] > 1
			down = down || m.peaks[0][b] < 0
			drifted = drifted || m.px[0][b] != 0
		}
		m.draw() // peaks off-screen or in other columns must not panic
	}
	if !up || !down || !drifted {
		t.Fatalf("chaos burst: up=%v down=%v drifted=%v", up, down, drifted)
	}
}

func TestPeakNeverDrawnInsideABar(t *testing.T) {
	p, _ := PaletteByName("white") // white bars, red peak
	m := New(Channels(1), Bands(2), FFTSize(256), Size(2, 10), WithPalette(p))
	// band 0: peak below its own bar; band 1: a tall bar that band 0's peak drifts into
	m.bars[0][0], m.peaks[0][0] = 0.8, 0.3
	m.bars[0][1] = 0.9
	m.draw()
	for y, row := range m.Frame() {
		if row[0] == red || row[1] == red {
			t.Fatalf("row %d: peak drawn inside a bar: %v", y, row)
		}
	}
	m.px[0][0] = 1 // drift into column 1, still below that bar's top
	m.draw()
	for y, row := range m.Frame() {
		if row[1] == red {
			t.Fatalf("row %d: drifted peak drawn inside the neighbouring bar", y)
		}
	}
	m.peaks[0][0], m.px[0][0] = 0.95, 0 // above its own bar: must show
	m.draw()
	if m.Frame()[0][0] != red {
		t.Fatal("peak above the bar should be drawn")
	}
}

func TestParseModeRoundTrips(t *testing.T) {
	for mode := Bars; mode <= Fire; mode++ {
		if got, ok := ParseMode(mode.String()); !ok || got != mode {
			t.Fatalf("%v: got %v ok=%v", mode, got, ok)
		}
	}
	if _, ok := ParseMode("nope"); ok {
		t.Fatal("unknown mode parsed")
	}
	m := New(WithMode(Fire))
	if m.Mode() != Fire {
		t.Fatal("WithMode")
	}
	m.SetMode(Bars)
	if m.Mode() != Bars {
		t.Fatal("SetMode")
	}
}

// column returns the lit rows of column x as a string, top to bottom.
func column(m Model, x int) string {
	s := make([]byte, len(m.Frame()))
	for y, row := range m.Frame() {
		s[y] = '.'
		if row[x].A != 0 {
			s[y] = '#'
		}
	}
	return string(s)
}

func TestFireSeedsFromTheBandsAndClimbs(t *testing.T) {
	m := New(Channels(1), Bands(1), FFTSize(256), Size(4, 8), WithMode(Fire))
	loud := sine(256, 1000)
	top, bottom := false, false
	for range 30 {
		m, _ = m.Update(SamplesMsg{loud})
		bottom = bottom || strings.Contains(column(m, 1), "#")
		top = top || m.Frame()[0][0].A != 0 || m.Frame()[0][1].A != 0 || m.Frame()[0][2].A != 0 || m.Frame()[0][3].A != 0
	}
	if !bottom || m.Frame()[7][1].A == 0 {
		t.Fatal("bottom row not burning")
	}
	if !top {
		t.Fatal("a full-level band never reached the top")
	}
	silence := make([]float32, 256)
	for range 40 {
		m, _ = m.Update(SamplesMsg{silence})
	}
	for y := range m.Frame() {
		if strings.Contains(column(m, 1), "#") {
			t.Fatalf("row %d still burning after silence", y)
		}
	}
	m.SetBands(8)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 3, Height: 2}) // resize must not panic
	m, _ = m.Update(SamplesMsg{loud})
}

func TestTrailsFadeInsteadOfClearing(t *testing.T) {
	p, _ := PaletteByName("white")
	m := New(Channels(1), Bands(1), FFTSize(256), Size(1, 4), WithPalette(p), WithTrails(true), WithPeakStyle(NoPeaks))
	if !m.Trails() {
		t.Fatal("WithTrails")
	}
	m.bars[0][0] = 1
	m.draw()
	m.bars[0][0] = 0
	m.draw()
	px := m.Frame()[0][0]
	if px.A == 0 || px.R == 255 {
		t.Fatalf("pixel should be dimmed, not cleared: %v", px)
	}
	for range 100 {
		m.draw()
	}
	if m.Frame()[0][0].A != 0 {
		t.Fatal("trail never died")
	}
	m.SetTrails(false)
	m.bars[0][0] = 1
	m.draw()
	m.bars[0][0] = 0
	m.draw()
	if m.Frame()[0][0].A != 0 {
		t.Fatal("trails off: pixel should be cleared")
	}
}

func TestLifeBlinkerRotates(t *testing.T) {
	m := New(Channels(1), Bands(1), FFTSize(256), Size(5, 5), WithMode(Life))
	m.stepLife()                                                // allocates the grid
	m.life[2][1], m.life[2][2], m.life[2][3] = true, true, true // horizontal blinker
	m.stepLife()
	for y := range 5 {
		for x := range 5 {
			want := x == 2 && y >= 1 && y <= 3 // vertical now
			if m.life[y][x] != want {
				t.Fatalf("cell %d,%d alive=%v", x, y, m.life[y][x])
			}
		}
	}
	m.stepLife()
	if !m.life[2][1] || !m.life[2][3] || m.life[1][2] {
		t.Fatal("blinker did not rotate back")
	}
}

func TestLifeWrapsAtTheEdges(t *testing.T) {
	m := New(Channels(1), Bands(1), FFTSize(256), Size(5, 5), WithMode(Life))
	m.stepLife()
	m.life[0][0], m.life[0][1], m.life[0][4] = true, true, true // a blinker across the left edge
	m.stepLife()
	if !m.life[4][0] || !m.life[1][0] || !m.life[0][0] {
		t.Fatal("blinker across the seam did not become vertical")
	}
}

func TestLifeIsSeededByTheSpectrum(t *testing.T) {
	// 4 bands, 16 columns: 2.15 kHz is band 2, columns 8..11.
	m := New(Channels(1), Bands(4), FFTSize(1024), Size(16, 8), WithMode(Life))
	loudSeen := false
	for range 40 {
		m, _ = m.Update(SamplesMsg{sine(1024, 2150)})
		for x := 0; x < 16; x++ {
			if m.Frame()[7][x].A != 0 && x >= 8 && x < 12 {
				loudSeen = true
			}
		}
	}
	if !loudSeen {
		t.Fatal("the loud band never seeded its columns")
	}
	m = New(Channels(1), Bands(4), FFTSize(1024), Size(16, 8), WithMode(Life))
	silence := make([]float32, 1024)
	for range 40 {
		m, _ = m.Update(SamplesMsg{silence})
		for y, row := range m.Frame() {
			for x, px := range row {
				if px.A != 0 {
					t.Fatalf("silence seeded a cell at %d,%d", x, y)
				}
			}
		}
	}
	m.SetBands(8)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 3, Height: 1}) // resize must not panic
	m, _ = m.Update(SamplesMsg{silence})
	if mode, ok := ParseMode("life"); !ok || mode != Life {
		t.Fatal("ParseMode life")
	}
}

func TestLifeUsesPeakColourWhenBarsAreHidden(t *testing.T) {
	p, _ := PaletteByName("peaks") // bars hidden, peaks blue
	m := New(Channels(1), Bands(1), FFTSize(256), Size(3, 3), WithMode(Life), WithPalette(p))
	m.stepLife()
	m.life[1][1] = true
	m.draw()
	if m.Frame()[1][1] != blue {
		t.Fatalf("cell should take the peak colour, got %v", m.Frame()[1][1])
	}
}

// drop returns a model of the given mode right after a detected drop.
func drop(mode Mode) Model {
	quiet := sine(256, 1000)
	for i := range quiet {
		quiet[i] *= 0.32
	}
	m := New(Channels(1), Bands(8), FFTSize(256), Size(16, 8), WithMode(mode))
	for range 300 {
		m, _ = m.Update(SamplesMsg{quiet})
	}
	m, _ = m.Update(SamplesMsg{sine(256, 1000)})
	return m
}

func TestDropDetectorRunsInEveryMode(t *testing.T) {
	if m := drop(Starfield); m.burst == 0 {
		t.Fatal("starfield: drop not detected")
	}
	if m := drop(Fireworks); m.burst == 0 {
		t.Fatal("fireworks: drop not detected")
	}
}

func TestStarsFlyFasterWithEnergyAndWarpOnADrop(t *testing.T) {
	m := New(Channels(1), Bands(8), FFTSize(256), Size(16, 8), WithMode(Starfield))
	silence := make([]float32, 256)
	m, _ = m.Update(SamplesMsg{silence})
	if len(m.stars) == 0 {
		t.Fatal("no stars")
	}
	lit := 0
	for _, row := range m.Frame() {
		for _, px := range row {
			if px.A != 0 {
				lit++
			}
		}
	}
	if lit == 0 {
		t.Fatal("no star drawn")
	}
	speed := func(m *Model, block []float32) float32 {
		z := m.stars[0].z
		*m, _ = m.Update(SamplesMsg{block})
		if m.stars[0].z > z {
			return 0 // respawned, no measurement
		}
		return z - m.stars[0].z
	}
	var slow, fast float32
	for slow == 0 {
		slow = speed(&m, silence)
	}
	for fast == 0 {
		fast = speed(&m, sine(256, 1000))
	}
	if fast <= slow {
		t.Fatalf("loud block should move stars faster: %v vs %v", fast, slow)
	}
	w := drop(Starfield)
	if w.burst == 0 || w.warp() == 0 {
		t.Fatal("drop should start a warp")
	}
	m.SetBands(4)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 3, Height: 1}) // resize must not panic
	m, _ = m.Update(SamplesMsg{silence})
	if mode, ok := ParseMode("stars"); !ok || mode != Starfield {
		t.Fatal("ParseMode stars")
	}
}

func TestFireworksLaunchFromLoudBandsAndBurst(t *testing.T) {
	// 4 bands: 2.15 kHz is band 2 and the only band with a level.
	m := New(Channels(1), Bands(4), FFTSize(1024), Size(16, 16), WithMode(Fireworks))
	for range 300 { // settle the drop detector: the onset itself counts as a drop
		m, _ = m.Update(SamplesMsg{sine(1024, 2150)})
	}
	m.parts = nil
	for range 100 {
		m, _ = m.Update(SamplesMsg{sine(1024, 2150)})
	}
	if len(m.parts) == 0 {
		t.Fatal("no rockets launched")
	}
	for _, p := range m.parts {
		if p.band != 2 {
			t.Fatalf("particle from silent band %d", p.band)
		}
	}
	silence := make([]float32, 1024)
	sparks := 0
	for i := 0; i < 400 && len(m.parts) > 0; i++ {
		m, _ = m.Update(SamplesMsg{silence})
		for _, p := range m.parts {
			if !p.rocket {
				sparks++
			}
		}
	}
	if sparks == 0 {
		t.Fatal("no rocket ever burst into sparks")
	}
	if len(m.parts) != 0 {
		t.Fatalf("%d particles never died", len(m.parts))
	}
	for range 40 {
		m, _ = m.Update(SamplesMsg{silence})
		if len(m.parts) != 0 {
			t.Fatal("silence launched a rocket")
		}
	}
}

func TestFireworksVolleyOnADrop(t *testing.T) {
	d := drop(Fireworks)
	rockets := 0
	for _, p := range d.parts {
		if p.rocket {
			rockets++
		}
	}
	if rockets < 3 {
		t.Fatalf("drop should fire a volley, got %d rockets", rockets)
	}
	lit := 0
	for _, row := range d.Frame() {
		for _, px := range row {
			if px.A != 0 {
				lit++
			}
		}
	}
	if lit <= rockets {
		t.Fatal("rockets should draw a tail below the head")
	}
	d.SetBands(2)
	d, _ = d.Update(tea.WindowSizeMsg{Width: 3, Height: 1}) // resize must not panic
	d, _ = d.Update(SamplesMsg{make([]float32, 256)})
	if mode, ok := ParseMode("fireworks"); !ok || mode != Fireworks {
		t.Fatal("ParseMode fireworks")
	}
}

func TestLifeSurvivesShrinkingResize(t *testing.T) {
	m := New(WithMode(Life))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m, _ = m.Update(SamplesMsg{sine(m.fftSize, 440), sine(m.fftSize, 440)})
	m.life[m.h-1][m.w-1] = true                              // a cell in the far corner of the old grid
	m, _ = m.Update(tea.WindowSizeMsg{Width: 20, Height: 5}) // must not panic
	m, _ = m.Update(SamplesMsg{sine(m.fftSize, 440), sine(m.fftSize, 440)})
	if len(m.life) != 10 || len(m.life[0]) != 20 {
		t.Fatalf("life grid %dx%d after resize", len(m.life[0]), len(m.life))
	}
}

func TestParrotDancesFasterWhenLoud(t *testing.T) {
	if len(parrotFrames) != 10 {
		t.Fatalf("%d parrot frames embedded", len(parrotFrames))
	}
	m := New(Channels(1), Bands(8), FFTSize(256), Size(50, 36), WithMode(Parrot))
	silence := make([]float32, 256)
	advance := func(block []float32) int {
		n := 0
		for range 40 {
			before := m.parrotFrame
			m, _ = m.Update(SamplesMsg{block})
			n += (m.parrotFrame - before + len(parrotFrames)) % len(parrotFrames)
		}
		return n
	}
	slow := advance(silence)
	fast := advance(sine(256, 1000))
	if slow == 0 || fast <= slow {
		t.Fatalf("loud blocks should dance faster: %d vs %d frames", fast, slow)
	}
	lit, dimmed := 0, 0
	for _, row := range m.Frame() {
		for _, px := range row {
			if px.A != 0 {
				lit++
			}
		}
	}
	art := parrotFrames[m.parrotFrame]
	for _, line := range art {
		for _, ch := range line {
			if ch != ' ' {
				dimmed++
			}
		}
	}
	if lit != 2*dimmed { // at native size every character is exactly two pixels
		t.Fatalf("%d pixels lit for %d characters", lit, dimmed)
	}
	m.SetBands(4)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 3, Height: 1}) // resize must not panic
	m, _ = m.Update(SamplesMsg{silence})
	if mode, ok := ParseMode("parrot"); !ok || mode != Parrot {
		t.Fatal("ParseMode parrot")
	}
}

func TestParrotDancesOnTheBeat(t *testing.T) {
	m := New(Channels(1), Bands(8), FFTSize(1024), Size(50, 36), WithMode(Parrot))
	loud, quiet := sine(1024, 1000), sine(1024, 1000)
	for i := range quiet {
		quiet[i] *= 0.1
	}
	const period = 21 // blocks of 1024 at 44.1 kHz: ~123 BPM
	crossings := 0
	beat := func() {
		for i := range period {
			block := quiet
			if i == 0 {
				block = loud
			}
			before := m.parrotFrame
			m, _ = m.Update(SamplesMsg{block})
			if before >= 5 && m.parrotFrame < 5 {
				crossings++
			}
		}
	}
	for range 30 {
		beat()
	}
	if bpm := m.BPM(); bpm < 118 || bpm > 128 {
		t.Fatalf("BPM %.1f, want ~123", bpm)
	}
	crossings = 0
	for range 20 {
		beat()
	}
	if crossings < 9 || crossings > 11 {
		t.Fatalf("%d dance cycles in 20 beats, want one every two beats", crossings)
	}
}

func TestParrotMaskFollowsResize(t *testing.T) {
	m := New(Channels(1), Bands(8), FFTSize(256), Size(50, 36), WithMode(Parrot))
	m.draw()
	before := m.parrot
	if before == nil || len(before) != len(parrotFrames) || len(before[0]) != 36 || len(before[0][0]) != 50 {
		t.Fatalf("mask not built for 50x36: %v", before != nil)
	}
	m.draw()
	if &m.parrot[0][0][0] != &before[0][0][0] {
		t.Fatal("mask rebuilt without a resize")
	}
	lit := 0
	for _, row := range m.frame {
		for _, px := range row {
			if px.A != 0 {
				lit++
			}
		}
	}
	if lit == 0 {
		t.Fatal("parrot drew nothing")
	}
	m.resize(100, 72)
	m.draw()
	if len(m.parrot[0]) != 72 || len(m.parrot[0][0]) != 100 {
		t.Fatal("mask not rebuilt for the new size")
	}
}

func TestStarsSurgeOnTheBeat(t *testing.T) {
	m := New(Channels(1), Bands(8), FFTSize(1024), Size(16, 8), WithMode(Starfield))
	loud, quiet := sine(1024, 1000), sine(1024, 1000)
	for i := range quiet {
		quiet[i] *= 0.1
	}
	const period = 21
	m, _ = m.Update(SamplesMsg{quiet}) // allocates the stars
	// speed is the mean depth step of the stars that did not respawn
	speed := func(block []float32) float32 {
		before := append([]star(nil), m.stars...)
		m, _ = m.Update(SamplesMsg{block})
		var sum float32
		n := 0
		for i, s := range m.stars {
			if s.z < before[i].z {
				sum += before[i].z - s.z
				n++
			}
		}
		return sum / float32(max(n, 1))
	}
	var onBeat, offBeat float32
	for b := range 40 {
		for i := range period {
			block := quiet
			if i == 0 {
				block = loud
			}
			v := speed(block)
			if b >= 30 && i == 1 { // the quiet block right after the beat
				onBeat += v
			}
			if b >= 30 && i == period/2 { // the quiet block halfway to the next
				offBeat += v
			}
		}
	}
	if m.BPM() == 0 {
		t.Fatal("no tempo found")
	}
	if onBeat < 2*offBeat {
		t.Fatalf("stars should surge on the beat: %v right after vs %v halfway", onBeat, offBeat)
	}
}
