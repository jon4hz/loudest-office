package api

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
)

func TestTrustedProxy(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	h := TrustedProxy(netip.MustParsePrefix("172.30.32.2/32"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	get := func(remote string, hdr ...string) int {
		req := httptest.NewRequest("PATCH", "/api/v1/controller", nil)
		req.RemoteAddr = remote
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := get("172.30.32.2:4321", "X-Ingress-Path", "/api/hassio_ingress/x", "X-Remote-User-Name", "jonah"); c != http.StatusNoContent {
		t.Fatalf("supervisor: %d", c)
	}
	if c := get("[::ffff:172.30.32.2]:4321", "X-Ingress-Path", "/api/hassio_ingress/x"); c != http.StatusNoContent {
		t.Fatalf("v4-mapped supervisor: %d", c)
	}
	for _, remote := range []string{"192.168.16.5:1", "172.30.32.1:1", "[::1]:1", "garbage"} {
		if c := get(remote); c != http.StatusForbidden {
			t.Fatalf("%s: %d, want 403", remote, c)
		}
	}
	out := logs.String()
	if strings.Count(out, "peer 172.30.32.2") != 1 || !strings.Contains(out, "jonah: PATCH /api/v1/controller") {
		t.Fatalf("log:\n%s", out)
	}
}
