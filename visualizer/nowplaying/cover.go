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

// wantCover forgets the art when the track has another image than the one
// held. An instance whose fetch would duplicate one the source already has
// under way, or already has the answer to, does not ask MA again: it either
// waits for the cover message the fetching instance's answer will reach it
// through too, or, once the source holds that image, builds its own cover
// from the cached decode. Only when start and the source has not started
// this image yet does it fetch. The announcement waits either way.
func (p *NowPlaying) wantCover(image string, start bool) tea.Cmd {
	if image == p.imageURL {
		return nil
	}
	p.imageURL, p.cover, p.art = image, nil, nil
	from := coverURL(p.src.url, image)
	if from == "" {
		p.loading = false
		return nil
	}
	p.loading = true
	side := p.h - 2
	if image == p.src.imageURL { // the source already fetched it, or is fetching it
		if !p.src.fetched {
			return nil // in flight: the fetching instance's cover message reaches this one too
		}
		img := p.src.img // nil for a failed fetch: shrinkAt then yields no art, a black cover like everyone else
		return func() tea.Msg { return cover{url: image, img: img, side: side, art: shrinkAt(img, side)} }
	}
	if !start {
		return nil
	}
	p.src.imageURL, p.src.img, p.src.fetched = image, nil, false
	return func() tea.Msg {
		img, _ := fetch(from)                                                    // a failed cover is a black one
		return cover{url: image, img: img, side: side, art: shrinkAt(img, side)} // here, off the frame loop: providers send covers of 1200 px
	}
}

// gotCover takes the cover unless the track moved on meanwhile, caches the
// decode on the source for a late instance to build its own art from, and
// makes the announcement that waited for it.
func (p *NowPlaying) gotCover(c cover) tea.Cmd {
	if c.url != p.imageURL {
		return nil
	}
	p.src.img, p.src.fetched = c.img, true // idempotent: every instance the message reaches sets the same values
	p.loading, p.cover = false, c.img
	if p.art = c.art; c.side != p.h-2 { // resized while it was fetched
		p.resized()
	}
	return p.announce()
}

// resized scales the cover for the current height; an instance too short to
// show one (the controller's template, height 0) holds no art.
func (p *NowPlaying) resized() {
	p.art = nil
	if p.cover != nil && p.h > 2 {
		p.art = shrink(p.cover, p.h-2)
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

// shrinkAt is shrink guarded for a fetch that may have failed (img nil) or an
// instance without a size to shrink to (side < 1): the template instance,
// which fetches for the group, holds no art of its own.
func shrinkAt(img image.Image, side int) [][]color.RGBA {
	if img == nil || side < 1 {
		return nil
	}
	return shrink(img, side)
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
