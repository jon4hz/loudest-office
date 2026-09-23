package dsp

import "math"

// Signal is one frame's audio analysis, accumulated by Add since the last
// Take.
type Signal struct {
	Levels  [][]float32 // per channel, max since the last Take; valid until the next Add
	Mix     []float32   // channel-mixed levels; len(Mix) is the band count
	Energy  float32
	Drop    bool    // latched until Take
	BigDrop bool    // latched until Take
	Beat    float32 // phase 0..1, meaningful while BPM > 0
	Beats   int
	BPM     float64 // 0 = no tempo
	DB      float64 // RMS dBFS before gain, clamped to >= -120
}

// Analyzer turns audio blocks into a Signal: band levels, a drop detector,
// onset-driven tempo tracking and an auto-gain offset. Create it with
// NewAnalyzer, feed blocks to Add and read the accumulated result with Take.
type Analyzer struct {
	channels, rate, fftSize, bands int
	gain, agc                      float64 // agc is the auto-gain offset in dB
	autoGain                       bool

	window []float32
	edges  []int

	levels  [][]float32 // per channel, max-merged since the last Take
	mix     []float32   // channel-mixed, max-merged since the last Take
	cur     []float32   // this block's own mix, for the onset flux; never merged
	prevMix []float32
	taken   bool // true right after Take, so the next Add overwrites instead of merging

	energy        float32
	energyAvg     float32
	refractory    int
	drop, bigDrop bool

	fluxAvg float32
	tempo   *Tempo
	beat    float32
	beats   int

	db float64
}

// NewAnalyzer creates an Analyzer for the given channel count, band count,
// sample rate and FFT size. gain is a fixed offset in dB; autoGain adapts it
// so the loudest band sits near the top (see Analyzer.Add).
func NewAnalyzer(channels, bands, rate, fftSize int, gain float64, autoGain bool) *Analyzer {
	// db starts at -120 (silence), not 0: the first frame tick usually beats
	// the first Add, and 0 dBFS would show a music bubble at full brightness
	// until the first audio block arrives.
	a := &Analyzer{channels: channels, rate: rate, fftSize: fftSize, gain: gain, autoGain: autoGain, db: -120}
	a.window = Hann(fftSize)
	a.SetBands(bands)
	return a
}

// SetBands changes the band count and resets the level buffers.
func (a *Analyzer) SetBands(n int) {
	a.bands = n
	a.edges = Bands(n, a.fftSize, a.rate, 40, 16000)
	a.levels = make([][]float32, a.channels)
	for ch := range a.levels {
		a.levels[ch] = make([]float32, n)
	}
	a.mix = make([]float32, n)
	a.cur = make([]float32, n)
	a.prevMix = make([]float32, n)
}

// Bands is the current band count.
func (a *Analyzer) Bands() int { return a.bands }

// Add analyses one block of samples per channel and merges it into the
// signal accumulated since the last Take.
func (a *Analyzer) Add(block [][]float32) {
	fresh := a.taken
	a.taken = false

	clear(a.cur)
	var loudest float32
	var energy float32
	for ch := 0; ch < a.channels && ch < len(block); ch++ {
		if len(block[ch]) < a.fftSize {
			continue
		}
		lv := Levels(block[ch], a.window, a.edges, a.gain+a.agc, -60)
		for b, l := range lv {
			loudest = max(loudest, l)
			energy += l / float32(len(lv)*a.channels)
			a.cur[b] += l / float32(a.channels)
			if fresh {
				a.levels[ch][b] = l
			} else {
				a.levels[ch][b] = max(a.levels[ch][b], l)
			}
		}
	}
	if fresh {
		copy(a.mix, a.cur)
	} else {
		for b, v := range a.cur {
			a.mix[b] = max(a.mix[b], v)
		}
	}
	a.energy = energy
	a.db = dbfs(block)

	// ponytail: fixed drop detector: energy 1.5x above a ~2 s average
	// starts a ~0.7 s burst, 1.7x is a big drop. A running burst is never
	// re-triggered, so whatever it drives stays clean.
	a.refractory = max(a.refractory-1, 0)
	if a.refractory == 0 && a.energyAvg > 0.02 && energy > 1.5*a.energyAvg {
		a.refractory, a.drop, a.bigDrop = 30, true, a.bigDrop || energy > 1.7*a.energyAvg
	}
	a.energyAvg += (energy - a.energyAvg) * 0.015

	// onset flux: how much the spectrum rose since the last block
	var flux float32
	for b, l := range a.cur {
		flux += max(0, l-a.prevMix[b])
	}
	copy(a.prevMix, a.cur)
	if a.tempo == nil && len(block) > 0 && len(block[0]) > 0 {
		a.tempo = NewTempo(float64(a.rate) / float64(len(block[0])))
	}
	if a.tempo != nil {
		a.tempo.Add(flux)
	}
	// ponytail: fixed onset rule, flux twice its ~0.5 s average
	onset := a.fluxAvg > 0.01 && flux > 2*a.fluxAvg
	a.fluxAvg += (flux - a.fluxAvg) * 0.05
	// beat phase: runs at the tempo, pulled back to the start of the beat
	// by an onset that lands near it
	if p := a.period(); p > 0 {
		a.beat += 1 / float32(p)
		if a.beat >= 1 {
			a.beat--
			a.beats++
		}
		if onset && (a.beat > 0.8 || a.beat < 0.2) {
			if a.beat > 0.8 { // the beat came early: it still counts
				a.beats++
			}
			a.beat = 0
		}
	}

	if a.autoGain {
		// ponytail: fixed attack/release in dB per block (~23 ms); make
		// them options if a track ever pumps visibly.
		switch {
		case loudest > 0.95:
			a.agc = max(a.agc-1, -30)
		case loudest > 0.05 && loudest < 0.6:
			a.agc = min(a.agc+0.05, 40)
		}
	}
}

// period is the beat length in blocks, 0 while no tempo is known.
func (a *Analyzer) period() float64 {
	if a.tempo == nil {
		return 0
	}
	return a.tempo.Period()
}

// bpm is the tempo in beats per minute, 0 while unknown.
func (a *Analyzer) bpm() float64 {
	if a.tempo == nil {
		return 0
	}
	return a.tempo.BPM()
}

// Take returns the signal accumulated since the last Take and clears the
// latched Drop/BigDrop flags. The returned slices are not copied; they stay
// valid until the next Add.
func (a *Analyzer) Take() Signal {
	sig := Signal{
		Levels:  a.levels,
		Mix:     a.mix,
		Energy:  a.energy,
		Drop:    a.drop,
		BigDrop: a.bigDrop,
		Beat:    a.beat,
		Beats:   a.beats,
		BPM:     a.bpm(),
		DB:      a.db,
	}
	a.taken = true
	a.drop, a.bigDrop = false, false
	return sig
}

// dbfs is the RMS level of every sample across every channel, in dBFS,
// clamped to -120 so it survives JSON (which cannot carry -Inf).
func dbfs(block [][]float32) float64 {
	var sum float64
	var n int
	for _, ch := range block {
		for _, s := range ch {
			sum += float64(s) * float64(s)
		}
		n += len(ch)
	}
	if n == 0 {
		return -120
	}
	return max(10*math.Log10(sum/float64(n)), -120)
}
