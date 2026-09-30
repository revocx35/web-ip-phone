# Android app

Native client (Android 8.0+, Kotlin + Jetpack Compose) for a Web IP Phone server. It uses the same
account and access rights as the web UI.

## Install

Download `web-ip-phone-<version>.apk` from the
[releases](https://github.com/revocx35/web-ip-phone/releases/latest) and open it (allow installing from
your browser or file manager when Android asks). Updates are installed the same way; they are signed with
the same key, so your sign-in is kept.

Signing certificate SHA-256 (verify with `apksigner verify --print-certs`):
`89:91:AF:E5:2F:02:63:12:DB:BC:66:95:6A:FF:59:51:6A:02:6E:80:3B:E7:8F:8E:73:14:94:69:9C:2A:05:E6`

## First start

1. **Server address**: the address you open in a browser, e.g. `https://phone.example.com` or
   `https://192.168.1.5:8443`. Plain `http://` is refused.
2. On Android 17+ a server on your local network needs the **Local network access** permission; the app
   asks for it.
3. **Self-signed certificate**: the app shows the certificate's SHA-256 fingerprint. Compare it with
   *Admin -> Overview -> TLS certificate* in the web UI (or the `cert_sha256` line in the server log) and
   tap *Trust*. The app then accepts exactly this certificate for this server, and warns loudly if it ever
   changes. A certificate from a public CA (e.g. through your reverse proxy) is accepted without a prompt.
4. **Sign in** with your username and password (and 2FA code if enabled). If your admin set a temporary
   password or requires 2FA, the app walks you through that first.

## Calls

- **Keypad**: choose the phone (extension) at the top; the dot shows its registration state
  (green = registered, i.e. it can receive calls). Dial rules set by your admin apply.
- **In a call**: mute, keypad (DTMF), audio output (phone/speaker/Bluetooth/headset), hold, transfer.
  The screen turns off at your ear.
- **Continue here**: a call running on another of your devices (e.g. the web UI) can be moved to the
  phone and back.
- If the network changes during a call (Wi-Fi to mobile data), the app reconnects and resumes the call
  (the server keeps it for 30 s).

## Receiving calls when the app is closed

*Settings -> Receive calls when the app is closed* (on by default) keeps a connection to your server in
a foreground service, shown as a silent notification. There is no push service: the connection itself
delivers the calls, so:

- allow *Running in the background* (battery optimization exemption) when the app suggests it, otherwise
  some phones cut the connection while sleeping;
- on Android 14+ allow *full-screen calls* (Settings in the app links there), or incoming calls show as a
  heads-up notification with Answer/Decline instead of the full call screen.

Turn the option off to receive calls only while the app is open.

## Own SIP accounts

If your admin allows "any SIP account" on a PBX, *Settings -> My phones -> Add phone* adds an extension
with credentials you know. They are checked against the PBX before saving and stored encrypted on the
server (never on the phone).

## Privacy and security

- The session token is encrypted with a key in the Android Keystore; app data is excluded from backups.
- Only HTTPS; certificate pinning for self-signed servers (see above).
- Sign out in *Settings*, or end the session from another device under *Settings -> Security -> Signed-in
  devices* in the web UI.
