package ws

import (
	"context"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	frame := [][]color.RGBA{{{1, 2, 3, 255}, {4, 5, 6, 255}}}
	got, err := Decode(Encode(frame))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0]) != 2 || got[0][1] != frame[0][1] {
		t.Fatalf("got %v, want %v", got, frame)
	}
	if _, err := Decode([]byte{2, 0, 1, 0, 9}); err == nil {
		t.Fatal("short message decoded")
	}
}

// A client sees the latest frame; one sent before it connected or while it
// was not reading is dropped, not queued.
func TestHubStreamsLatest(t *testing.T) {
	var h Hub
	srv := httptest.NewServer(&h)
	defer srv.Close()
	h.Send([][]color.RGBA{{{9, 9, 9, 255}}}) // no client yet

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cl, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/frames")
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	deadline := time.After(2 * time.Second)
	for { // the client registers after Dial returns: send until it hears one
		h.Send([][]color.RGBA{{{1, 2, 3, 255}, {4, 5, 6, 255}}})
		select {
		case f := <-cl.Frames:
			if len(f) != 1 || len(f[0]) != 2 || f[0][0] != (color.RGBA{1, 2, 3, 255}) {
				t.Fatalf("got %v", f)
			}
			return
		case err := <-cl.Err:
			t.Fatal(err)
		case <-deadline:
			t.Fatal("no frame")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Hubs keep one stream per panel name: a frame for "a" never reaches "b",
// and Clients counts per name.
func TestHubsFanOutPerName(t *testing.T) {
	var hubs Hubs
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/panels/{name}/frames", &hubs)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/panels/a/frames")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for hubs.Clients("a") != 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if hubs.Clients("b") != 0 {
		t.Fatal("b has a client")
	}
	deadline := time.After(2 * time.Second)
	for {
		hubs.Send("b", [][]color.RGBA{{{7, 7, 7, 255}}})
		hubs.Send("a", [][]color.RGBA{{{1, 2, 3, 255}}})
		select {
		case f := <-a.Frames:
			if f[0][0] != (color.RGBA{1, 2, 3, 255}) {
				t.Fatalf("a got %v", f)
			}
			return
		case err := <-a.Err:
			t.Fatal(err)
		case <-deadline:
			t.Fatal("no frame")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// An unwatched hub drops the frame without encoding it.
func TestHubSendWithoutClients(t *testing.T) {
	var h Hub
	h.Send([][]color.RGBA{{{R: 1}}})
}
