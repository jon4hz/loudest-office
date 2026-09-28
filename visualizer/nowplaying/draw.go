package nowplaying

import (
	"image/color"
	"time"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

const (
	scrollSpeed = 12.0            // px/s
	scrollPause = 2 * time.Second // before a line starts to move
	scrollGap   = 3 * 6           // px between a line and its repeat: three glyphs
)

var (
	white = color.RGBA{255, 255, 255, 255}
	green = color.RGBA{0x1d, 0xb9, 0x54, 255}
	dim   = color.RGBA{153, 153, 153, 255} // the clock's
)

// draw lays the track out like Tronbyt's compact Spotify mode: the cover in a
// square of the panel's height with 1 px of padding, title over artist right
// of it, centred on the middle row. Without a cover the text takes the width.
func (p *NowPlaying) draw() {
	if p.w == 0 || p.h == 0 {
		return
	}
	for _, row := range p.frame {
		clear(row)
	}
	if p.title == "" { // no track: say why, so a wrong token shows
		s := p.status
		if s == "" {
			s = "-"
		}
		bubble.DrawText(p.frame, (p.w-bubble.TextWidth(s, 1))/2, (p.h-7)/2, s, dim, 1)
		return
	}
	p.line(p.title, p.h/2-8, white)
	p.line(p.artist, p.h/2+1, green)
	// the cover's square goes over the text: that is what clips the scroll
	for y, row := range p.frame {
		clear(row[:min(p.x0(), p.w)])
		if y >= 1 && y-1 < len(p.art) {
			copy(row[1:], p.art[y-1])
		}
	}
}

// x0 is where the text column starts: right of the cover, else 1 px in.
func (p *NowPlaying) x0() int {
	if p.art == nil {
		return 1
	}
	return p.h + 1
}

// line draws s from the text column on. A line that does not fit waits
// scrollPause, then moves left with a repeat of itself behind a gap, and
// waits again when the repeat has reached the start.
func (p *NowPlaying) line(s string, y int, c color.RGBA) {
	x0 := p.x0()
	width := bubble.TextWidth(s, 1)
	if width <= p.w-x0 {
		bubble.DrawText(p.frame, x0, y, s, c, 1)
		return
	}
	period := width + scrollGap
	cycle := scrollPause + time.Duration(float64(period)/scrollSpeed*float64(time.Second))
	var off int
	if t := p.t % cycle; t > scrollPause {
		off = int((t - scrollPause).Seconds() * scrollSpeed)
	}
	bubble.DrawText(p.frame, x0-off, y, s, c, 1)
	bubble.DrawText(p.frame, x0-off+period, y, s, c, 1)
}
