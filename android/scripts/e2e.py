#!/usr/bin/env python3
"""Emulator end-to-end test of the Android app against a running server + the test Asterisk.

Prerequisites (see CLAUDE.md): emulator running (adb devices), debug APK installed, a server at
SERVER (from the emulator: 10.0.2.2 = the host) with:
  - PBX "Test Asterisk" (test/asterisk) with shared extension 2001 granted to USER,
  - USER may add own SIP accounts on that PBX ("any" mode).
Optional REAL_PBX_NUMBER dials that number from the phone matching REAL_PBX_PHONE (e.g. FreePBX
extension 1007 calling the *43 echo test). HOST_SERVER is the same server as seen from this host
(contacts are also created through the REST API to check that the app picks them up live).

The debug build replaces the microphone with a 1 kHz tone (--ez test_tone true) and logs
"CallAudio: stats sent=.. played=.. loud=.." every 2 s; audio is checked through those counters.
"""
import json
import os
import re
import ssl
import subprocess
import sys
import time
import urllib.request
import xml.etree.ElementTree as ET

ADB = os.environ.get("ADB", "/opt/android-sdk/platform-tools/adb")
PKG = os.environ.get("E2E_PKG", "io.github.revocx35.webipphone.debug")
SERVER = os.environ.get("SERVER", "https://10.0.2.2:8443")
USER = os.environ.get("E2E_USER", "droid")
PASSWORD = os.environ.get("E2E_PASSWORD", "")  # the test user's password (see CLAUDE.local.md)
OUT = os.environ.get("OUT", "/tmp/e2e-shots")
REAL_PBX_NUMBER = os.environ.get("REAL_PBX_NUMBER", "")
REAL_PBX_PHONE = os.environ.get("REAL_PBX_PHONE", "· 1007")  # text in the phone picker entry
ASTERISK = os.environ.get("ASTERISK_CONTAINER", "webphone-test-pbx")
HOST_SERVER = os.environ.get("HOST_SERVER", "https://127.0.0.1:8443")


def adb(*args, check=True):
    r = subprocess.run([ADB, *args], capture_output=True, text=True)
    if check and r.returncode != 0:
        raise RuntimeError(f"adb {args}: {r.stderr}")
    return r.stdout


def step(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


def shot(name):
    os.makedirs(OUT, exist_ok=True)
    with open(f"{OUT}/{name}.png", "wb") as f:
        f.write(subprocess.run([ADB, "exec-out", "screencap", "-p"], capture_output=True).stdout)


def nodes():
    for _ in range(3):
        out = adb("exec-out", "uiautomator", "dump", "/dev/tty", check=False)
        i = out.find("<?xml")
        if i >= 0:
            xml = out[i:out.rfind(">") + 1]
            try:
                return list(ET.fromstring(xml).iter("node"))
            except ET.ParseError:
                pass
        time.sleep(0.5)
    return []


def find(rid=None, text=None, desc=None, contains=None):
    for n in nodes():
        r = n.get("resource-id", "")
        if rid and not (r == rid or r.endswith(":id/" + rid) or r.endswith("/" + rid)):
            continue
        if text is not None and n.get("text") != text:
            continue
        if desc is not None and n.get("content-desc") != desc:
            continue
        if contains is not None and contains not in (n.get("text", "") + " " + n.get("content-desc", "")):
            continue
        return n
    return None


def wait(what, timeout=20, **kw):
    end = time.time() + timeout
    while time.time() < end:
        n = find(**kw)
        if n is not None:
            return n
        time.sleep(0.7)
    shot("zz-failure")
    raise AssertionError(f"timeout waiting for {what} ({kw})")


def tap(n):
    x1, y1, x2, y2 = map(int, re.findall(r"\d+", n.get("bounds")))
    adb("shell", "input", "tap", str((x1 + x2) // 2), str((y1 + y2) // 2))
    time.sleep(0.4)


def type_into(n, text):
    tap(n)
    adb("shell", "input", "text", text.replace(" ", "%s").replace("*", "\\*").replace("#", "\\#"))
    time.sleep(0.3)


def scroll_to(what, **kw):
    for _ in range(4):
        n = find(**kw)
        if n is not None:
            return n
        adb("shell", "input", "swipe", "40", "1700", "40", "700", "300")
        time.sleep(0.8)
    return wait(what, **kw)


def hide_keyboard():
    """Closes the on-screen keyboard (it can cover dialog buttons)."""
    if "mInputShown=true" in adb("shell", "dumpsys", "input_method", check=False):
        adb("shell", "input", "keyevent", "KEYCODE_BACK")
        time.sleep(0.6)


def stats():
    """Latest CallAudio stats line from logcat."""
    out = adb("logcat", "-d", "-s", "CallAudio:I", check=False)
    lines = re.findall(r"stats sent=(\d+) played=(\d+) loud=(\d+)", out)
    if not lines:
        return {"sent": 0, "played": 0, "loud": 0}
    s, p, l = map(int, lines[-1])
    return {"sent": s, "played": p, "loud": l}


def audio_check(label, seconds=6, min_loud=100):
    adb("logcat", "-c")
    time.sleep(seconds)
    st = stats()
    step(f"{label}: {st}")
    if st["sent"] < 100 or st["played"] < 100 or st["loud"] < min_loud:
        shot("zz-failure")
        raise AssertionError(f"audio did not flow: {st}")
    return st


def dial(number):
    for ch in number:
        tap(wait("key " + ch, desc="key " + ch))
    tap(wait("call button", rid="call"))


def asterisk(cmd):
    return subprocess.run(["docker", "exec", ASTERISK, "asterisk", "-rx", cmd], capture_output=True, text=True).stdout


_token = []


def rest(method, path, body=None):
    """REST call as the test user (app token; the local server's certificate is self-signed)."""
    tls = ssl.create_default_context()
    tls.check_hostname, tls.verify_mode = False, ssl.CERT_NONE

    def call(m, p, b, token=None):
        h = {"Content-Type": "application/json", "X-Requested-With": "webphone"}
        if token:
            h["Authorization"] = "Bearer " + token
        req = urllib.request.Request(HOST_SERVER + "/api/v1" + p, method=m, headers=h,
                                     data=None if b is None else json.dumps(b).encode())
        with urllib.request.urlopen(req, context=tls, timeout=15) as r:
            return json.loads(r.read() or b"null")

    if not _token:
        _token.append(call("POST", "/auth/login", {"username": USER, "password": PASSWORD, "client": "app",
                                                   "deviceName": "e2e script"})["token"])
    return call(method, path, body, _token[0])


def pick_phone(label_part):
    tap(wait("phone picker", rid="phone-picker"))
    tap(wait("phone " + label_part, contains=label_part))


def main():
    if not PASSWORD:
        raise SystemExit("set E2E_PASSWORD (and E2E_USER) for the test account")
    step("fresh install state")
    adb("shell", "input", "keyevent", "KEYCODE_WAKEUP")
    adb("shell", "wm", "dismiss-keyguard")
    adb("shell", "pm", "clear", PKG)
    for p in ["android.permission.RECORD_AUDIO", "android.permission.POST_NOTIFICATIONS"]:
        adb("shell", "pm", "grant", PKG, p, check=False)
    launch = ["shell", "am", "start", "-W", "-n", f"{PKG}/io.github.revocx35.webipphone.MainActivity", "--ez", "test_tone", "true"]
    adb(*launch)
    field = None
    for _ in range(2):  # right after an install the first launch can race with the package update
        try:
            field = wait("server field", rid="server", timeout=12)
            break
        except AssertionError:
            adb(*launch)
    if field is None:
        field = wait("server field", rid="server")

    step("server address")
    type_into(field, SERVER)
    hide_keyboard()
    tap(wait("continue", text="Continue"))
    # Android 17: local network permission (our explanation, then the system dialog)
    n = find(text="Allow local network access")
    if n is not None:
        tap(wait("allow", text="Allow"))
        time.sleep(1.5)
        shot("01-local-network")
        tap(wait("system permission dialog", timeout=10, text="Allow"))
        tap(wait("continue", text="Continue"))
    wait("trust dialog", text="Trust this server?")
    shot("02-trust")
    tap(wait("trust", text="Trust"))

    step("sign in")
    type_into(wait("username", rid="username"), USER)
    type_into(wait("password", rid="password"), PASSWORD)
    shot("03-login")
    hide_keyboard()
    tap(wait("sign in", rid="signin"))
    wait("keypad", rid="phone-picker", timeout=25)
    time.sleep(1)
    n = find(text="Not now")  # "show calls on the lock screen" explanation, if the permission is missing
    if n is not None:
        tap(n)

    step("select test PBX phone and wait for registration")
    pick_phone("Reception · 2001")
    wait("registered", contains="Registered", timeout=20)
    shot("04-keypad")

    step("echo call (600) with the test tone")
    dial("600")
    wait("call running", rid="call-status", timeout=15)
    time.sleep(2)
    shot("05-in-call")
    audio_check("echo call")
    tap(wait("hang up", rid="hangup"))
    wait("back on keypad", rid="phone-picker", timeout=10)

    step("contacts: add one in the Contacts tab")
    for c in rest("GET", "/contacts")["contacts"]:  # leftovers of an aborted run
        if c["editable"]:
            rest("DELETE", f"/contacts/{c['id']}")
    tap(wait("contacts tab", rid="tab-contacts"))
    wait("empty phone book", contains="No contacts yet")
    tap(wait("add contact", rid="add-contact"))
    type_into(wait("name field", rid="contact-name"), "Echo Desk")
    hide_keyboard()
    type_into(wait("number field", rid="contact-number-0"), "600")
    hide_keyboard()
    tap(wait("save", text="Save"))
    wait("contact listed", text="Echo Desk", timeout=10)
    shot("05b-contacts")

    step("contacts: call from the list")
    tap(wait("call button of the contact", desc="Call Echo Desk"))
    n = wait("call screen", rid="call-who", timeout=15)
    if n.get("text") != "Echo Desk":
        raise AssertionError(f"call screen shows {n.get('text')!r} instead of the contact name")
    wait("call running", rid="call-status", timeout=15)
    audio_check("call from contacts", seconds=4)
    shot("05c-in-call-contact")
    tap(wait("hang up", rid="hangup"))
    wait("back on contacts", rid="add-contact", timeout=10)

    step("contacts: keypad suggestion")
    tap(wait("keypad tab", rid="tab-keypad"))
    for ch in "60":
        tap(wait("key " + ch, desc="key " + ch))
    tap(wait("suggestion", rid="suggestion", timeout=5))
    if wait("number", rid="number").get("text") != "600":
        raise AssertionError("suggestion did not fill in the number")
    for _ in range(3):
        tap(wait("delete", desc="Delete"))

    step("contacts: one created on the server shows up live and names the caller")
    created = rest("POST", "/contacts", {"name": "Front Desk", "numbers": [{"label": "Desk", "number": "2003"}]})
    tap(wait("contacts tab", rid="tab-contacts"))
    wait("pushed contact", text="Front Desk", timeout=10)
    asterisk("channel originate Local/2001@cid-2003 application Milliwatt m")
    n = wait("incoming screen", rid="incoming-who", timeout=15)
    shot("05d-incoming-contact")
    if n.get("text") != "Front Desk":
        raise AssertionError(f"incoming call shows {n.get('text')!r} instead of the contact name")
    wait("number line", text="Desk · 2003")
    tap(wait("decline", rid="decline"))
    wait("back on contacts", rid="add-contact", timeout=10)

    step("contacts: recents use contact names; delete a contact in the app")
    tap(wait("recents tab", rid="tab-recents"))
    wait("named recent call", text="Front Desk", timeout=10)
    wait("named recent call", text="Echo Desk")
    shot("05e-recents")
    tap(wait("contacts tab", rid="tab-contacts"))
    tap(wait("contact row", text="Echo Desk"))
    tap(wait("edit", text="Edit"))
    tap(scroll_to("delete", text="Delete contact"))
    tap(wait("confirm delete", text="Delete"))
    time.sleep(1.5)
    if find(text="Echo Desk") is not None:
        raise AssertionError("contact not deleted")
    rest("DELETE", f"/contacts/{created['id']}")
    wait("phone book empty again", contains="No contacts yet", timeout=10)
    tap(wait("keypad tab", rid="tab-keypad"))

    step("incoming call while the app is open")
    subprocess.run(["docker", "exec", ASTERISK, "asterisk", "-rx",
                    "channel originate PJSIP/2001 application Milliwatt m"], capture_output=True)
    wait("incoming screen", rid="answer", timeout=15)
    shot("06-incoming")
    tap(find(rid="answer"))
    wait("answered", rid="hangup", timeout=10)
    audio_check("incoming call (milliwatt)")
    tap(wait("hang up", rid="hangup"))
    wait("keypad again", rid="phone-picker", timeout=10)

    step("incoming call with the app in the background")
    adb("shell", "input", "keyevent", "KEYCODE_HOME")
    time.sleep(2)
    subprocess.run(["docker", "exec", ASTERISK, "asterisk", "-rx",
                    "channel originate PJSIP/2001 application Milliwatt m"], capture_output=True)
    time.sleep(3)
    shot("07-background-incoming")
    # The heads-up call notification is not in UI Automator's window; open the shade.
    n = None
    end = time.time() + 20
    while time.time() < end and n is None:
        adb("shell", "cmd", "statusbar", "expand-notifications")
        time.sleep(1)
        n = find(rid="answer") or find(text="Answer") or find(desc="Answer")
    shot("07b-shade")
    if n is None:
        raise AssertionError("no incoming call UI while in the background")
    tap(n)
    wait("answered from background", rid="hangup", timeout=15)
    audio_check("background call")
    tap(wait("hang up", rid="hangup"))
    time.sleep(2)

    step("add an own SIP account (2002) in Settings")
    adb("shell", "am", "start", "-n", f"{PKG}/io.github.revocx35.webipphone.MainActivity")
    tap(wait("settings tab", rid="tab-settings"))
    wait("settings", text="Settings", timeout=10)
    for _ in range(3):
        if find(text="Add phone") is not None:
            break
        adb("shell", "input", "swipe", "40", "1700", "40", "500", "300")
        time.sleep(0.8)
    tap(wait("add phone", text="Add phone", timeout=15))
    type_into(wait("sip user field", rid="add-sipuser"), "2002")
    hide_keyboard()
    type_into(wait("password field", rid="add-password"), "Test-2002-pw")
    hide_keyboard()
    tap(wait("save", text="Check & save"))
    wait("new phone listed", contains="· 2002", timeout=15)
    time.sleep(1)
    shot("08-settings")
    # clean up so the next run starts from the same server state
    tap(scroll_to("delete own phone", text="Delete"))
    time.sleep(2)
    if find(contains="· 2002") is not None:
        raise AssertionError("own phone not deleted")

    if REAL_PBX_NUMBER:
        step(f"real PBX: dial {REAL_PBX_NUMBER}")
        tap(wait("keypad tab", rid="tab-keypad"))
        pick_phone(REAL_PBX_PHONE)
        dial(REAL_PBX_NUMBER)
        wait("real call running", rid="call-status", timeout=20)
        audio_check("real PBX call", seconds=10, min_loud=50)
        shot("09-real-pbx")
        tap(wait("hang up", rid="hangup"))

    print("ANDROID E2E PASSED")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        print("FAILED:", e)
        sys.exit(1)
