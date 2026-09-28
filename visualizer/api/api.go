// Package api is the HTTP surface over the controller: twenty routes under
// /api/v1, stdlib net/http only. No handler touches controller state
// directly; every one runs a closure inside the controller's Update loop
// through the injected do, so nothing here needs a lock.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jon4hz/loudest-office/visualizer/bubble"
	"github.com/jon4hz/loudest-office/visualizer/controller"
	"github.com/jon4hz/loudest-office/visualizer/music/bars"
	"github.com/jon4hz/loudest-office/visualizer/palette"
)

// maxBody caps a request body, so a crashed or hostile sender cannot wedge a
// handler with an unbounded read.
const maxBody = 64 << 10

type server struct {
	do func(controller.Call) error
}

// New returns the API handler. do runs a Call inside the controller's
// Update loop, returning an error when it could not (e.g. past shutdown).
func New(do func(controller.Call) error) http.Handler {
	s := &server{do: do}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/state", s.getState)
	mux.HandleFunc("PATCH /api/v1/controller", s.patchController)
	mux.HandleFunc("GET /api/v1/bubbles", s.getBubbles)
	mux.HandleFunc("PATCH /api/v1/bubbles/{name}", s.patchBubble)
	mux.HandleFunc("POST /api/v1/bubbles/{name}/show", s.postShow)
	mux.HandleFunc("GET /api/v1/options", s.getOptions)
	mux.HandleFunc("POST /api/v1/next", s.postNext)
	mux.HandleFunc("POST /api/v1/events", s.postEvent)
	mux.HandleFunc("DELETE /api/v1/events/{id}", s.deleteEvent)
	mux.HandleFunc("GET /api/v1/palettes", s.getPalettes)
	mux.HandleFunc("PUT /api/v1/palettes/{name}", s.putPalette)
	mux.HandleFunc("DELETE /api/v1/palettes/{name}", s.deletePalette)
	mux.HandleFunc("GET /api/v1/presets", s.getPresets)
	mux.HandleFunc("PUT /api/v1/presets/{name}", s.putPreset)
	mux.HandleFunc("POST /api/v1/presets/{name}/load", s.loadPreset)
	mux.HandleFunc("DELETE /api/v1/presets/{name}", s.deletePreset)
	mux.HandleFunc("GET /api/v1/panels", s.getPanels)
	mux.HandleFunc("PUT /api/v1/panels/{name}", s.putPanel)
	mux.HandleFunc("PATCH /api/v1/panels/{name}", s.patchPanel)
	mux.HandleFunc("DELETE /api/v1/panels/{name}", s.deletePanel)
	return mux
}

// call runs fn inside the controller and writes its result: 503 when do
// itself fails, 404 for controller.ErrNotFound, 409 for palette.ErrShipped
// and controller.ErrInUse, 500 for controller.ErrSave, 400 for any other
// error, else status with fn's value marshalled to JSON. The marshal happens
// inside do, since the value may alias live controller state.
func (s *server) call(w http.ResponseWriter, status int, fn func(*controller.Controller) (any, error)) {
	var body []byte
	var err error
	if derr := s.do(func(c *controller.Controller) {
		var v any
		if v, err = fn(c); err == nil && v != nil {
			body, err = json.Marshal(v)
		}
	}); derr != nil {
		fail(w, http.StatusServiceUnavailable, derr)
		return
	}
	if err != nil {
		code := http.StatusBadRequest
		switch {
		case errors.Is(err, controller.ErrNotFound):
			code = http.StatusNotFound
		case errors.Is(err, controller.ErrSave):
			code = http.StatusInternalServerError
		case errors.Is(err, palette.ErrShipped), errors.Is(err, controller.ErrInUse):
			code = http.StatusConflict
		}
		fail(w, code, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

// fail writes the JSON error shape.
func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// decode reads r's body size-limited and JSON-decodes it into v, rejecting
// unknown fields and anything after the one JSON value (trailing whitespace
// is fine; Decode alone would silently ignore trailing garbage).
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("unexpected data after JSON value")
	}
	return nil
}

func (s *server) getState(w http.ResponseWriter, r *http.Request) {
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) {
		return c.State(), nil
	})
}

// patchController passes the raw body straight to c.Patch, which does its
// own decoding and validation.
func (s *server) patchController(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) {
		if err := c.Patch(raw); err != nil {
			return nil, err
		}
		return c.State().Settings, nil
	})
}

func (s *server) getBubbles(w http.ResponseWriter, r *http.Request) {
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) {
		return c.Bubbles(), nil
	})
}

// patchBubble decodes the {weight, settings} envelope and hands both,
// optional, to c.PatchBubble.
func (s *server) patchBubble(w http.ResponseWriter, r *http.Request) {
	var env struct {
		Weight   *int            `json:"weight"`
		Settings json.RawMessage `json:"settings"`
	}
	if err := decode(w, r, &env); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	name := r.PathValue("name")
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) {
		if err := c.PatchBubble(name, env.Weight, env.Settings); err != nil {
			return nil, err
		}
		return bubbleInfo(c, name), nil
	})
}

// bubbleInfo is name's entry from c.Bubbles(); the caller only asks after
// confirming name is registered.
func bubbleInfo(c *controller.Controller, name string) controller.BubbleInfo {
	for _, b := range c.Bubbles() {
		if b.Name == name {
			return b
		}
	}
	return controller.BubbleInfo{}
}

// defaultShow is how long postShow shows a bubble when the body names no time.
const defaultShow = 10

// postShow shows a bubble for {seconds}, 10 without a body or with 0.
func (s *server) postShow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Seconds float64 `json:"seconds"`
	}
	if err := decode(w, r, &body); err != nil && !errors.Is(err, io.EOF) { // EOF: no body at all
		fail(w, http.StatusBadRequest, err)
		return
	}
	if body.Seconds == 0 {
		body.Seconds = defaultShow
	}
	if body.Seconds < 0 || body.Seconds > 600 {
		fail(w, http.StatusBadRequest, fmt.Errorf("seconds must be 0..600, got %v", body.Seconds))
		return
	}
	name, d := r.PathValue("name"), time.Duration(body.Seconds*float64(time.Second))
	s.call(w, http.StatusNoContent, func(c *controller.Controller) (any, error) {
		return nil, c.Show(name, d)
	})
}

// options is the GET /options document: static except for Bubbles, which
// needs the controller's registered bubbles.
type options struct {
	Palettes []string                          `json:"palettes"`
	Layouts  []string                          `json:"layouts"`
	Peaks    []string                          `json:"peaks"`
	Levels   []string                          `json:"levels"`
	Orders   []string                          `json:"orders"`
	Bubbles  map[string]map[string]bubble.Hint `json:"bubbles"`
}

func (s *server) getOptions(w http.ResponseWriter, r *http.Request) {
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) {
		return options{
			Palettes: palette.Names(), Layouts: bars.Layouts, Peaks: bars.Peaks,
			Levels: bubble.Levels, Orders: controller.Orders, Bubbles: c.Hints(),
		}, nil
	})
}

func (s *server) postNext(w http.ResponseWriter, r *http.Request) {
	s.call(w, http.StatusNoContent, func(c *controller.Controller) (any, error) {
		c.Next()
		return nil, nil
	})
}

// postEvent validates text length, level and ttl before touching the
// controller, so a bad request never reaches AddEvent.
func (s *server) postEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID    string  `json:"id"`
		Text  string  `json:"text"`
		Level string  `json:"level"`
		TTL   float64 `json:"ttl"`
	}
	if err := decode(w, r, &body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if n := utf8.RuneCountInString(body.Text); n < 1 || n > 256 {
		fail(w, http.StatusBadRequest, fmt.Errorf("text must be 1..256 characters, got %d", n))
		return
	}
	if !slices.Contains(bubble.Levels, body.Level) {
		fail(w, http.StatusBadRequest, fmt.Errorf("unknown level %q, have: %s", body.Level, strings.Join(bubble.Levels, ", ")))
		return
	}
	if body.TTL <= 0 || body.TTL > 86400 {
		fail(w, http.StatusBadRequest, fmt.Errorf("ttl must be > 0 and <= 86400 seconds, got %v", body.TTL))
		return
	}
	ev := bubble.Event{
		ID: body.ID, Text: body.Text, Level: body.Level,
		Expires: time.Now().Add(time.Duration(body.TTL * float64(time.Second))),
	}
	s.call(w, http.StatusCreated, func(c *controller.Controller) (any, error) {
		id, err := c.AddEvent(ev)
		if err != nil {
			return nil, err
		}
		return map[string]string{"id": id}, nil
	})
}

func (s *server) deleteEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.call(w, http.StatusNoContent, func(c *controller.Controller) (any, error) {
		return nil, c.RemoveEvent(id)
	})
}

func (s *server) getPalettes(w http.ResponseWriter, r *http.Request) {
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) { return c.Palettes(), nil })
}

// putPalette takes {stops, axis, peak}; the name comes from the path.
func (s *server) putPalette(w http.ResponseWriter, r *http.Request) {
	var cp palette.Custom
	if err := decode(w, r, &cp); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	cp.Name = r.PathValue("name")
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) { return c.PutPalette(cp) })
}

func (s *server) deletePalette(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.call(w, http.StatusNoContent, func(c *controller.Controller) (any, error) { return nil, c.DeletePalette(name) })
}

func (s *server) getPresets(w http.ResponseWriter, r *http.Request) {
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) { return c.Presets(), nil })
}

// putPreset saves the current state under the name, or the body when there
// is one; the controller checks the body's shape.
func (s *server) putPreset(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	name := r.PathValue("name")
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) {
		if err := c.SavePreset(name, raw); err != nil {
			return nil, err
		}
		return map[string]string{"name": strings.TrimSpace(name)}, nil
	})
}

func (s *server) loadPreset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) {
		if err := c.LoadPreset(name); err != nil {
			return nil, err
		}
		return c.State(), nil
	})
}

func (s *server) deletePreset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.call(w, http.StatusNoContent, func(c *controller.Controller) (any, error) { return nil, c.DeletePreset(name) })
}

func (s *server) getPanels(w http.ResponseWriter, r *http.Request) {
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) { return c.Panels(), nil })
}

// putPanel declares a panel from {kind, address, baud, w, h, fps, brightness,
// idle_brightness}; the name comes from the path, a name in the body is a
// 400. Missing fields take the defaults, the controller drops those that do
// not fit the kind.
func (s *server) putPanel(w http.ResponseWriter, r *http.Request) {
	// what the body leaves out; 30 fps like the controller's own default
	spec := controller.PanelSpec{FPS: 30, Baud: controller.DefaultBaud, Brightness: controller.DefaultBrightness, IdleBrightness: controller.DefaultBrightness}
	if err := decode(w, r, &spec); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if spec.Name != "" {
		fail(w, http.StatusBadRequest, fmt.Errorf("the name comes from the path"))
		return
	}
	spec.Name = r.PathValue("name")
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) { return c.PutPanel(spec) })
}

// patchPanel hands the raw body to c.PatchPanel, which decodes and validates.
func (s *server) patchPanel(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	name := r.PathValue("name")
	s.call(w, http.StatusOK, func(c *controller.Controller) (any, error) { return c.PatchPanel(name, raw) })
}

func (s *server) deletePanel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.call(w, http.StatusNoContent, func(c *controller.Controller) (any, error) { return nil, c.DeletePanel(name) })
}
