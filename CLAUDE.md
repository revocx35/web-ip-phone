# CLAUDE.md

Guide for AI assistants (and humans) working on this repository.

## What this is

**Web IP Phone** lets people use extensions of internal PBXs (FreePBX/Asterisk, any SIP PBX) from
anywhere: a browser or the native Android app talks HTTPS/WebSocket to this server, and the server
talks SIP/RTP to the PBX on the LAN. Clients never speak SIP. The deliverable is one Docker image
(Go binary with the embedded web UI) plus the Android app in `android/`.

Read [ARCHITECTURE.md](ARCHITECTURE.md) before changing call, media, auth or protocol code, and
[SECURITY.md](SECURITY.md) before touching anything reachable from the internet.

## Commands

```bash
# server (Go 1.27; the host has /usr/local/go/bin)
go build ./... && go vet ./...
go test ./...                                   # unit tests, no PBX needed (~2 s)
CGO_ENABLED=1 go test -race ./...               # race detector

# integration tests against the disposable Asterisk (host networking, SIP 127.0.0.1:5160)
docker compose -f test/docker-compose.yml up -d --build
go test -tags integration ./internal/...        # SIP engine + full stack (REST+WS+SIP)

# web UI (Preact + Vite + TypeScript; only runtime dep: preact)
cd web && npm ci && npm run build               # -> web/dist (embedded by web/embed.go)

# run locally (self-signed HTTPS, data in ./data)
WEBPHONE_DATA_DIR=./data WEBPHONE_HTTPS_LISTEN=127.0.0.1:28443 go run ./cmd/webphone

# real-browser test (Firefox via Playwright; Chromium's AudioWorklet hangs inside this LXC's containers)
docker run --rm --network host -v $PWD:/repo -v /tmp/out:/out mcr.microsoft.com/playwright/python:v1.55.0-noble bash -c \
  "apt-get update -qq && apt-get install -y -qq pulseaudio >/dev/null && pulseaudio -D --exit-idle-time=-1 && sleep 2 && \
   pactl load-module module-null-sink sink_name=null && pip install -q playwright==1.55.0 && \
   python /repo/tools/browser_test.py --url https://127.0.0.1:28443 --token <setup token> --out /out"

# a test call like the app does (prints whether our 800 Hz tone comes back)
WEBPHONE_PASSWORD=... go run ./tools/livecall -url https://127.0.0.1:8443 -insecure -user admin -phone <id> -number '*43'

# Android (see android/ and CLAUDE.local.md for the emulator)
cd android && ./gradlew :app:testDebugUnitTest :app:assembleDebug :app:assembleE2e
python3 scripts/e2e.py                          # emulator e2e (env: SERVER, E2E_USER, E2E_PKG, REAL_PBX_NUMBER)
```

Test credentials, the user's PBX details and which extension may be used live in the git-ignored
`CLAUDE.local.md`. **Never commit credentials.** On the user's FreePBX only extension 1007 may be used.

## Layout

- `cmd/webphone/`: main (wiring, TLS, housekeeping) + subcommands `healthcheck`, `reset-password`, `reset-2fa`.
- `internal/config`: `WEBPHONE_*` environment. Policies an admin can change live in the DB (`store.Settings`).
- `internal/store`: SQLite (modernc, no cgo). Migrations are append-only (`migrations` slice); `usableSQL`
  in `phones.go` is THE authorization rule for "may user X use phone Y".
- `internal/auth`: argon2id, tokens, TOTP (RFC 6238), `Throttle` (login backoff), `RateLimiter`.
- `internal/secretbox`: AES-GCM for SIP passwords/TOTP seeds, bound to a context string (AAD).
- `internal/sipua`: SIP engine on sipgo: `engine.go` (UA, listeners, source filter, routing),
  `register.go` (REGISTER loop, credential check), `call.go` (call state machine, hold, DTMF, REFER),
  `sdp.go`, `validate.go` (header-injection guards: sipgo does not validate values).
- `internal/media`: RTP session (paced 20 ms sender, RFC 4733 DTMF, symmetric RTP), G.711.
- `internal/phone`: the service between clients and the engine: who is online on which phone,
  which client carries a call's audio, authorization of every WS command, call history.
- `internal/httpapi`: REST handlers, CSRF/origin checks, WebSocket (`ws.go`), SPA serving.
- `web/`: the UI. `src/phone/client.ts` (WS + call logic), `src/phone/audio.ts` + `public/worklets/*.js`
  (capture/playback AudioWorklets), `src/views`, `src/admin`.
- `android/`: Kotlin/Compose app. `phone/PhoneClient.kt` mirrors `web/src/phone/client.ts`.
- `test/asterisk`: disposable Asterisk with extensions 2001-2004 and test numbers (600 echo, 601 tone,
  602 DTMF capture, 603 busy, 604 early media, 605 remote hangup, 606 MOH, 699 never answers).
- `tools/`: `browser_test.py` (Playwright), `livecall` (CLI test call).

## Rules

- Security first (SECURITY.md). Every new endpoint: `authed(...)` + `csrf` for state changes, strict
  `readJSON`, validation of anything that reaches SIP headers (`sipua.Valid*`), audit log entry for admin
  actions. Every new WS command: authorize in `phone.Service` (never trust client-supplied IDs).
- Never log passwords, tokens or SIP secrets. `WEBPHONE_SIP_TRACE` dumps SIP (incl. digest
  responses, not passwords) and is for debugging only.
- Engine callbacks run on engine goroutines: never call engine methods while holding `phone.Service.mu`
  (deadlock: Hangup -> end -> listener -> Service.mu).
- The web UI must work under the strict CSP (no inline script/style attributes from strings;
  Vite `assetsInlineLimit: 0`).
- Keep the WS protocol in sync: `internal/phone/protocol.go`, `web/src/api.ts` + `client.ts`,
  `android/.../net/Models.kt` + `PhoneClient.kt`, and `docs/protocol.md`.
- Android: test hooks only behind `BuildConfig.TEST_HOOKS` (debug/e2e build types, false in release).
  Release signing key location is in Claude's project memory, never in the repo.
- Commits end with the Co-Authored-By trailer; releases = tag + GHCR image + GitHub Release with the APK.

## Environment gotchas (this LXC)

- ~2 GB free disk: build Docker images in CI; locally build the binary and wrap it in the distroless
  base (see `docs/development.md`).
- 127.0.0.1:18443 is taken by another project's container; use 28443 for local servers.
- The Android emulator (`emulator-5554`, API 37) is shared with other projects; `-no-audio`, so audio is
  verified with the debug/e2e test tone and the `CallAudio: stats` log line.
- UI Automator does not see heads-up notifications: the e2e opens the notification shade.
