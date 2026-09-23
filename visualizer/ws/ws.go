// Package ws streams the frames the controller renders to websocket clients,
// GET /api/v1/frames on the API port. Every message is one frame:
//
//	w u16 | h u16 | RGB888 pixels, row by row
//
// little-endian. A client that cannot keep up misses frames, it never holds
// up the panel. A client may send its own size as a text message
// {"w":..,"h":..} in pixels; without a panel the controller renders at that
// size, like it follows the local terminal.
package ws

import (
	"context"
	"encoding/binary"
	"encoding/json"
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

// maxSize bounds a client's requested size, in pixels each way.
const maxSize = 1024

// Hub fans frames out to the connected clients; the zero value is ready.
type Hub struct {
	// OnSize is told a client's size, in pixels, whenever it sends one.
	// ponytail: the last client to resize wins, one size per controller.
	OnSize func(w, h int)

	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

// size is what a client sends to ask for a render size.
type size struct{ W, H int }

// Send encodes frame and queues it for every client, dropping the frame a
// client has not read yet. It runs inside the controller's tick, so it must
// not block.
func (h *Hub) Send(frame [][]color.RGBA) {
	b := Encode(frame)
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case <-ch: // stale frame out
		default:
		}
		ch <- b // cap 1, just drained under the lock: never blocks
	}
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
	go func() { // sizes in; the read also answers pings and notices the client leaving
		defer cancel()
		for {
			typ, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var s size
			if typ != websocket.MessageText || json.Unmarshal(b, &s) != nil ||
				s.W < 1 || s.H < 1 || s.W > maxSize || s.H > maxSize {
				continue // not a size: ignored, a stray message must not drop the stream
			}
			if h.OnSize != nil {
				h.OnSize(s.W, s.H)
			}
		}
	}()

	ch := make(chan []byte, 1)
	h.mu.Lock()
	if h.clients == nil {
		h.clients = map[chan []byte]struct{}{}
	}
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, ch)
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

// Dial connects to a frame stream, ws://host:port/api/v1/frames, and reads
// frames until ctx ends or the server closes.
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

// Resize asks the server to render w x h pixels.
func (cl *Client) Resize(ctx context.Context, w, h int) error {
	b, _ := json.Marshal(size{w, h})
	return cl.c.Write(ctx, websocket.MessageText, b)
}

// Close drops the connection.
func (cl *Client) Close() { cl.c.CloseNow() }
