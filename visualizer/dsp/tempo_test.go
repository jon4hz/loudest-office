package dsp

import "testing"

func TestTempoFindsThePulsePeriod(t *testing.T) {
	tp := NewTempo(40) // 40 blocks/s, a pulse every 20 blocks is 120 BPM
	for i := range 400 {
		var onset float32
		if i%20 == 0 {
			onset = 1
		}
		tp.Add(onset)
	}
	if bpm := tp.BPM(); bpm < 118 || bpm > 122 {
		t.Fatalf("BPM %.1f, want 120", bpm)
	}
	tp = NewTempo(40) // 27 blocks is 88.9 BPM, between two lags
	for i := range 600 {
		var onset float32
		if i%27 == 0 {
			onset = 1
		}
		tp.Add(onset)
	}
	if bpm := tp.BPM(); bpm < 87 || bpm > 91 {
		t.Fatalf("BPM %.1f, want 88.9", bpm)
	}
	for range 400 {
		tp.Add(0)
	}
	if bpm := tp.BPM(); bpm != 0 {
		t.Fatalf("silence should forget the beat, got %.1f", bpm)
	}
}
