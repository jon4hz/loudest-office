package nowplaying

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/controller"
)

func init() { pollEvery = time.Millisecond } // pump runs the re-arm tick too

// fakeMA is Music Assistant: /api answers player, /imageproxy/ a cover.
type fakeMA struct {
	mu       sync.Mutex
	status   int    // of /api, 0 = 200
	player   string // the JSON /api answers
	auth     string // the last Authorization header
	body     string // the last /api body
	coverReq string // the last cover request, path?query
	cover    []byte // the PNG /imageproxy/ answers, nil = 404
}

func (m *fakeMA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.URL.Path != "/api" {
		m.coverReq = r.URL.RequestURI()
		if m.cover == nil {
			http.NotFound(w, r)
			return
		}
		w.Write(m.cover)
		return
	}
	buf, _ := io.ReadAll(r.Body)
	m.auth, m.body = r.Header.Get("Authorization"), string(buf)
	if m.status != 0 {
		http.Error(w, "nope", m.status)
		return
	}
	io.WriteString(w, m.player)
}

func (m *fakeMA) set(player string) { m.mu.Lock(); m.player = player; m.mu.Unlock() }

// playerJSON is a players/get answer.
func playerJSON(state, title, artist, image string) string {
	buf, _ := json.Marshal(map[string]any{"playback_state": state,
		"current_media": map[string]string{"title": title, "artist": artist, "image_url": image}})
	return string(buf)
}

// pump runs cmd and feeds what it yields back into p until nothing is left,
// except the re-arm tick: the test sends the next pollMsg itself. It returns
// the interludes p asked for.
func pump(p *NowPlaying, cmd tea.Cmd) []bubble.Interlude {
	if cmd == nil {
		return nil
	}
	var out []bubble.Interlude
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			out = append(out, pump(p, c)...)
		}
	case pollMsg:
	case bubble.Interlude:
		out = append(out, msg)
	default:
		out = append(out, pump(p, p.Update(msg))...)
	}
	return out
}

func start(t *testing.T, m *fakeMA) (*NowPlaying, []bubble.Interlude) {
	t.Helper()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	p := New(srv.URL, "secret", "visualizer")
	return p, pump(p, p.Update(bubble.Resize{W: 64, H: 32}))
}

func TestPollRequest(t *testing.T) {
	m := &fakeMA{player: playerJSON("idle", "", "", "")}
	start(t, m)
	if m.auth != "Bearer secret" {
		t.Errorf("Authorization = %q", m.auth)
	}
	var req struct {
		Command string            `json:"command"`
		Args    map[string]string `json:"args"`
	}
	if err := json.Unmarshal([]byte(m.body), &req); err != nil || req.Command != "players/get" || req.Args["player_id"] != "visualizer" {
		t.Errorf("body = %s (%v)", m.body, err)
	}
}

func TestAnnouncesEachTrackOnce(t *testing.T) {
	m := &fakeMA{player: playerJSON("playing", "One", "A", "")}
	p, got := start(t, m)
	if len(got) != 1 || got[0] != (bubble.Interlude{Name: "nowplaying", For: 10 * time.Second}) {
		t.Fatalf("first track: %v, want one 10 s interlude", got)
	}
	if got := pump(p, p.Update(pollMsg{})); len(got) != 0 {
		t.Errorf("same track: %v, want none", got)
	}
	m.set(playerJSON("paused", "One", "A", ""))
	pump(p, p.Update(pollMsg{}))
	m.set(playerJSON("playing", "One", "A", ""))
	if got := pump(p, p.Update(pollMsg{})); len(got) != 0 {
		t.Errorf("pause and resume: %v, want none", got)
	}
	m.set(playerJSON("paused", "Two", "A", ""))
	if got := pump(p, p.Update(pollMsg{})); len(got) != 0 {
		t.Errorf("a skip while paused: %v, want none yet", got)
	}
	m.set(playerJSON("playing", "Two", "A", ""))
	if got := pump(p, p.Update(pollMsg{})); len(got) != 1 {
		t.Errorf("resume on a new track: %v, want one", got)
	}
}

func TestSecondsZeroNeverAnnounces(t *testing.T) {
	m := &fakeMA{player: playerJSON("idle", "", "", "")}
	p, _ := start(t, m)
	if err := p.Configure(json.RawMessage(`{"seconds":0}`)); err != nil {
		t.Fatal(err)
	}
	m.set(playerJSON("playing", "One", "A", ""))
	if got := pump(p, p.Update(pollMsg{})); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestConfigure(t *testing.T) {
	p := New("http://ma", "t", "p")
	if got := p.Settings().(Settings).Seconds; got != 10 {
		t.Errorf("default seconds = %v, want 10", got)
	}
	for _, bad := range []string{`{"seconds":-1}`, `{"seconds":601}`, `{"secs":1}`} {
		if err := p.Configure(json.RawMessage(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if got := p.Settings().(Settings).Seconds; got != 10 {
		t.Errorf("a rejected patch changed seconds to %v", got)
	}
}

func TestErrorKeepsTrack(t *testing.T) {
	m := &fakeMA{player: playerJSON("playing", "One", "A", "")}
	p, _ := start(t, m)
	m.mu.Lock()
	m.status = http.StatusUnauthorized
	m.mu.Unlock()
	pump(p, p.Update(pollMsg{}))
	if p.title != "One" || p.status != "ma: 401" {
		t.Errorf("title = %q status = %q, want One / ma: 401", p.title, p.status)
	}
}

func TestUnknownPlayer(t *testing.T) {
	p, got := start(t, &fakeMA{player: "null"})
	if len(got) != 0 || p.status != "ma?" {
		t.Errorf("interludes %v status %q, want none / ma?", got, p.status)
	}
}

// flattenCtrl runs cmd and, recursively for a tea.BatchMsg, every cmd it
// yields; it never feeds a result back into any model.
func flattenCtrl(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			flattenCtrl(c)
		}
	}
}

// TestPollsWithFixedPanelSize is the regression for a real panel (a fixed
// Options.W/H): New's Resize is the only one a bubble ever gets, so the poll
// chain nowplaying starts from it must run from controller.Init, not be
// dropped along with New's now-corrected cmd.
func TestPollsWithFixedPanelSize(t *testing.T) {
	m := &fakeMA{player: playerJSON("idle", "", "", "")}
	srv := httptest.NewServer(m)
	defer srv.Close()
	p := New(srv.URL, "secret", "visualizer")
	c, err := controller.New([]controller.Entry{{Bubble: p, Weight: 1}},
		controller.Options{W: 64, H: 32, FPS: 30, Brightness: 64})
	if err != nil {
		t.Fatalf("controller.New: %v", err)
	}
	flattenCtrl(c.Init())
	m.mu.Lock()
	body := m.body
	m.mu.Unlock()
	if body == "" {
		t.Error("music assistant got no /api request: the poll chain never started")
	}
}

func TestResizeArmsOnce(t *testing.T) {
	p, _ := start(t, &fakeMA{player: "null"})
	if cmd := p.Update(bubble.Resize{W: 32, H: 16}); cmd != nil {
		t.Error("a second Resize started a second poll chain")
	}
}

var red = color.RGBA{200, 0, 0, 255}

// redPNG is a plain red square.
func redPNG(side int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+3] = red.R, 255
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// tick draws one frame dt later.
func tick(p *NowPlaying, dt time.Duration) { p.Update(bubble.Tick{Dt: dt}) }

// lit reports whether any pixel in the columns x0..x1-1 is on.
func lit(frame [][]color.RGBA, x0, x1 int) bool {
	for _, row := range frame {
		for _, px := range row[x0:x1] {
			if px != (color.RGBA{}) {
				return true
			}
		}
	}
	return false
}

func TestCoverRequestAndPlacement(t *testing.T) {
	m := &fakeMA{cover: redPNG(80),
		player: playerJSON("playing", "One", "A", "http://ma.invalid:8095/imageproxy/abc123?size=512&fmt=jpg")}
	p, got := start(t, m)
	if len(got) != 1 {
		t.Fatalf("interludes = %v, want one, after the cover arrived", got)
	}
	if m.coverReq != "/imageproxy/abc123?size=80&fmt=png" {
		t.Errorf("cover request = %q: want our host, size 80, png", m.coverReq)
	}
	tick(p, 0)
	f := p.Frame()
	if f[1][1] != red || f[30][30] != red {
		t.Errorf("cover corners = %v %v, want red", f[1][1], f[30][30])
	}
	for _, xy := range [][2]int{{0, 0}, {31, 1}, {1, 31}, {32, 15}} {
		if f[xy[1]][xy[0]] != (color.RGBA{}) {
			t.Errorf("pixel %v is lit, want the 1 px padding dark", xy)
		}
	}
	if !lit(f, 33, 64) {
		t.Error("no text right of the cover")
	}
}

func TestCoverErrorStillAnnounces(t *testing.T) {
	m := &fakeMA{player: playerJSON("playing", "One", "A", "http://ma.invalid/imageproxy/gone")}
	p, got := start(t, m)
	if len(got) != 1 {
		t.Fatalf("interludes = %v, want one although the cover is a 404", got)
	}
	tick(p, 0)
	if f := p.Frame(); lit(f, 0, 1) || !lit(f, 1, 33) {
		t.Error("without a cover the text should take the width, after 1 px of margin")
	}
}

// An SVG cover (MA's imageproxy passes a provider's SVG fallback logo through
// whatever fmt is asked) is no cover: stdlib cannot decode it, the text takes
// the width.
func TestSVGCoverIsNoCover(t *testing.T) {
	m := &fakeMA{cover: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 80 50"><path d="M0 0h80v50H0z"/></svg>`),
		player: playerJSON("playing", "One", "A", "http://ma.invalid:8095/imageproxy/abc?size=0&fmt=svg")}
	p, got := start(t, m)
	if len(got) != 1 {
		t.Fatalf("interludes = %v, want one", got)
	}
	tick(p, 0)
	if f := p.Frame(); p.art != nil || lit(f, 0, 1) || !lit(f, 1, 33) {
		t.Error("an SVG cover should leave the text the full width")
	}
}

func TestNoImageURLUsesFullWidth(t *testing.T) {
	m := &fakeMA{player: playerJSON("playing", "Short", "A", "")}
	p, _ := start(t, m)
	tick(p, 0)
	f := p.Frame()
	if !lit(f, 1, 7) || lit(f, 40, 64) {
		t.Error("a short title should start at x=1 and not reach the right edge")
	}
}

func TestCoverURL(t *testing.T) {
	for raw, want := range map[string]string{
		"http://other:1/imageproxy/ab?size=512&fmt=jpg": "http://ma:8095/imageproxy/ab?size=80&fmt=png",
		"https://radio.example/logo.png":                "https://radio.example/logo.png",
		"file:///etc/passwd":                            "",
		"":                                              "",
	} {
		if got := coverURL("http://ma:8095", raw); got != want {
			t.Errorf("coverURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestShrinkAverages(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4)) // left half white, right half black
	for y := 0; y < 4; y++ {
		for x := 0; x < 2; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	got := shrink(img, 2)
	if got[0][0] != (color.RGBA{255, 255, 255, 255}) || got[1][1] != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("shrink = %v", got)
	}
	if one := shrink(img, 1); one[0][0].R != 127 {
		t.Errorf("1x1 = %v, want the mean 127", one[0][0])
	}
}

func TestLongTitleScrollsShortDoesNot(t *testing.T) {
	m := &fakeMA{player: playerJSON("playing", "A very long title indeed", "Abc", "")}
	p, _ := start(t, m)
	snap := func() (title, artist string) {
		var a, b strings.Builder
		for y, row := range p.Frame() {
			for _, px := range row[33:] {
				c := byte('.')
				if px != (color.RGBA{}) {
					c = '#'
				}
				if y < 16 {
					a.WriteByte(c)
				} else {
					b.WriteByte(c)
				}
			}
		}
		return a.String(), b.String()
	}
	tick(p, 0)
	t0, a0 := snap()
	tick(p, time.Second) // inside the 2 s pause
	if t1, _ := snap(); t1 != t0 {
		t.Error("the title moved during the pause")
	}
	tick(p, 2*time.Second) // 1 s past the pause: 12 px
	t2, a2 := snap()
	if t2 == t0 {
		t.Error("the long title did not scroll")
	}
	if a2 != a0 {
		t.Error("the short artist moved")
	}
	if lit(p.Frame(), 0, 1) { // no cover here: the text starts at x=1 and column 0 is the margin
		t.Error("scrolled text reached the margin")
	}
	p.Update(bubble.Activate{})
	tick(p, 0)
	if t3, _ := snap(); t3 != t0 {
		t.Error("Activate did not reset the scroll")
	}
}

func TestPlaceholder(t *testing.T) {
	p, _ := start(t, &fakeMA{status: http.StatusUnauthorized})
	tick(p, 0)
	if !lit(p.Frame(), 0, 64) {
		t.Error("nothing drawn for ma: 401")
	}
	if p.status != "ma: 401" {
		t.Errorf("status = %q", p.status)
	}
}

// hugePNG is a valid 1x1 PNG whose IHDR claims side x side instead, CRC fixed
// up to match: a few hundred bytes that would otherwise make a decoder
// allocate a side x side pixel buffer.
func hugePNG(side uint32) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	png.Encode(&buf, img)
	b := buf.Bytes()
	binary.BigEndian.PutUint32(b[16:20], side)
	binary.BigEndian.PutUint32(b[20:24], side)
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	return b
}

func TestFetchRejectsHugeImage(t *testing.T) {
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(hugePNG(5000))
	}))
	defer huge.Close()
	img, err := fetch(huge.URL)
	if img != nil || err == nil || !strings.Contains(err.Error(), "5000") {
		t.Errorf("fetch(huge) = %v, %v, want a nil image and an error naming 5000", img, err)
	}

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(redPNG(80))
	}))
	defer ok.Close()
	if img, err := fetch(ok.URL); img == nil || err != nil {
		t.Errorf("fetch(ok 80x80) = %v, %v, want a decoded image", img, err)
	}
}
