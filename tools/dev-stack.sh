#!/usr/bin/env bash
# Local test stack for manual and Android e2e tests:
#   test Asterisk (127.0.0.1:5160) + this checkout's server on :8443 with a fresh data dir, seeded with
#   admin (ADMIN_PASSWORD), PBX "Test Asterisk", shared extension 2001 "Reception" and a user E2E_USER
#   (E2E_PASSWORD) who may use 2001 and add own SIP accounts on that PBX.
#
#   ADMIN_PASSWORD=... E2E_PASSWORD=... tools/dev-stack.sh up     # then: android/scripts/e2e.py
#   tools/dev-stack.sh down
set -euo pipefail
cd "$(dirname "$0")/.."
DATA="${DATA:-/tmp/webphone-dev}"
PIDFILE="$DATA/server.pid"
URL="https://127.0.0.1:8443"
E2E_USER="${E2E_USER:-droid}"

api() { # method path [json]
  curl -sk -b "$DATA/cookies" -c "$DATA/cookies" -H 'Content-Type: application/json' -H 'X-Requested-With: webphone' \
    -X "$1" "$URL/api/v1$2" ${3:+-d "$3"}
}

case "${1:-up}" in
  up)
    : "${ADMIN_PASSWORD:?set ADMIN_PASSWORD}" "${E2E_PASSWORD:?set E2E_PASSWORD}"
    docker compose -f test/docker-compose.yml up -d --build >/dev/null
    PATH="$PATH:/usr/local/go/bin" go build -o "$DATA.bin" ./cmd/webphone
    [ -f "$PIDFILE" ] && kill "$(cat "$PIDFILE")" 2>/dev/null || true
    rm -rf "$DATA" && mkdir -p "$DATA"
    TOKEN=DEVS-TACK-TOKE-NABC-DEFG
    WEBPHONE_DATA_DIR="$DATA" WEBPHONE_HTTPS_LISTEN=:8443 WEBPHONE_SETUP_TOKEN=$TOKEN nohup "$DATA.bin" >"$DATA/server.log" 2>&1 &
    echo $! >"$PIDFILE"
    for _ in $(seq 1 50); do curl -sk "$URL/healthz" >/dev/null 2>&1 && break; sleep 0.2; done
    api POST /setup "{\"token\":\"$TOKEN\",\"username\":\"admin\",\"password\":\"$ADMIN_PASSWORD\"}" >/dev/null
    api POST /admin/pbxs '{"name":"Test Asterisk","host":"127.0.0.1","port":5160,"transport":"udp","enabled":true,"grantMe":true}' >/dev/null
    api POST /admin/pbxs/1/phones '{"label":"Reception","sipUser":"2001","password":"Test-2001-pw","register":true,"verify":true}' >/dev/null
    api POST /admin/users "{\"username\":\"$E2E_USER\",\"password\":\"$E2E_PASSWORD\",\"displayName\":\"Android Tester\"}" >/dev/null
    api PUT /admin/users/2/access '[{"pbxId":1,"mode":"any","dialRules":"","phoneIds":[1]}]' >/dev/null
    echo "server $URL up (log: $DATA/server.log); admin + $E2E_USER seeded"
    ;;
  down)
    [ -f "$PIDFILE" ] && kill "$(cat "$PIDFILE")" 2>/dev/null || true
    docker compose -f test/docker-compose.yml down >/dev/null 2>&1 || docker rm -f webphone-test-pbx >/dev/null 2>&1 || true
    rm -rf "$DATA" "$DATA.bin"
    echo "stopped"
    ;;
  *) echo "usage: $0 up|down" >&2; exit 2 ;;
esac
