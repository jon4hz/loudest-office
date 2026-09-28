package api

import (
	"log"
	"net/http"
	"net/netip"
	"sync"
)

// TrustedProxy wraps next so that only requests from prefix are served and
// everything else is 403. Behind Home Assistant ingress the Supervisor
// (172.30.32.2) is the only caller that may reach the visualizer: Home
// Assistant has already logged the user in. The peer of the first ingress
// request (one carrying X-Ingress-Path) and every change by a named user
// (X-Remote-User-Name) are logged, so a wrong prefix and who changed what
// can be read off the app log. Only used headless, so the log goes to stderr.
func TrustedProxy(prefix netip.Prefix, next http.Handler) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ap, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil || !prefix.Contains(ap.Addr().Unmap()) { // Unmap: a dual-stack listener reports ::ffff:a.b.c.d
			http.Error(w, "forbidden: not the trusted proxy", http.StatusForbidden)
			return
		}
		if r.Header.Get("X-Ingress-Path") != "" {
			once.Do(func() { log.Printf("ingress: peer %s, trusted %s", ap.Addr().Unmap(), prefix) })
		}
		if user := r.Header.Get("X-Remote-User-Name"); user != "" && r.Method != http.MethodGet {
			log.Printf("ingress: %s: %s %s", user, r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}
