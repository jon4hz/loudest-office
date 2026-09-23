// Package nowplaying is the track bubble: cover, title and artist of what a
// Music Assistant player plays, laid out like the compact mode of Tronbyt's
// Spotify app. It polls MA's HTTP RPC, also while hidden, since that is how
// it notices a new track; it then asks the controller for an interlude.
package nowplaying

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"net/http"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
)

// pollEvery is the time between two polls; a var so that tests shorten it.
var pollEvery = 2 * time.Second

const maxPoll = 1 << 20

var pollClient = &http.Client{Timeout: 5 * time.Second}

// Settings is what the API patches.
type Settings struct {
	Seconds float64 `json:"seconds"` // how long a new track is shown, 0 = never
}

// pollMsg says the next poll is due.
type pollMsg struct{}

// polled is the answer to one poll.
type polled struct {
	playing              bool
	title, artist, image string
	code                 int // HTTP status of a failed poll
	err                  error
}

// NowPlaying shows the current track of one MA player.
type NowPlaying struct {
	url, token, player string
	set                Settings

	w, h  int
	frame [][]color.RGBA
	armed bool // the poll chain runs

	playing       bool
	title, artist string
	status        string // shown instead of a track: "ma?", "ma: 401"; "" after a good poll
	shown         string // title and artist last announced

	imageURL string         // current_media.image_url as MA sent it
	loading  bool           // its fetch is in flight: the announcement waits
	src      image.Image    // the cover as fetched
	art      [][]color.RGBA // src at (h-2)^2, nil = none
	t        time.Duration  // since Activate or the last new track: the scroll clock
}

var _ bubble.Bubble = (*NowPlaying)(nil)

// New returns the bubble for one player; url is MA's base URL without a
// trailing slash, token a long-lived token.
func New(url, token, player string) *NowPlaying {
	return &NowPlaying{url: url, token: token, player: player, set: Settings{Seconds: 10}, status: "ma?"}
}

func (p *NowPlaying) Name() string      { return "nowplaying" }
func (p *NowPlaying) Kind() bubble.Kind { return bubble.Track }

// Frame is the current picture, rows top to bottom.
func (p *NowPlaying) Frame() [][]color.RGBA { return p.frame }

func (p *NowPlaying) Settings() any { return p.set }

func (p *NowPlaying) Configure(raw json.RawMessage) error {
	set, err := bubble.Patch(p.set, raw)
	if err != nil {
		return err
	}
	if set.Seconds < 0 || set.Seconds > 600 {
		return fmt.Errorf("seconds must be 0..600, got %v", set.Seconds)
	}
	p.set = set
	return nil
}

func (p *NowPlaying) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case bubble.Resize:
		p.w, p.h = msg.W, msg.H
		p.frame = bubble.NewFrame(msg.W, msg.H)
		p.resized()
		p.draw()
		if !p.armed { // the first size starts the one poll chain
			p.armed = true
			return p.poll()
		}
	case bubble.Activate:
		p.t = 0
	case bubble.Tick:
		p.t += msg.Dt
		p.draw()
	case pollMsg:
		return p.poll()
	case polled:
		again := tea.Tick(pollEvery, func(time.Time) tea.Msg { return pollMsg{} })
		if msg.err != nil { // keeps the track that is shown
			if p.status = "ma?"; msg.code != 0 {
				p.status = fmt.Sprintf("ma: %d", msg.code)
			}
			return again
		}
		p.status, p.playing = "", msg.playing
		if msg.title != p.title || msg.artist != p.artist {
			p.title, p.artist, p.t = msg.title, msg.artist, 0
		}
		return tea.Batch(again, p.wantCover(msg.image), p.announce())
	case cover:
		return p.gotCover(msg)
	}
	return nil
}

// announce asks for the interlude once per track, and only while it plays: a
// skip while paused is announced on resume. It waits for a cover in flight,
// so the card does not open with a black one.
func (p *NowPlaying) announce() tea.Cmd {
	key := p.title + "\x00" + p.artist
	if !p.playing || p.title == "" || p.loading || key == p.shown {
		return nil
	}
	p.shown = key
	if p.set.Seconds <= 0 {
		return nil
	}
	d := time.Duration(p.set.Seconds * float64(time.Second))
	return func() tea.Msg { return bubble.Interlude{Name: p.Name(), For: d} }
}

// poll asks MA off the frame loop. The chain is poll, polled, tick, poll: at
// most one is in flight.
func (p *NowPlaying) poll() tea.Cmd {
	url, token, player := p.url, p.token, p.player
	return func() tea.Msg { return get(url, token, player) }
}

// get is one players/get. MA resolves sync groups: a member answers with
// what its group plays.
func get(url, token, player string) polled {
	body, _ := json.Marshal(map[string]any{"command": "players/get", "args": map[string]string{"player_id": player}})
	req, err := http.NewRequest(http.MethodPost, url+"/api", bytes.NewReader(body))
	if err != nil {
		return polled{err: err}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := pollClient.Do(req)
	if err != nil {
		return polled{err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return polled{code: resp.StatusCode, err: fmt.Errorf("music assistant: %s", resp.Status)}
	}
	var pl *struct {
		State string `json:"playback_state"`
		Media *struct {
			Title  string `json:"title"`
			Artist string `json:"artist"`
			Image  string `json:"image_url"`
		} `json:"current_media"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPoll)).Decode(&pl); err != nil {
		return polled{err: err}
	}
	if pl == nil { // MA answers null for a player it does not know
		return polled{err: fmt.Errorf("music assistant: unknown player %q", player)}
	}
	out := polled{playing: pl.State == "playing"}
	if pl.Media != nil {
		out.title, out.artist, out.image = pl.Media.Title, pl.Media.Artist, pl.Media.Image
	}
	return out
}
