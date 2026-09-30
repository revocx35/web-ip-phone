# Development

## Layout

```
cmd/webphone      server entry point
internal/...      server packages (see ARCHITECTURE.md)
web/              web UI (Preact + TypeScript + Vite), built into web/dist and embedded
android/          Android app (Gradle, Kotlin, Compose)
test/asterisk     disposable Asterisk for integration tests
tools/            browser_test.py (Playwright), livecall (CLI test call)
deploy/           compose file for the published image
```

## Server

Go 1.27+. `web/dist` must exist for `go build` (it holds a `.gitkeep`; build the UI for a working page).

```bash
cd web && npm ci && npm run build && cd ..
go build -o webphone ./cmd/webphone
WEBPHONE_DATA_DIR=./data WEBPHONE_HTTPS_LISTEN=127.0.0.1:28443 ./webphone
```

For UI work, `cd web && npm run dev` serves the UI with hot reload and proxies `/api` to
`https://127.0.0.1:8443` (see `web/vite.config.ts`).

## Tests

```bash
go test ./...                                    # unit tests (no network)
CGO_ENABLED=1 go test -race ./...

docker compose -f test/docker-compose.yml up -d --build     # Asterisk on 127.0.0.1:5160
go test -tags integration ./internal/...         # SIP engine + full stack against Asterisk
```

The integration tests use SIP ports 5071/5073 and RTP 22000-22100/24000-24100 on the host.

Browser test (Firefox, fake microphone, two users calling each other through Asterisk): start a server
with `WEBPHONE_SETUP_TOKEN` set and a fresh data directory, then run `tools/browser_test.py` in the
Playwright image (command in the script's docstring; PulseAudio with a null sink is needed for audio).

Android:

```bash
cd android
./gradlew :app:testDebugUnitTest :app:lintDebug :app:assembleDebug
./gradlew :app:assembleE2e            # minified like release, with test hooks (test tone, audio stats)
python3 scripts/e2e.py                # needs a running emulator, the server and Asterisk (see the script)
```

## Docker image

`Dockerfile` builds the UI and the server in builder stages and copies the static binary into
`gcr.io/distroless/static-debian13:nonroot`. CI publishes multi-arch images to
`ghcr.io/revocx35/web-ip-phone`. On a small machine, build the binary on the host and wrap it:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o img/webphone ./cmd/webphone
mkdir -p img/data
sed -n '/^FROM gcr.io/,$p' Dockerfile | sed 's#COPY --from=build /out/webphone#COPY webphone#; s#COPY --from=build --chown=65532:65532 /out/data /data#COPY --chown=65532:65532 data /data#' > img/Dockerfile
docker build -t web-ip-phone:local img
```

## Releases

1. Bump `versionCode`/`versionName` in `android/app/build.gradle.kts`.
2. Tag `vX.Y.Z` and push; CI tests, publishes the image (`:X.Y.Z`, `:X.Y`, `:X`, `:latest`) and builds the
   signed APK (secrets `RELEASE_KEYSTORE_B64`, `RELEASE_KEYSTORE_PASSWORD`, `RELEASE_KEY_ALIAS`,
   `RELEASE_KEY_PASSWORD`).
3. The release workflow creates the GitHub Release with the APK and its SHA-256.
