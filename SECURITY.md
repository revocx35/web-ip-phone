# Security

Web IP Phone is meant to be reachable from the internet: it hands out access to internal telephone
systems, and a stolen login can place (possibly expensive) calls. This page lists what the software
does about that, and what you should configure.

## Reporting a vulnerability

Please use GitHub's private vulnerability reporting (*Security* tab -> *Report a vulnerability*) rather
than a public issue.

## Threat model

| Asset | Threat | Controls |
|---|---|---|
| Admin account | taking over a fresh install | first-run admin creation needs a one-time **setup token** from the server log (not guessable, throttled) |
| Accounts | password guessing | argon2id hashes; exponential login backoff per IP and per account (no permanent lockout); identical errors and timing for unknown users; optional/required **TOTP 2FA** with single-use recovery codes and replay protection |
| Sessions | theft, CSRF, fixation | 256-bit random tokens stored only as SHA-256; `__Host-` cookie, `HttpOnly`, `Secure`, `SameSite=Strict`; bearer tokens for the app are not accepted as cookies (and vice versa); idle + absolute expiry; CSRF header + `Sec-Fetch-Site` + `Origin` checks; WebSocket `Origin` check; sessions revocable per device; password change / reset / disable signs out other sessions and closes live connections |
| Web UI | XSS, clickjacking | strict CSP (`script-src 'self'`, no inline code), `frame-ancestors 'none'`, `nosniff`, `no-referrer`; the UI renders text only (no HTML from data) |
| Phone book | reading or changing other users' contacts | contacts belong to one user (others get 404); shared contacts are changed only by admins and audited; numbers are validated like dial strings, and dialing a contact goes through the same dial rules |
| PBXs | toll fraud, abuse | users only reach PBXs and extensions an admin granted; per-user **dial rules** (allow/deny patterns); per-user and global call limits; admins can watch and hang up live calls; call history and audit log |
| PBXs | using the server to guess SIP passwords | credential checks are rate limited per user (10/h) and audited |
| SIP stack | spoofed INVITEs, scanning, header injection | SIP from anything but the configured PBX addresses is dropped before parsing; incoming INVITEs need the unguessable per-phone Contact token **and** the matching PBX source; RTP accepted only from the negotiated address; all user values that reach SIP headers are validated (no CR/LF, quotes, etc.) |
| Stored secrets | database leak | SIP passwords and TOTP seeds encrypted with AES-256-GCM (key in `/data/secret.key` or `WEBPHONE_SECRET_KEY`), each bound to its row; passwords hashed; files created `0600` |
| Transport | eavesdropping | HTTPS only (built-in TLS 1.2+ or your reverse proxy); the Android app refuses plain HTTP and pins self-signed certificates on first use (fingerprint shown in the admin UI to compare); SIP to the PBX over UDP/TCP or TLS (with certificate verification unless disabled per PBX) |
| Container | escape / tampering | distroless image, non-root user, read-only root file system, all capabilities dropped, `no-new-privileges` |

Not in scope: the PBX itself (keep FreePBX updated and its SIP port closed to the internet), and the
audio between server and PBX, which is plain RTP on your LAN (use a trusted network segment).

## Recommended configuration

1. **Create the admin before exposing the server**, then require 2FA: *Admin -> Policies ->
   Two-factor authentication: required for everyone* (or at least for administrators).
2. Put the server behind your reverse proxy with a real certificate, set `WEBPHONE_TRUSTED_PROXIES`
   to the proxy's address (not `private` unless the proxy really is the only thing that can reach the
   server), and expose nothing else. SIP (5070) and RTP ports must **not** be forwarded from the internet.
3. Grant the least access: "selected extensions" instead of "any SIP account" where possible, and add
   dial rules, e.g. only internal extensions `_1XXX`, or deny premium numbers `-_0900.`.
4. Also restrict outbound routes on the PBX for the extensions you expose (FreePBX: outbound route
   permissions / pattern restrictions). Dial rules here are a second line of defence, not the only one.
5. Back up the data volume. Without `secret.key` the stored SIP passwords cannot be decrypted.
6. Review *Admin -> Audit log* and *Call log* from time to time.

## Recovery

Locked out? With shell access to the server:

```bash
docker compose exec webphone webphone reset-password <username>   # prints a temporary password
docker compose exec webphone webphone reset-2fa <username>
```

Both are recorded in the audit log.
