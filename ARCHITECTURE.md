# Architecture

## 1. Overview

```mermaid
flowchart LR
  subgraph Internet / anywhere
    B[Browser<br/>web UI]
    A[Android app]
  end
  subgraph Home / office LAN
    RP[(optional reverse proxy<br/>e.g. Nginx Proxy Manager)]
    S[Web IP Phone server<br/>Go, one container]
    P1[PBX 1<br/>FreePBX / Asterisk]
    P2[PBX 2]
  end
  B -- HTTPS + WSS :443/:8443 --> RP --> S
  A -- HTTPS + WSS --> RP
  S -- SIP UDP/TCP/TLS :5060 --> P1
  S -- RTP G.711 --> P1
  S -- SIP + RTP --> P2
```

Clients only ever talk HTTPS/WebSocket to the server. The server is a SIP user agent on the LAN: it
registers the virtual phones (extensions) with the PBXs and places/answers calls for its clients, and
relays the audio between the RTP stream (PBX side) and the WebSocket (client side). Only one TCP port
has to be reachable from outside, so it works through any reverse proxy and needs no UDP port
forwarding, STUN or TURN.

Terms: a **virtual phone** is a SIP account on a PBX (extension + credentials). It is either
**provisioned** by an admin (shared, granted to users) or **own** (created by a user on a PBX where the
admin allowed "any SIP account"). A **client** is one connected browser tab or app instance.

## 2. Server components

| Package | Role |
|---|---|
| `cmd/webphone` | wiring, TLS certificate (self-signed or provided, hot-reloaded), setup token, shutdown, CLI recovery commands |
| `internal/httpapi` | REST API, WebSocket endpoint, static SPA; security middleware (client IP/proxy resolution, CSRF, headers, rate limits) |
| `internal/phone` | `Service`: clients, online phones, call ownership, authorization of every call command, call history |
| `internal/sipua` | SIP engine on [sipgo](https://github.com/emiago/sipgo): registrations, calls, SDP, in-dialog requests |
| `internal/media` | RTP sessions (paced sender, RFC 4733 DTMF, symmetric RTP), G.711 |
| `internal/store` | SQLite: users, sessions, PBXs, phones, access grants, calls, audit log, settings |
| `internal/auth` | argon2id, session tokens, TOTP, login throttle, rate limiter |
| `internal/secretbox` | AES-256-GCM encryption of SIP passwords and TOTP seeds (context-bound) |
| `internal/dialrule` | Asterisk-style dial patterns per user and PBX |

## 3. Data model

```
users ─┬─< sessions            (token hash, kind web|app, idle + absolute expiry)
       ├─< recovery_codes      (SHA-256 of 2FA recovery codes)
       ├─< pbx_access >── pbxs (mode: any | selected, dial_rules)
       ├─< phone_access >── phones   (provisioned phones granted to the user)
       └─< phones (owner_id)   (own phones; owner_id NULL = provisioned)
calls (history), audit_log, settings (policy JSON)
```

**Authorization rule** (`store.usableSQL`): user U may use phone P iff P's PBX is enabled, U has a
`pbx_access` row for it, and either P is U's own phone and the mode is `any`, or P is provisioned and
granted to U in `phone_access`. Everything (REST, WebSocket commands, incoming-call routing) goes
through this rule; `phone.Service.Revalidate` re-applies it to live connections and calls after every
admin change, so revoked access ends calls immediately.

## 4. Registration

A phone with "receive incoming calls" on is registered **while at least one client is online with
it** (plus 45 s grace so a reload or network switch does not bounce it). The Contact URI carries a
random per-phone token (`sip:w<26 base32 chars>@<server-ip>:5070`); incoming INVITEs are routed by
that token, not by extension number. Unregistering sends `Contact: <our uri>;expires=0`: only our
binding is removed, never a desk phone registered on the same extension. The PBX must allow several
contacts per extension for that to coexist (FreePBX: *Max Contacts* > 1).

## 5. Call flows

Outgoing:

```
client            server (phone.Service / sipua)            PBX
  │ {"type":"dial"}    │                                      │
  ├──────────────────► authorize phone + dial rules + limits  │
  │                    │ INVITE (SDP offer PCMU/PCMA/101) ───►│
  │                    │◄──────── 401 ── INVITE+auth ────────►│
  │◄─ call ringing ────│◄──────── 180 / 183+SDP (early media) │
  │◄─ call active ─────│◄──────── 200 OK ── ACK ─────────────►│
  │══ binary audio ═══►│ paced RTP 20 ms ════════════════════►│
  │◄══ binary audio ═══│◄═══════════════════════════ RTP ═════│
```

Incoming: the PBX sends INVITE to the Contact token; the engine asks `phone.Service.IncomingCall`,
which rings every idle client online on that phone whose user may still use it (486 if all are busy,
480 if none is online). The first client that answers gets the call; the others see
`answered_elsewhere`. CANCEL from the PBX becomes a missed call.

Hold = re-INVITE with `a=sendonly`; transfer = REFER (blind); DTMF = RFC 4733 events or SIP INFO
(per PBX). Re-INVITE/UPDATE from the PBX (hold, session refresh, address changes) are answered and
applied to the RTP session. A call whose RTP stops for 90 s (and is not on hold) is hung up.

**Detach/attach:** when a client's WebSocket drops mid-call, the call stays up for 30 s. The next
`hello` lists the user's active calls and the client sends `attach` to resume. The same command moves a
call between devices of the same user ("Continue here").

## 6. Media pipeline

G.711 is carried end to end, never transcoded: the negotiated codec (PCMU or PCMA, per PBX preference)
is announced in the call state, the client encodes/decodes it itself.

- **Client -> PBX:** 20 ms frames (160 bytes) over the WebSocket as binary messages `0x01 + payload`.
  The server queues them per call and sends exactly one RTP packet every 20 ms (silence when the queue
  is empty, e.g. muted), dropping backlog above 160 ms so TCP bursts never turn into lasting delay.
- **PBX -> client:** each RTP payload is forwarded immediately. The client's jitter buffer (web:
  `public/worklets/playback.js`, Android: `JitterBuffer.kt`) starts at 60 ms, grows after underruns,
  shrinks slowly while stable, and drops backlog after a network stall.
- **Web capture:** getUserMedia with echo cancellation/noise suppression/AGC -> AudioWorklet: 4th-order
  low-pass, fractional resampling to 8 kHz, G.711 encode (`capture.js`). Playback resamples 8 kHz to the
  device rate with an anti-imaging filter.
- **Android:** `AudioRecord(VOICE_COMMUNICATION)` (platform AEC/NS/AGC) at 8 kHz, or 16/48 kHz with a
  windowed-sinc decimator; `AudioTrack(USAGE_VOICE_COMMUNICATION)`; `AudioRouter` handles audio mode,
  focus and earpiece/speaker/Bluetooth/wired routing.

Why WebSocket rather than WebRTC: one HTTPS port through any reverse proxy, no ICE/TURN, identical
code path for browser and app, and G.711 at 64 kb/s needs no bandwidth adaptation. The cost is TCP
head-of-line blocking on lossy links, which the jitter buffers bound.

## 7. Request path and security layers

1. `base`: client IP from the TCP peer, or from `X-Forwarded-For` (right-most untrusted entry) only when
   the peer is a configured trusted proxy; security headers (strict CSP, HSTS, frame denial); global API
   rate limit per IP.
2. `authed`: session from the `__Host-` cookie (browser) or `Authorization: Bearer` (app) - a token is
   only valid in the transport it was issued for. Restricted sessions (temporary password, required 2FA
   not yet enrolled) reach only the endpoints needed to fix that.
3. `csrf` for cookie-authenticated state changes: `X-Requested-With` header, `Sec-Fetch-Site`, `Origin`.
   WebSocket upgrades with a cookie require a matching `Origin`.
4. Handler: strict JSON (size-limited, unknown fields rejected), validation, authorization, audit log.

On the SIP side: packets from addresses other than the configured PBXs are dropped before parsing;
incoming INVITEs must match a Contact token *and* come from that phone's PBX; RTP is accepted only from
the address in the PBX's SDP. SIP header values from users are validated against strict character sets
(sipgo does not escape header values).

## 8. Deployment

One container, `network_mode: host` (SIP/RTP need the host's LAN address), read-only root file system,
no capabilities, non-root (UID 65532), data in the `/data` volume: `webphone.db` (SQLite, WAL),
`secret.key` (master key for secretbox; or `WEBPHONE_SECRET_KEY`), `tls/` (self-signed certificate).
Only 8443/tcp (or a reverse proxy's 443) needs to be exposed. See `docs/deployment.md`.

## 9. Android app

Single-activity Compose app. `WebPhoneApp` owns `Prefs` (token encrypted with an Android Keystore
AES-GCM key), `Api` (OkHttp with `PinningTrustManager`: system CAs, or the certificate fingerprint the
user confirmed on first connect) and `PhoneClient` (the WebSocket session, same protocol as the web
client). `PhoneService` is a foreground service: type `phoneCall|microphone` during calls,
`specialUse` while waiting for calls in the background (optional; there is no push service, the
connection itself delivers incoming calls). Incoming calls ring with a CallStyle notification and a
full-screen `IncomingCallActivity` over the lock screen.
