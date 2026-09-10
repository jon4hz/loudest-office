// ma-announce: play an announcement (URL or spoken text) on a Music Assistant player.
//
//	ma-announce -player Office "Lunch is ready"
//	ma-announce -player Office -url http://example.com/ding.mp3 -volume 40
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// call sends one JSON-RPC command to the MA HTTP API and returns the raw result.
func call(server, token, command string, args map[string]any) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{"command": command, "args": args})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(server, "/")+"/api", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func run(server, token, player, url, message string, volume int, pre bool) error {
	if url == "" && message == "" {
		return fmt.Errorf("need a message or -url")
	}
	// Accept a player name or a raw player id.
	raw, err := call(server, token, "players/get_by_name", map[string]any{"name": player})
	if err != nil {
		return err
	}
	var st struct {
		PlayerID string `json:"player_id"`
	}
	if json.Unmarshal(raw, &st) == nil && st.PlayerID != "" {
		player = st.PlayerID
	}
	args := map[string]any{"player_id": player}
	if url != "" {
		args["url"] = url
	} else {
		args["message"] = message
	}
	if volume >= 0 {
		args["volume_level"] = volume
	}
	if pre {
		args["pre_announce"] = true
	}
	_, err = call(server, token, "players/cmd/play_announcement", args)
	return err
}

func main() {
	server := flag.String("server", env("MA_URL", "http://localhost:8095"), "Music Assistant base URL ($MA_URL)")
	token := flag.String("token", os.Getenv("MA_TOKEN"), "API token ($MA_TOKEN)")
	player := flag.String("player", "", "player name or id (required)")
	url := flag.String("url", "", "audio URL to play instead of spoken text")
	volume := flag.Int("volume", -1, "announcement volume 0-100 (default: player's own)")
	pre := flag.Bool("pre", false, "play the pre-announce chime")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s -player NAME [flags] [message words...]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *player == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*server, *token, *player, *url, strings.Join(flag.Args(), " "), *volume, *pre); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
