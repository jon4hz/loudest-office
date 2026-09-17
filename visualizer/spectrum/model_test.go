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
					m.bars[ch][b], m.peaks[ch][b] = 0.5, 0.9
				}
			}
			m.draw() // must not panic
		}
	}
}
