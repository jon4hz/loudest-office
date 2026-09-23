package api_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jon4hz/loudest-office/visualizer/alert"
	"github.com/jon4hz/loudest-office/visualizer/api"
	"github.com/jon4hz/loudest-office/visualizer/clock"
	"github.com/jon4hz/loudest-office/visualizer/controller"
	"github.com/jon4hz/loudest-office/visualizer/music/bars"
	"github.com/jon4hz/loudest-office/visualizer/palette"
)

// newTestServer builds a real controller over real bubbles, backed by a
// state file in a temp dir, and an api.Handler whose do runs synchronously.
func newTestServer(t *testing.T) http.Handler {
	h, _ := newTestServerPath(t)
	return h
}

func newTestServerPath(t *testing.T) (http.Handler, string) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "state.json")
	c, err := controller.New([]controller.Entry{
		{Bubble: bars.New(), Weight: 8},
		{Bubble: clock.New(), Weight: 1},
		{Bubble: alert.New(), Weight: 1},
	}, controller.Options{W: 64, H: 32, FPS: 30, Brightness: 64, StatePath: statePath})
	if err != nil {
		t.Fatalf("controller.New: %v", err)
	}
	do := func(f controller.Call) error { f(c); return nil }
	return api.New(do), statePath
}

func doReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		r = bytes.NewReader(b)
	case string:
		r = strings.NewReader(b)
	default:
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, r)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, want, rec.Body.String())
	}
}

func TestGetState(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "GET", "/api/v1/state", nil)
	wantStatus(t, rec, http.StatusOK)
	var st controller.State
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.W != 64 || st.H != 32 {
		t.Fatalf("state = %+v, want 64x32", st)
	}
}

func TestPatchController(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "PATCH", "/api/v1/controller", map[string]any{"brightness": 10})
	wantStatus(t, rec, http.StatusOK)
	set := decodeJSON[controller.Settings](t, rec)
	if set.Brightness != 10 {
		t.Fatalf("brightness = %d, want 10", set.Brightness)
	}
}

func TestPatchControllerBadJSON(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "PATCH", "/api/v1/controller", "{not json")
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestPatchControllerUnknownShow(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "PATCH", "/api/v1/controller", map[string]any{"show": "nope"})
	wantStatus(t, rec, http.StatusNotFound)
}

func TestGetBubbles(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "GET", "/api/v1/bubbles", nil)
	wantStatus(t, rec, http.StatusOK)
	infos := decodeJSON[[]controller.BubbleInfo](t, rec)
	if len(infos) != 3 {
		t.Fatalf("len(infos) = %d, want 3", len(infos))
	}
}

func TestPatchBubble(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "PATCH", "/api/v1/bubbles/bars", map[string]any{"weight": 3})
	wantStatus(t, rec, http.StatusOK)
	info := decodeJSON[controller.BubbleInfo](t, rec)
	if info.Weight != 3 {
		t.Fatalf("weight = %d, want 3", info.Weight)
	}

	// shows in the next GET /bubbles
	rec2 := doReq(t, h, "GET", "/api/v1/bubbles", nil)
	infos := decodeJSON[[]controller.BubbleInfo](t, rec2)
	found := false
	for _, b := range infos {
		if b.Name == "bars" {
			found = true
			if b.Weight != 3 {
				t.Fatalf("bars weight = %d, want 3", b.Weight)
			}
		}
	}
	if !found {
		t.Fatal("bars not found in GET /bubbles")
	}
}

func TestPatchBubbleSettingsPersistsToStateFile(t *testing.T) {
	h, statePath := newTestServerPath(t)
	rec := doReq(t, h, "PATCH", "/api/v1/bubbles/bars", map[string]any{"settings": map[string]any{"palettes": []string{"rainbow"}}})
	wantStatus(t, rec, http.StatusOK)

	rec2 := doReq(t, h, "GET", "/api/v1/bubbles", nil)
	infos := decodeJSON[[]controller.BubbleInfo](t, rec2)
	var raw json.RawMessage
	for _, b := range infos {
		if b.Name == "bars" {
			buf, err := json.Marshal(b.Settings)
			if err != nil {
				t.Fatal(err)
			}
			raw = buf
		}
	}
	if !strings.Contains(string(raw), "rainbow") {
		t.Fatalf("bars settings = %s, want to contain rainbow", raw)
	}

	onDisk, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if !strings.Contains(string(onDisk), "rainbow") {
		t.Fatalf("state file = %s, want to contain rainbow", onDisk)
	}
}

func TestPatchBubbleUnknownName(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "PATCH", "/api/v1/bubbles/nope", map[string]any{"weight": 1})
	wantStatus(t, rec, http.StatusNotFound)
}

func TestPatchBubbleUnknownField(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "PATCH", "/api/v1/bubbles/bars", map[string]any{"bogus": 1})
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestPatchBubbleUnknownPalette(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "PATCH", "/api/v1/bubbles/bars", map[string]any{"settings": map[string]any{"palettes": []string{"nope"}}})
	wantStatus(t, rec, http.StatusBadRequest)
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, want := range []string{"rainbow", "drift"} {
		if !strings.Contains(body["error"], want) {
			t.Fatalf("error %q does not name allowed palette %q", body["error"], want)
		}
	}
}

func TestPatchBubbleTrailingGarbage(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "PATCH", "/api/v1/bubbles/bars", `{"weight":3}garbage`)
	wantStatus(t, rec, http.StatusBadRequest)

	// weight unchanged
	infos := decodeJSON[[]controller.BubbleInfo](t, doReq(t, h, "GET", "/api/v1/bubbles", nil))
	for _, b := range infos {
		if b.Name == "bars" && b.Weight != 8 {
			t.Fatalf("bars weight = %d, want unchanged 8", b.Weight)
		}
	}
}

func TestPatchBubbleTrailingWhitespaceAccepted(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "PATCH", "/api/v1/bubbles/bars", "{\"weight\":3}\n\t \n")
	wantStatus(t, rec, http.StatusOK)
	info := decodeJSON[controller.BubbleInfo](t, rec)
	if info.Weight != 3 {
		t.Fatalf("weight = %d, want 3", info.Weight)
	}
}

func TestGetOptions(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "GET", "/api/v1/options", nil)
	wantStatus(t, rec, http.StatusOK)
	var opts struct {
		Palettes []string `json:"palettes"`
		Layouts  []string `json:"layouts"`
		Peaks    []string `json:"peaks"`
		Levels   []string `json:"levels"`
		Orders   []string `json:"orders"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &opts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(opts.Palettes) != len(palette.Palettes) {
		t.Fatalf("palettes = %d, want %d", len(opts.Palettes), len(palette.Palettes))
	}
	wantLayouts := []string{"stacked", "side", "mirror", "hmirror", "single"}
	if !slicesEqual(opts.Layouts, wantLayouts) {
		t.Fatalf("layouts = %v, want %v", opts.Layouts, wantLayouts)
	}
	wantPeaks := []string{"fall", "fly", "beat", "none"}
	if !slicesEqual(opts.Peaks, wantPeaks) {
		t.Fatalf("peaks = %v, want %v", opts.Peaks, wantPeaks)
	}
	if !slicesEqual(opts.Levels, []string{"info", "warning", "critical"}) {
		t.Fatalf("levels = %v", opts.Levels)
	}
	if !slicesEqual(opts.Orders, []string{"random", "sequence"}) {
		t.Fatalf("orders = %v", opts.Orders)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPostNext(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "POST", "/api/v1/next", nil)
	wantStatus(t, rec, http.StatusNoContent)
}

func TestPostAndDeleteEvent(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "POST", "/api/v1/events", map[string]any{"text": "hello", "level": "info", "ttl": 5})
	wantStatus(t, rec, http.StatusCreated)
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ID == "" {
		t.Fatal("empty id")
	}

	st := decodeJSON[controller.State](t, doReq(t, h, "GET", "/api/v1/state", nil))
	if len(st.Events) != 1 || st.Events[0].Text != "hello" {
		t.Fatalf("events = %+v", st.Events)
	}

	rec2 := doReq(t, h, "DELETE", "/api/v1/events/"+body.ID, nil)
	wantStatus(t, rec2, http.StatusNoContent)

	st2 := decodeJSON[controller.State](t, doReq(t, h, "GET", "/api/v1/state", nil))
	if len(st2.Events) != 0 {
		t.Fatalf("events after delete = %+v", st2.Events)
	}
}

func TestPostEventBadLevel(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "POST", "/api/v1/events", map[string]any{"text": "hi", "level": "urgent", "ttl": 5})
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestPostEventZeroTTL(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "POST", "/api/v1/events", map[string]any{"text": "hi", "level": "info", "ttl": 0})
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestPostEventTooLongText(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "POST", "/api/v1/events", map[string]any{"text": strings.Repeat("x", 257), "level": "info", "ttl": 5})
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestPostEventEmptyText(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "POST", "/api/v1/events", map[string]any{"text": "", "level": "info", "ttl": 5})
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestPostEventUnknownField(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "POST", "/api/v1/events", map[string]any{"text": "hi", "level": "info", "ttl": 5, "bogus": 1})
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestPostEventTrailingGarbage(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "POST", "/api/v1/events", `{"text":"hi","level":"info","ttl":5}garbage`)
	wantStatus(t, rec, http.StatusBadRequest)

	st := decodeJSON[controller.State](t, doReq(t, h, "GET", "/api/v1/state", nil))
	if len(st.Events) != 0 {
		t.Fatalf("events = %+v, want none queued", st.Events)
	}
}

func TestPostEventTrailingWhitespaceAccepted(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "POST", "/api/v1/events", "{\"text\":\"hi\",\"level\":\"info\",\"ttl\":5}\n\t \n")
	wantStatus(t, rec, http.StatusCreated)
}

func TestDeleteEventUnknown(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, "DELETE", "/api/v1/events/nope", nil)
	wantStatus(t, rec, http.StatusNotFound)
}

// TestPatchControllerSaveFailureIs500 points StatePath at a path whose parent
// does not exist, so save() fails after the settings are already live; the
// handler must report 500, not 400, and the change must still show in GET.
func TestPatchControllerSaveFailureIs500(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "nope", "state.json") // parent dir missing
	c, err := controller.New([]controller.Entry{{Bubble: bars.New(), Weight: 8}},
		controller.Options{W: 64, H: 32, FPS: 30, Brightness: 64, StatePath: statePath})
	if err != nil {
		t.Fatalf("controller.New: %v", err)
	}
	do := func(f controller.Call) error { f(c); return nil }
	h := api.New(do)

	rec := doReq(t, h, "PATCH", "/api/v1/controller", map[string]any{"brightness": 10})
	wantStatus(t, rec, http.StatusInternalServerError)
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("body = %s, want a JSON error", rec.Body.String())
	}

	st := decodeJSON[controller.State](t, doReq(t, h, "GET", "/api/v1/state", nil))
	if st.Settings.Brightness != 10 {
		t.Fatalf("brightness = %d, want 10: the change must be live despite the save failure", st.Settings.Brightness)
	}
}

func TestServiceUnavailable(t *testing.T) {
	h := api.New(func(controller.Call) error { return errBoom })
	rec := doReq(t, h, "GET", "/api/v1/state", nil)
	wantStatus(t, rec, http.StatusServiceUnavailable)
}

var errBoom = errors.New("boom")

func TestPostShow(t *testing.T) {
	h := newTestServer(t)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/clock/show", nil), http.StatusNoContent)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/clock/show", `{"seconds":5}`), http.StatusNoContent)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/nope/show", nil), http.StatusNotFound)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/alert/show", nil), http.StatusBadRequest)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/clock/show", `{"seconds":601}`), http.StatusBadRequest)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/clock/show", `{"seconds":-1}`), http.StatusBadRequest)
	wantStatus(t, doReq(t, h, "POST", "/api/v1/bubbles/clock/show", `{"secs":5}`), http.StatusBadRequest)

	wantStatus(t, doReq(t, h, "PATCH", "/api/v1/controller", `{"show":"bars"}`), http.StatusOK)
	rec := doReq(t, h, "POST", "/api/v1/bubbles/clock/show", nil)
	wantStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "bars") {
		t.Errorf("body %s does not name the pin", rec.Body.String())
	}
}
