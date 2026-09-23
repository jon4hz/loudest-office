# Now playing

When the track changes, the panel shows cover, title and artist for a few
seconds, then returns to the music loop. The data comes from Music Assistant
(MA). The layout follows the compact mode of Tronbyt's Spotify app
(`tronbyt/apps`, `apps/spotify`).

## What is known (checked 2026-09-21, MA server 2.10.4 source)

- `POST http://<ma>:8095/api`, `Authorization: Bearer <token>`, body
  `{"command":"players/get","args":{"player_id":"<id>"}}`. The answer is the
  bare player object, `null` for an unknown player. Errors are plain text:
  401 for a bad token, 400, 500, 503.
- Auth is mandatory since MA 2.7. A long-lived token comes from the MA profile
  settings (or the command `auth/token/create`); a guest-role user can read
  players. It expires after 365 days and does not renew.
- The fields used: `playback_state` (`idle|paused|playing|unknown`) and
  `current_media.{title, artist, image_url}`. MA fills `current_media` from the
  active queue and resolves sync groups itself: asking for a group member (the
  Sendspin player `visualizer` at home, the HiFiBerry player at the office)
  answers what the group plays.
- `image_url` is `<MA base_url>/imageproxy/<64 hex>?size=512&fmt=jpg`, fetched
  without auth. The sizes are allow-listed (`0, 80, 160, 256, 512, 1024`;
  anything else is a 400). The host is MA's configured base URL, which need not
  be reachable from the visualizer. Radio streams may carry an external URL
  instead.
- The player ID is listed by `{"command":"players/all"}` and shown in the MA
  player settings.
- `sendspin-pipe` also gets this metadata (sendspin-go `OnMetadata`), in step
  with the audio, but only at home. Rejected: the office captures with
  `arecord` and would show nothing.

## Decisions

- Poll `players/get` over HTTP every 2 s, stdlib only. The WebSocket pushes the
  same data but needs a dependency, a handshake and reconnects; a card that is
  up to 2 s late is fine.
- Three config keys: `ma_url` (e.g. `http://127.0.0.1:8095`), `ma_token`,
  `ma_player`. With `ma_url` empty (the default) the bubble is not registered
  and nothing changes. With `ma_url` set, `ma_token` and `ma_player` are
  required at load. The token is a secret and the repo is public: no default
  anywhere; `SPECTRUM_MA_TOKEN` works like every other key.
- A new kind, `bubble.Track`. The controller never picks it for a loop: a track
  bubble is only shown as an interlude or through the pin.
- A new message from a bubble to the controller, `bubble.Interlude{Name string;
  For time.Duration}`: "show this bubble for this long". The priority becomes
  event > pin > interlude > music > idle. A pin still means "only this".
- How long the card shows is the bubble's setting, not the controller's, so the
  state file's controller section and `Settings` do not change.
- The brightness of a track bubble is `brightness`, as for music.

## The bubble: `visualizer/nowplaying`

`New(url, token, player string) *NowPlaying`, name `nowplaying`, kind track,
default weight 1. Settings: `{"seconds": 10}`, 0..600; 0 = no card on a track
change.

- The first `Resize` arms the poll (with a fixed panel size that `Resize` comes
  from `controller.New`, which has no program yet: it keeps the bubbles'
  answers and `Init` runs them): a `tea.Tick` of 2 s whose message the
  controller hands to every bubble, as it does for the Tronbyt fetch result.
  Each poll result re-arms it. It polls while hidden, since that is how a track
  change is noticed. One poll is in flight at most; timeout 5 s, body limit
  1 MiB.
- Poll result, ok: keep `playing`, `title`, `artist`. A track is announced
  once: when the state is `playing`, the title is not empty and title+artist
  differ from the last announced pair, the bubble remembers the pair and, with
  `seconds > 0`, answers with a cmd that yields
  `bubble.Interlude{"nowplaying", seconds}`. So the first track after the start
  is announced, a pause and resume of the same track is not, and a skip while
  paused is announced on resume. While a cover is being fetched the
  announcement waits for it (ok or error), so the card never opens with a
  black cover.
- When `image_url` differs from the last one: fetch it in a `tea.Cmd`. A URL
  whose path starts with `/imageproxy/` is rewritten to `ma_url` + that path +
  `?size=80&fmt=png`; any other URL is fetched as it is. Timeout 10 s, body
  limit 4 MiB, http(s) only, decoded by `image.Decode` (`image/jpeg`,
  `image/png`, `image/gif`) after `image.DecodeConfig` said it is at most
  2048 px a side (the decoders allocate from the header's size, so the byte
  limit alone does not bound memory), scaled to
  `(h-2) x (h-2)` by area averaging (a small function, no dependency). An error
  or an empty URL leaves the cover area black.
- Poll result, error: keep the track that is shown and remember the error
  text: `ma: <status>` for an HTTP status, `ma?` for anything else. It is not
  logged, like the Tronbyt bubble's errors: in a terminal stderr belongs to
  the TUI. `--show nowplaying` or the show route make it visible.
- Layout, for a `w x h` frame (64x32: cover 30x30, text 31 px wide):
  the cover at (1,1); the text column from `x = h+1` to `w-1`; the title in
  white at `y = h/2-8`, the artist in green (`#1db954`) at `y = h/2+1`, both in
  the 5x7 font at scale 1. A line that fits is left-aligned. A line that does
  not scrolls left at 12 px/s after a 2 s pause, followed by a gap of 3 glyphs
  and its own repeat, and pauses again when the repeat reaches the start. The
  text is drawn first and the columns `0..h` are then overwritten by the cover
  area, which clips the scroll without a clipping `DrawText`.
- `Activate` resets both scroll positions.
- Nothing known yet, or not playing and no track: the dim text `ma?`, the error
  text or `-`, centred; visible only when pinned or shown through the API.

## Font

`bubble.DrawText` folds diacritics before the glyph lookup: NFD, drop the
combining marks (`golang.org/x/text/unicode/norm`, already in the module graph
through viper, becomes a direct dependency), plus `ß` -> `SS`, `ø` -> `O`,
`æ` -> `AE`. `TextWidth` counts the folded runes. The alert bubble gains this
too.

## Controller

- `Update`: a `bubble.Interlude` from a bubble with a weight > 0 calls
  `c.Show(name, for)`; its error is dropped. Weight 0 switches the automatic
  card off, as it keeps every other bubble out of its loop.
- `Show(name string, d time.Duration) error` (in `controller/api.go`): unknown
  name: `ErrNotFound`; an event bubble or `d <= 0`: an error; while a pin is
  set: an error naming the pin. Like the pin it does not look at the weight.
  Else `c.interlude, c.interludeFor = name, d`; the next tick stamps
  `c.interludeUntil = now + d` (the controller has no clock of its own outside
  a tick). A pin set later ends the interlude.
- `decide()`: after the pin, before music: a live interlude returns its
  bubble like a pin does (activated once, never looped). When it runs out, the
  kind changes and `choose` picks a music or idle bubble as after an event.
- `wantBright`: `bubble.Track` counts as music.
- `candidates(bubble.Music|Idle)` never see the track bubble, and `nextName`
  (the m key) includes it, since it only skips event bubbles.
- `State` gains nothing: `active` and `kind` already tell.

## API

`POST /api/v1/bubbles/{name}/show`, body `{"seconds": 8}` (optional; without a
body or with 0: 10 s; at most 600). 204, 404 for an unknown bubble, 400 for the
other `Show` errors. It works for every non-event bubble, so
`/bubbles/nowplaying/show` shows the card and `/bubbles/clock/show` the clock.
The route count in the package comment becomes nine.

## Around it

- `config`: the three string flags and the "required together" check.
- `cmd/spectrum/main.go`: the registry line, depending on `c.MAURL`.
- `addon/config.yaml`: `ma_url: str?`, `ma_token: password?`, `ma_player: str?`
  in the schema, no defaults.
- Ansible: the token is in ansible-vault. The vault password is
  `ansible/vault.txt`, gitignored (the repo is public).
  - Role defaults: `visualizer_ma_url: ""`, `visualizer_ma_player: ""`,
    `visualizer_ma_token: ""`. `config.yaml.j2` renders `visualizer_config`
    combined with `ma_url`/`ma_token`/`ma_player` when `visualizer_ma_url` is
    set, so the inventory does not have to repeat the whole `visualizer_config`
    dict to add three keys. The file is already deployed 0640 to the compose
    user.
  - `inventory/group_vars/raspi/visualizer.yml`: `visualizer_ma_url:
    http://127.0.0.1:8095` (host network), `visualizer_ma_player`, and
    `visualizer_ma_token` as an inline `!vault` value from `ansible-vault
    encrypt_string`. The token itself is made by hand in the MA profile
    settings; until it is there the variable is absent and the bubble is off.
  - `ansible.cfg`: `vault_password_file = vault.txt`, relative to the file.
    The playbook runs in the execution environment, which mounts `ansible/`,
    so the password file is there without an extra mount. Verified with
    `ansible-navigator run playbooks/site.yml --tags visualizer --check --diff`.
- README: a short part with the token, the player ID, the vault and the show
  route.

## Not built

- A private-address block for external cover URLs: a radio station's metadata
  can make the visualizer send one blind GET into the LAN; nothing of the
  answer leaves the device and no token rides along.
- The WebSocket, a progress bar (compact mode has none), a periodic re-show,
  album name, new glyphs for umlauts, using MA's `playback_state` instead of the
  dB detector, a cover cache (one cover per track change is cheap).

## Verification

- `nowplaying_test.go`, against an `httptest` server for `/api` and
  `/imageproxy/`: the bearer token and the command are sent; a new track while
  playing yields one `Interlude`, the same track none, a paused player none,
  `seconds: 0` none; the cover request goes to the test server's host with
  `size=80&fmt=png` although `image_url` names another host; after the cover
  arrived, pixel (1,1) has the fixture's colour and no text pixel lies left of
  `x = h+1`; a long title moves between ticks, a short one does not; a 401
  keeps the last track and shows on a fresh bubble as `ma: 401`.
- `controller_test.go`: an interlude beats music and idle, loses to an event
  and returns after it, is refused under a pin, ends after its time, and a
  track bubble is never picked by the music or idle loop.
- `api_test.go`: the show route: 204, 404, 400.
- `font` test: `Züri West` draws the same pixels as `Zuri West`.
- `CGO_ENABLED=0 go build ./...`, `go vet ./...`, `go test ./...`.
- At home: a track change shows the card for 10 s, then the music loop
  continues; `curl -X POST :8099/api/v1/bubbles/nowplaying/show` shows it again.
