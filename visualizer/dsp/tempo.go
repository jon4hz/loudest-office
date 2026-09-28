package dsp

import "math"

// Tempo estimates the beat period from onset strengths, one per block, by
// autocorrelating the last few seconds. It knows nothing until half a window
// has passed and forgets the beat when the window goes quiet.
type Tempo struct {
	blockHz float64
	hist    []float32 // ring of onset strengths
	pos, n  int
	period  float64 // blocks per beat, 0 = unknown
}

// NewTempo takes the block rate in blocks per second.
func NewTempo(blockHz float64) *Tempo {
	// ponytail: fixed 6 s window; longer is steadier, shorter follows changes
	return &Tempo{blockHz: blockHz, hist: make([]float32, max(4, int(6*blockHz)))}
}

// Add records one block's onset strength and re-estimates twice a second.
func (t *Tempo) Add(onset float32) {
	t.hist[t.pos] = onset
	t.pos = (t.pos + 1) % len(t.hist)
	t.n++
	if t.n >= len(t.hist)/2 && t.n%max(1, int(t.blockHz/2)) == 0 {
		t.estimate()
	}
}

// Period is the beat length in blocks, 0 while unknown.
func (t *Tempo) Period() float64 { return t.period }

// BPM is the tempo in beats per minute, 0 while unknown.
func (t *Tempo) BPM() float64 {
	if t.period == 0 {
		return 0
	}
	return 60 * t.blockHz / t.period
}

// estimate picks the lag between 60 and 200 BPM with the strongest
// autocorrelation, nudged towards 120 BPM to break octave ties, and refines
// it between blocks with a parabola through its neighbours.
func (t *Tempo) estimate() {
	n := min(t.n, len(t.hist))
	x := make([]float64, n)
	var mean float64
	for i := range x {
		x[i] = float64(t.hist[(t.pos-n+i+len(t.hist))%len(t.hist)])
		mean += x[i] / float64(n)
	}
	var r0 float64
	for i := range x {
		x[i] -= mean
		r0 += x[i] * x[i]
	}
	t.period = 0
	if r0 == 0 {
		return
	}
	minLag, maxLag := int(t.blockHz*60/200), int(t.blockHz*60/60)
	if maxLag >= n-1 || minLag < 1 {
		return
	}
	r := make([]float64, maxLag+2)
	for lag := minLag - 1; lag <= maxLag+1 && lag < n; lag++ {
		if lag < 1 {
			continue
		}
		var s float64
		for i := lag; i < n; i++ {
			s += x[i] * x[i-lag]
		}
		r[lag] = s / float64(n-lag) / (r0 / float64(n))
	}
	best, bestScore := 0, 0.0
	for lag := minLag; lag <= maxLag; lag++ {
		oct := math.Log2(60 * t.blockHz / float64(lag) / 120)
		if score := r[lag] * math.Exp(-0.5*oct*oct); score > bestScore {
			best, bestScore = lag, score
		}
	}
	// ponytail: fixed 0.15 floor on the normalised autocorrelation; below it
	// the rhythm is too weak to trust and the caller falls back to energy.
	if best == 0 || r[best] < 0.15 {
		return
	}
	p := float64(best)
	if d := r[best-1] - 2*r[best] + r[best+1]; d < 0 {
		p += 0.5 * (r[best-1] - r[best+1]) / d
	}
	t.period = p
}
