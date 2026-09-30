# API and WebSocket protocol

All endpoints are under `/api/v1`. JSON in and out; unknown fields in requests are rejected; errors are
`{"error": "...", "code": "..."}` with an HTTP status.

## Authentication

- **Browser**: `POST /auth/login {"username","password","client":"web"}` sets the session cookie
  (`__Host-wip_session`). State-changing requests need the header `X-Requested-With: webphone`.
- **Apps**: `POST /auth/login {"username","password","client":"app","deviceName"}` returns
  `{"token", "user"}`; send `Authorization: Bearer <token>`. The login request itself also needs
  `X-Requested-With: webphone`.
- 2FA: the login response is `{"mfaRequired": true, "ticket"}`; then `POST /auth/mfa {"ticket","code"}`
  (TOTP code or recovery code). Tickets expire after 5 minutes / 5 wrong codes.
- `GET /me` returns the user incl. `mustChangePassword` / `mustEnroll2fa`; while one is true only
  `/me`, `/me/password`, `/me/totp/*` and `/auth/logout` work (`403 password_change_required` /
  `2fa_required` otherwise).

## REST endpoints

| Method + path | |
|---|---|
| `GET /info` | `{name, version, setupRequired, api}` (public) |
| `GET /setup`, `POST /setup {token, username, password, displayName}` | first-run admin creation |
| `POST /auth/logout` | end this session |
| `PATCH /me {displayName}`, `POST /me/password {current, new}` | profile |
| `POST /me/totp/setup`, `/me/totp/enable {code}`, `/me/totp/disable {password}`, `/me/totp/recovery {password}` | 2FA |
| `GET /me/sessions`, `DELETE /me/sessions/{id}` | signed-in devices |
| `GET /phones` | `{phones: [...], pbxs: [{id,name,mode,canAddPhones}]}` |
| `POST /phones`, `PUT /phones/{id}`, `DELETE /phones/{id}` | own SIP accounts (`verify: true` checks them first) |
| `POST /phones/{id}/test` | check credentials with the PBX |
| `GET /calls?limit=`, `DELETE /calls` | call history |
| `/admin/...` | users, access, PBXs, extensions, status, call log, audit log, settings (admins only; see `internal/httpapi/server.go`) |

## WebSocket `/api/v1/ws`

Browsers authenticate with the cookie (the `Origin` must match the server), apps with the bearer token.
Text messages are JSON; binary messages carry audio.

### Server -> client

```jsonc
{"type":"hello","user":{...},"phones":[PhoneView],"calls":[CallView],"serverTime":"...","version":"1.0.0"}
{"type":"phones","phones":[PhoneView]}          // after any change (registration state, access)
{"type":"call","call":CallView}                  // every state change of a call that concerns this client
{"type":"ack","req":"r1","call":"<call id>"}     // command accepted (dial returns the call id)
{"type":"error","req":"r1","code":"forbidden","error":"you are not allowed to call 0900..."}
{"type":"dtmf","call":"<id>","digit":"5"}        // DTMF received from the PBX
{"type":"pong"}
```

`PhoneView`: `{id, label, sipUser, displayName, pbxId, pbxName, owned, register, reg:{status, error}, online}`
with `reg.status` = `off | registering | registered | failed`.

`CallView`: `{id, phoneId, direction: in|out, remote, remoteName, state, codec, hold, remoteHold,
startedAt, answeredAt, endReason, endStatus, attached, mine}`

- `state`: `calling`, `ringing`, `early` (early media), `incoming`, `active`, `ended`
- `endStatus`: `answered`, `missed`, `rejected`, `busy`, `cancelled`, `failed`, `answered_elsewhere`
- `attached`: this client carries the call's audio; `mine`: the call belongs to this user (dialed or
  answered by them). Incoming calls are offered with `mine: false`.

### Client -> server

```jsonc
{"type":"online","phones":[12]}                  // receive calls on these phones (and keep them registered)
{"type":"dial","req":"r1","phone":12,"number":"*43"}
{"type":"answer","req":"r2","call":"<id>"}
{"type":"reject","call":"<id>"}                  // decline an offered call (486)
{"type":"hangup","call":"<id>"}                  // also declines an offered call on this device only
{"type":"dtmf","call":"<id>","digits":"12#"}
{"type":"hold","call":"<id>","on":true}
{"type":"transfer","call":"<id>","number":"1002"} // blind transfer (REFER)
{"type":"attach","call":"<id>"}                   // take over the audio of one of your calls
{"type":"ping"}
```

`req` is optional and echoed in `ack`/`error`.

### Audio

Binary messages are `0x01` followed by G.711 audio in the call's `codec` (`PCMU` or `PCMA`), 8 kHz
mono, 160 bytes = 20 ms per message recommended (up to 480 bytes accepted). Send them while the call is
`active` or `early` and attached to your connection; the server paces them onto RTP. Audio from the
PBX arrives the same way, one RTP payload per message, as it comes (the client needs a jitter buffer).

Limits: 64 KiB per message, 30 control messages/s, 120 audio messages/s per connection.
