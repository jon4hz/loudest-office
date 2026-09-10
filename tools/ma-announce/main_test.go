package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRun(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("bad request %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		got = append(got, m)
		if m["command"] == "players/get_by_name" {
			io.WriteString(w, `{"player_id":"aa:bb","name":"Office"}`)
			return
		}
		io.WriteString(w, "null")
	}))
	defer srv.Close()

	if err := run(srv.URL, "tok", "Office", "", "hello there", 40, true); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1]["command"] != "players/cmd/play_announcement" {
		t.Fatalf("unexpected calls: %v", got)
	}
	args := got[1]["args"].(map[string]any)
	if args["player_id"] != "aa:bb" || args["message"] != "hello there" || args["volume_level"] != 40.0 || args["pre_announce"] != true {
		t.Fatalf("bad args: %v", args)
	}
	if err := run(srv.URL, "tok", "Office", "", "", -1, false); err == nil {
		t.Fatal("expected error without message or url")
	}
}
