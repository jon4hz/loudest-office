package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestServesIndexAssetsAndFallback(t *testing.T) {
	h := handler(fstest.MapFS{
		"index.html":      {Data: []byte("<title>ui</title>")},
		"assets/app-1.js": {Data: []byte("js")},
	})
	for _, path := range []string{"/", "/index.html", "/anything"} {
		rec := get(h, path)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>ui</title>") || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: %d %q %q", path, rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
		}
	}
	rec := get(h, "/assets/app-1.js")
	if rec.Code != 200 || rec.Body.String() != "js" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
}

func TestUnbuiltUIIs503(t *testing.T) {
	rec := get(handler(fstest.MapFS{".gitkeep": {}}), "/")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "task web:build") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}
