# Deployment

## Requirements

- Linux host with Docker (compose plugin) that can reach the PBX over the network. It can run on the
  PBX machine itself, in a VM or a Proxmox LXC (LXC options `nesting=1`, and `keyctl=1` if unprivileged).
- `amd64` or `arm64`.

## Compose file

[`deploy/docker-compose.yml`](../deploy/docker-compose.yml) runs the published image. The container
uses **host networking**: SIP and RTP must carry the host's real LAN address to the PBX. Ports opened
on the host:

| Port | Purpose | Expose to the internet? |
|---|---|---|
| 8443/tcp | web UI, API, WebSocket (HTTPS) | yes (directly or through a reverse proxy) |
| 5070/udp + tcp | SIP towards your PBXs | **no** |
| 20000-20199/udp | RTP audio with your PBXs | **no** |

Other packets than those of configured PBXs are dropped, but keep SIP/RTP closed to the internet anyway.

## Environment variables

| Variable | Default | |
|---|---|---|
| `WEBPHONE_HTTPS_LISTEN` | `:8443` | built-in HTTPS server; empty disables it |
| `WEBPHONE_HTTP_LISTEN` | *(empty)* | plain HTTP, only for a TLS reverse proxy on the same host/LAN, e.g. `127.0.0.1:8080` |
| `WEBPHONE_TLS_CERT_FILE`, `WEBPHONE_TLS_KEY_FILE` | *(empty)* | PEM certificate + key (reloaded when the file changes); otherwise a self-signed certificate is generated in `/data/tls` |
| `WEBPHONE_TRUSTED_PROXIES` | *(empty)* | IPs/CIDRs of reverse proxies whose `X-Forwarded-For`/`-Proto` are trusted; `private` = all private ranges, `loopback` |
| `WEBPHONE_PUBLIC_ORIGINS` | *(empty)* | extra browser origins, e.g. `https://phone.example.com`, if your proxy rewrites the `Host` header |
| `WEBPHONE_SIP_PORT` | `5070` | local SIP port (UDP + TCP) |
| `WEBPHONE_RTP_PORT_MIN` / `_MAX` | `20000` / `20199` | RTP port range (2 ports per call are reserved, 1 used) |
| `WEBPHONE_ADVERTISE_IP` | *(auto)* | address announced to PBXs; default = the local address towards each PBX |
| `WEBPHONE_MAX_CALLS` | `20` | simultaneous calls on the server |
| `WEBPHONE_SECRET_KEY` / `_FILE` | *(auto)* | 32-byte base64 key that encrypts SIP passwords; default `/data/secret.key` |
| `WEBPHONE_SETUP_TOKEN` / `_FILE` | *(random)* | fixed first-run setup token (16+ characters) |
| `WEBPHONE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `WEBPHONE_SIP_TRACE` | `false` | log every SIP message (debugging only) |

Policies (2FA requirement, password length, session lifetimes, calls per user, history retention) are
set in the web UI under *Admin -> Policies*.

## Behind Nginx Proxy Manager (or another reverse proxy)

The proxy terminates TLS with a real certificate; the server can keep its self-signed HTTPS or listen
on plain HTTP towards the proxy.

1. Proxy host: `phone.example.com` -> scheme `https`, forward host = this server's LAN IP, port `8443`
   (NPM does not verify the upstream certificate), **Websockets Support: on**, SSL: request a
   certificate, Force SSL, HTTP/2.
2. In the compose file set `WEBPHONE_TRUSTED_PROXIES` to the proxy's IP (e.g. `192.168.1.20`), so login
   throttling and the audit log see the real client address. `private` also works if nothing else on
   your LAN is untrusted.
3. Optional: `WEBPHONE_HTTPS_LISTEN=""` and `WEBPHONE_HTTP_LISTEN=":8080"` with the proxy forwarding to
   `http://<ip>:8080` (only if the proxy-to-server path is trusted).

With a publicly trusted certificate the Android app connects without the fingerprint prompt.

## PBX notes (FreePBX / Asterisk)

- The server authenticates like any SIP phone (digest auth), from the host's LAN address.
- **Max Contacts**: if an extension is also used by another device, set *Max Contacts* > 1 (FreePBX:
  *Extensions -> Advanced*), otherwise the registrations replace each other. Unregistering from here only
  removes this server's own contact.
- Registration happens only while a user is online with that phone and "Receive incoming calls" is on;
  outgoing calls never need it.
- NAT between server and PBX is not expected (same LAN). If the server runs in Docker bridge mode
  instead of host mode, publish the SIP/RTP ports and set `WEBPHONE_ADVERTISE_IP` to the host's LAN IP.
- Codecs: enable `ulaw` and/or `alaw` for the extensions. DTMF: RFC 4733 (default) or SIP INFO per PBX.
- Restrict what the exposed extensions may dial on the PBX too (outbound route permissions).

## TLS towards the PBX

Choose transport *TLS* for the PBX (port 5061 by default). The PBX certificate is verified against the
system CAs unless *Verify the PBX's TLS certificate* is turned off (self-signed PBX on a trusted LAN).

## Backup and restore

Everything is in the `webphone-data` volume: `webphone.db` (SQLite in WAL mode), `secret.key`,
`tls/`. Back up the whole volume (stop the container for a consistent copy, or use
`sqlite3 webphone.db ".backup copy.db"`). Without `secret.key` the SIP passwords cannot be decrypted;
re-enter them in that case.

## Updating

```bash
docker compose pull && docker compose up -d
```

Database migrations run automatically at start. Downgrading to a version with an older schema is refused,
so back up the volume before an update you might want to roll back (e.g. 1.1.0 creates schema v2;
1.0.x refuses to start on it).

## Recovery

```bash
docker compose exec webphone webphone reset-password <username>   # temporary password, must be changed
docker compose exec webphone webphone reset-2fa <username>
```
