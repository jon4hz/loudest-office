// Package ws streams the frames the controller renders to websocket clients,
// GET /api/v1/panels/{name}/frames on the API port. Every message is one
// frame of one panel:
//
//	w u16 | h u16 | RGB888 pixels, row by row
//
// little-endian. A client that cannot keep up misses frames, it never holds
// up the panel.
package ws

import (
	"context"
	"encoding/binary"
	"fmt"
	"image/color"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// writeTimeout bounds one frame write, so a client that stopped reading is
// dropped instead of pinned.
const writeTimeout = 5 * time.Second

// Hub fans frames out to the connected clients; the zero value is ready.
type Hub struct {
	mu    sync.Mutex
	conns map[chan []byte]struct{}
}

// Send encodes frame and queues it for every client, dropping the frame a
// client has not read yet. It runs inside the controller's tick, so it must
// not block. An unwatched panel is not encoded.
func (h *Hub) Send(frame [][]color.RGBA) {
	if h.clients() == 0 {
		return
	}
	b := Encode(frame)
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.conns {
		select {
		case <-ch: // stale frame out
		default:
		}
		ch <- b // cap 1, just drained under the lock: never blocks
	}
}

// clients is how many are connected.
func (h *Hub) clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

// ServeHTTP upgrades to a websocket and streams frames until the client
// goes away. Origins are not checked: the API has no auth either.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return // Accept wrote the error
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() { // the read answers pings and notices the client leaving; anything sent is ignored
		defer cancel()
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}()

	ch := make(chan []byte, 1)
	h.mu.Lock()
	if h.conns == nil {
		h.conns = map[chan []byte]struct{}{}
	}
	h.conns[ch] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.conns, ch)
		h.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case b := <-ch:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.Write(wctx, websocket.MessageBinary, b)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// Encode is one frame as the wire message.
func Encode(frame [][]color.RGBA) []byte {
	h := len(frame)
	var w int
	if h > 0 {
		w = len(frame[0])
	}
	b := make([]byte, 4, 4+3*w*h)
	binary.LittleEndian.PutUint16(b, uint16(w))
	binary.LittleEndian.PutUint16(b[2:], uint16(h))
	for _, row := range frame {
		for _, c := range row {
			b = append(b, c.R, c.G, c.B)
		}
	}
	return b
}

// Decode is the frame of a wire message.
func Decode(b []byte) ([][]color.RGBA, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("frame message of %d bytes, need at least 4", len(b))
	}
	w, h := int(binary.LittleEndian.Uint16(b)), int(binary.LittleEndian.Uint16(b[2:]))
	if len(b) != 4+3*w*h {
		return nil, fmt.Errorf("%dx%d frame message of %d bytes, want %d", w, h, len(b), 4+3*w*h)
	}
	frame := make([][]color.RGBA, h)
	p := b[4:]
	for y := range frame {
		frame[y] = make([]color.RGBA, w)
		for x := range frame[y] {
			frame[y][x] = color.RGBA{p[0], p[1], p[2], 255}
			p = p[3:]
		}
	}
	return frame, nil
}

// Client is a connection to a frame stream.
type Client struct {
	c      *websocket.Conn
	Frames <-chan [][]color.RGBA // closed when the stream ends; Err then says why
	Err    <-chan error
}

// Dial connects to a frame stream, ws://host:port/api/v1/panels/name/frames,
// and reads frames until ctx ends or the server closes.
func Dial(ctx context.Context, url string) (*Client, error) {
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(-1) // a frame is w*h*3 bytes, the server decides the size
	frames, errc := make(chan [][]color.RGBA), make(chan error, 1)
	go func() {
		defer close(frames)
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				errc <- err
				return
			}
			f, err := Decode(b)
			if err != nil {
				errc <- err
				return
			}
			select {
			case frames <- f:
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			}
		}
	}()
	return &Client{c: c, Frames: frames, Err: errc}, nil
}

// Close drops the connection.
func (cl *Client) Close() { cl.c.CloseNow() }

// Hubs is one Hub per panel name, made on first use; the zero value is ready.
type Hubs struct {
	mu   sync.Mutex
	hubs map[string]*Hub
}

// hub is the panel's hub, made when missing.
func (h *Hubs) hub(name string) *Hub {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hubs == nil {
		h.hubs = map[string]*Hub{}
	}
	if h.hubs[name] == nil {
		h.hubs[name] = &Hub{}
	}
	return h.hubs[name]
}

// Send queues frame for the panel's clients; see Hub.Send.
func (h *Hubs) Send(name string, frame [][]color.RGBA) { h.hub(name).Send(frame) }

// Clients is how many are connected to the panel's stream.
func (h *Hubs) Clients(name string) int { return h.hub(name).clients() }

// ServeHTTP streams the panel named by the {name} path value.
func (h *Hubs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.hub(r.PathValue("name")).ServeHTTP(w, r)
}
