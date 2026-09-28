package music

import (
	"image/color"
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

func TestFadeDimsAndSwitchesOff(t *testing.T) {
	f := bubble.NewFrame(2, 1)
	f[0][0] = color.RGBA{100, 0, 0, 255}
	f[0][1] = color.RGBA{1, 1, 1, 255}
	Fade(f, 0.5)
	if f[0][0] != (color.RGBA{50, 0, 0, 255}) || f[0][1].A != 0 {
		t.Fatalf("fade: got %v", f[0])
	}
}

func TestBlurSpreadsWithoutGettingBrighter(t *testing.T) {
	f := bubble.NewFrame(3, 3)
	f[1][1] = color.RGBA{200, 0, 0, 255}
	Blur(f, 0.25)
	if f[1][1].R >= 200 || f[1][0].A == 0 || f[0][1].A == 0 || f[0][0].A == 0 {
		t.Fatalf("blur should spread to every neighbour and dim the centre: %v", f)
	}
	if f[0][0].R > f[0][1].R || f[0][1].R > f[1][1].R {
		t.Fatalf("blur should fall off with distance: %v", f)
	}
	Blur(bubble.NewFrame(0, 0), 0.25) // must not panic
}

func TestNoiseIsSmoothAndBounded(t *testing.T) {
	for i := range 1000 {
		x, y := float64(i)*0.013, float64(i)*0.007
		a, b := Noise(x, y), Noise(x+0.01, y)
		if a < 0 || a > 1 || b-a > 0.05 || a-b > 0.05 {
			t.Fatalf("noise at %v,%v: %v then %v", x, y, a, b)
		}
	}
	if Noise(1.5, 2.5) == Noise(10.5, 20.5) {
		t.Fatal("noise should vary across the plane")
	}
}
