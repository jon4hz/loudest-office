package nowplaying

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // radio logos
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

const maxCover = 4 << 20

const maxCoverSide = 2048 // px; MA's largest size is 1024

var coverClient = &http.Client{Timeout: 10 * time.Second}

// cover is the answer to one cover fetch; img is nil when it failed.
type cover struct {
	url  string // the image_url it was fetched for
	img  image.Image
	side int            // the size art was scaled to
	art  [][]color.RGBA // img at side x side
}

// coverURL is where to fetch MA's image_url from. MA builds its imageproxy
// URLs with its own base URL, which need not be reachable from here, and in
// 512 px: take the path, from our MA, in the smallest size MA allows. Any
// other http(s) URL (radio) is used as it is, anything else is "".
func coverURL(base, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	if strings.HasPrefix(u.Path, "/imageproxy/") {
		return base + u.Path + "?size=80&fmt=png"
	}
	return raw
}

// wantCover starts the fetch when the track has another image than the one
// held; the announcement waits for it.
func (p *NowPlaying) wantCover(image string) tea.Cmd {
	if image == p.imageURL {
		return nil
	}
	p.imageURL, p.src, p.art = image, nil, nil
	from := coverURL(p.url, image)
	if from == "" {
		p.loading = false
		return nil
	}
	p.loading = true
	side := p.h - 2
	return func() tea.Msg {
		img, _ := fetch(from) // a failed cover is a black one
		c := cover{url: image, img: img, side: side}
		if img != nil { // here, off the frame loop: providers send covers of 1200 px
			c.art = shrink(img, side)
		}
		return c
	}
}

// gotCover takes the cover unless the track moved on meanwhile, and makes the
// announcement that waited for it.
func (p *NowPlaying) gotCover(c cover) tea.Cmd {
	if c.url != p.imageURL {
		return nil
	}
	p.loading, p.src = false, c.img
	if p.art = c.art; c.side != p.h-2 { // resized while it was fetched
		p.resized()
	}
	return p.announce()
}

// resized scales the cover for the current height.
func (p *NowPlaying) resized() {
	if p.art = nil; p.src != nil {
		p.art = shrink(p.src, p.h-2)
	}
}

// fetch decodes the image at from. The decoders allocate their pixel buffer
// from the header's declared width x height before reading any pixel data,
// so limiting the encoded bytes read does not bound memory: a forged header
// can claim a huge size in a few hundred bytes. Read the whole (bounded)
// body first and check its declared size before decoding.
func fetch(from string) (image.Image, error) {
	resp, err := coverClient.Get(from)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover: %s", resp.Status)
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, maxCover))
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	if cfg.Width > maxCoverSide || cfg.Height > maxCoverSide {
		return nil, fmt.Errorf("cover: %dx%d is too large", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(buf))
	return img, err
}

// shrink scales img to side x side by averaging the source pixels under each
// target pixel; nil for nothing to draw. Covers are square: another shape is
// squashed.
func shrink(img image.Image, side int) [][]color.RGBA {
	b := img.Bounds()
	if side <= 0 || b.Empty() {
		return nil
	}
	out := bubble.NewFrame(side, side)
	for y := range side {
		y0, y1 := b.Min.Y+y*b.Dy()/side, b.Min.Y+(y+1)*b.Dy()/side
		for x := range side {
			x0, x1 := b.Min.X+x*b.Dx()/side, b.Min.X+(x+1)*b.Dx()/side
			var r, g, bl, n uint32
			for sy := y0; sy < max(y1, y0+1); sy++ { // at least one: a source smaller than side
				for sx := x0; sx < max(x1, x0+1); sx++ {
					cr, cg, cb, _ := img.At(sx, sy).RGBA()
					r, g, bl, n = r+cr>>8, g+cg>>8, bl+cb>>8, n+1
				}
			}
			out[y][x] = color.RGBA{uint8(r / n), uint8(g / n), uint8(bl / n), 255}
		}
	}
	return out
}
