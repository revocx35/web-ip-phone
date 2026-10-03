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

## Contacts

The *Contacts* tab is your phone book on the server, the same as in the web UI: a contact added or
changed on one device appears on the others right away.

- **Search** by name or number; favorites (star) are listed first.
- Tap the phone icon to call (a menu appears for contacts with several numbers), or tap the contact for
  its numbers, the favorite star and *Edit*.
- **Add**: the + button, or the person icon next to an unknown number in *Recents*. *From phone
  contacts* copies a name and number from your phone's address book through Android's contact picker;
  the app has no access to the rest of your contacts and needs no permission for this.
- Contacts marked *Shared* come from your administrator and are visible to all users; only
  administrators can change them.
- Callers are shown with their contact name on the incoming call screen, in notifications, during the
  call and in *Recents*. While you type on the keypad, a matching contact is suggested; tap it to use its
  number. The transfer dialog suggests contacts too.

## Receiving calls when the app is closed

*Settings -> Receive calls when the app is closed* (on by default) keeps a connection to your server in
a foreground service, shown as a silent notification. There is no push service: the connection itself
delivers the calls, so:

- allow *Running in the background* (battery optimization exemption) when the app suggests it, otherwise
  some phones cut the connection while sleeping;
- see *Incoming calls on the lock screen* below.

Turn the option off to receive calls only while the app is open.

## Incoming calls on the lock screen

Like WhatsApp, an incoming call turns the screen on and shows the call screen over the lock screen;
you can answer without unlocking. On Android 14 and newer this needs the special access
**Full-screen notifications**, which Android usually does not grant automatically to apps installed
outside the Play Store. Without it, a call only rings with a notification.

The app explains this after you sign in and shows a red banner on the keypad while it is missing; *Allow*
opens the right Android setting (*Settings -> Apps -> Special app access -> Full-screen notifications*).

If your phone has no such setting or still only shows a notification (some manufacturers add their own
restrictions), allow **Display over other apps** under *Settings -> Call screen when locked* in the app:
the app then opens the call screen itself when a call comes in, and again when you turn on or unlock the
screen while it rings. The app does not draw anything over other apps; Android just requires this
permission for an app to open a screen from the background. On Xiaomi/MIUI also allow *Show on lock
screen* and *Display pop-up windows while running in the background* in the app's permissions.

While you are using the phone, an incoming call shows as a heads-up notification with Answer and Decline,
like other call apps.

## Own SIP accounts

If your admin allows "any SIP account" on a PBX, *Settings -> My phones -> Add phone* adds an extension
with credentials you know. They are checked against the PBX before saving and stored encrypted on the
server (never on the phone).

## Privacy and security

- The session token is encrypted with a key in the Android Keystore; app data is excluded from backups.
- Only HTTPS; certificate pinning for self-signed servers (see above).
- Sign out in *Settings*, or end the session from another device under *Settings -> Security -> Signed-in
  devices* in the web UI.
