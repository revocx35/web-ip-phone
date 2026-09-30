#!/usr/bin/env python3
"""Real-browser end-to-end test of the web UI (Chromium via Playwright, fake microphone).

Runs against a live server and the disposable Asterisk of test/asterisk:

    docker run --rm --network host -v $PWD:/repo -v /tmp/out:/out \
      mcr.microsoft.com/playwright/python:v1.55.0-noble bash -c \
      "pip install -q playwright==1.55.0 && python /repo/tools/browser_test.py \
         --url https://127.0.0.1:28443 --token <setup token> --out /out"

Flow: first-run setup -> add PBX + extension -> grant access -> call the echo test (600)
and check audio both ways -> second user (own SIP account) receives a call from the admin.
The page's audio is observed by wrapping WebSocket.send and the AudioWorklet message port;
the app itself exposes no test hooks.
"""
import argparse
import math
import struct
import sys
import time
import wave

from playwright.sync_api import sync_playwright, expect

MONITOR = r"""
(() => {
  const t = { sent: 0, sentLoud: 0, recv: 0, recvLoud: 0 };
  window.__wipTest = t;
  const ulaw = new Float32Array(256), alaw = new Float32Array(256);
  for (let i = 0; i < 256; i++) {
    let u = ~i & 0xff, v = ((u & 0x0f) << 3) + 0x84; v <<= (u & 0x70) >> 4;
    ulaw[i] = (u & 0x80) ? 0x84 - v : v - 0x84;
    let a = i ^ 0x55, w = (a & 0x0f) << 4, seg = (a & 0x70) >> 4;
    if (seg === 0) w += 8; else if (seg === 1) w += 0x108; else { w += 0x108; w <<= seg - 1; }
    alaw[i] = (a & 0x80) ? w : -w;
  }
  const rms = (bytes, table, from) => {
    let s = 0;
    for (let i = from; i < bytes.length; i++) s += table[bytes[i]] ** 2;
    return Math.sqrt(s / (bytes.length - from));
  };
  const send = WebSocket.prototype.send;
  WebSocket.prototype.send = function (d) {
    if (d instanceof Uint8Array && d[0] === 1) {
      t.sent++;
      if (rms(d, ulaw, 1) > 800 || rms(d, alaw, 1) > 800) t.sentLoud++;
    }
    return send.call(this, d);
  };
  const post = MessagePort.prototype.postMessage;
  MessagePort.prototype.postMessage = function (m, tr) {
    if (m && m.data instanceof Uint8Array && m.codec) {
      t.recv++;
      if (rms(m.data, m.codec === 'PCMA' ? alaw : ulaw, 0) > 800) t.recvLoud++;
    }
    return post.call(this, m, tr);
  };
})();
"""


def make_wav(path):
    """Tone bursts of changing pitch: survives browser noise suppression."""
    rate = 48000
    with wave.open(path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        frames = bytearray()
        for i in range(40):
            f = [440, 660, 550, 880, 700][i % 5]
            for n in range(int(rate * 0.3)):
                frames += struct.pack("<h", int(9000 * math.sin(2 * math.pi * f * n / rate)))
            frames += b"\x00\x00" * int(rate * 0.1)
        w.writeframes(bytes(frames))


def launch_browser(p, name, wav):
    """Firefox by default: Chromium's AudioWorklet hangs inside this LXC's containers."""
    if name == "chromium":
        return p.chromium.launch(args=[
            "--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream",
            f"--use-file-for-fake-audio-capture={wav}", "--autoplay-policy=no-user-gesture-required",
        ])
    return p.firefox.launch(firefox_user_prefs={
        "media.navigator.streams.fake": True,          # built-in fake microphone (a tone)
        "media.navigator.permission.disabled": True,
        "media.autoplay.default": 0,
        "media.autoplay.block-webaudio": False,
    })


def new_context(browser, args, viewport):
    ctx = browser.new_context(ignore_https_errors=True, viewport=viewport)
    if args.browser == "chromium":
        ctx.grant_permissions(["microphone"], origin=args.url)
    ctx.add_init_script(MONITOR)
    return ctx


def step(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


def audio_check(page, label, seconds=4):
    before = page.evaluate("({...window.__wipTest})")
    time.sleep(seconds)
    after = page.evaluate("({...window.__wipTest})")
    d = {k: after[k] - before[k] for k in after}
    step(f"{label}: {d}")
    return d


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", required=True)
    ap.add_argument("--token", required=True)
    ap.add_argument("--pbx-host", default="127.0.0.1")
    ap.add_argument("--pbx-port", default="5160")
    ap.add_argument("--out", default="/tmp")
    ap.add_argument("--browser", default="firefox", choices=["firefox", "chromium"])
    args = ap.parse_args()
    wav = "/tmp/fake-mic.wav"
    make_wav(wav)
    shot = lambda page, name: page.screenshot(path=f"{args.out}/{name}.png", full_page=not name.startswith("10-"))

    with sync_playwright() as p:
        browser = launch_browser(p, args.browser, wav)
        ctx = new_context(browser, args, {"width": 1280, "height": 860})
        page = ctx.new_page()
        errors = []
        page.on("pageerror", lambda e: errors.append(str(e)))
        page.on("console", lambda m: m.type in ("error", "warning") and errors.append(m.text))
        run_flow(args, browser, ctx, page, errors, shot)


def run_flow(args, browser, ctx, page, errors, shot):
    try:
        flow(args, browser, ctx, page, errors, shot)
    except Exception:
        shot(page, "zz-failure")
        print("page errors so far:", errors)
        print("probe:", page.evaluate("""async () => {
          const out = {};
          const t = (p, ms) => Promise.race([p, new Promise((_, rej) => setTimeout(() => rej(new Error('timeout')), ms))]);
          try { const c = new AudioContext(); out.state0 = c.state;
            try { await t(c.resume(), 2000); } catch (e) { out.resume = String(e); }
            out.state1 = c.state;
            try { await t(c.audioWorklet.addModule('/worklets/capture.js'), 4000); out.addModule = 'ok'; } catch (e) { out.addModule = String(e); }
          } catch (e) { out.ctx = String(e); }
          return out; }"""))
        raise


def flow(args, browser, ctx, page, errors, shot):
    if True:

        # ---- setup
        step("setup")
        page.goto(args.url)
        expect(page.get_by_text("Create the admin account")).to_be_visible()
        shot(page, "01-setup")
        page.get_by_label("Setup token").fill(args.token.lower())
        page.get_by_label("Username").fill("admin")
        page.get_by_label("Display name (optional)").fill("Admin")
        page.get_by_label("Password", exact=True).fill("Correct-Horse-42")
        page.get_by_label("Repeat password").fill("Correct-Horse-42")
        page.get_by_role("button", name="Create admin account").click()
        expect(page.get_by_text("No phone available yet.")).to_be_visible()
        shot(page, "02-empty-phone")

        # ---- PBX + extension
        step("add PBX")
        page.get_by_role("button", name="Open Admin").click()
        page.get_by_role("button", name="Add PBX").first.click()
        page.get_by_label("Name", exact=True).fill("Test PBX")
        page.get_by_label("Host", exact=True).fill(args.pbx_host)
        page.get_by_label("Port", exact=True).fill(args.pbx_port)
        shot(page, "03-add-pbx")
        page.get_by_role("button", name="Save").click()
        expect(page.get_by_role("heading", name="Test PBX")).to_be_visible()
        page.get_by_role("button", name="Test connection").click()
        expect(page.get_by_text("Test PBX answered")).to_be_visible()

        step("add extension 2001")
        page.get_by_role("button", name="Add extension").click()
        page.get_by_label("Extension / SIP user").fill("2001")
        page.get_by_label("Password (SIP secret)").fill("Test-2001-pw")
        page.get_by_label("Label").fill("Front desk")
        page.get_by_role("button", name="Check & save").click()
        expect(page.get_by_role("cell", name="2001", exact=True)).to_be_visible()
        shot(page, "04-pbx-list")

        # ---- grant the admin the extension
        step("access")
        page.get_by_role("tab", name="Users").click()
        page.get_by_role("cell", name="admin").first.click()
        page.get_by_label("2001").check()
        page.get_by_role("button", name="Save access").click()
        expect(page.get_by_text("Access saved").last).to_be_visible()
        shot(page, "05-access")
        page.keyboard.press("Escape")

        # ---- second user with own SIP account
        step("create bob")
        page.get_by_role("button", name="Add user").click()
        page.get_by_label("Username").fill("bob")
        page.get_by_label("Initial password").fill("Builder-Pass-77")
        page.get_by_label("User must choose a new password at first sign-in").uncheck()
        page.get_by_role("button", name="Create user").click()
        page.locator(".segmented").get_by_role("button", name="Any SIP account").click()
        page.get_by_role("button", name="Save access").click()
        expect(page.get_by_text("Access saved").last).to_be_visible()
        page.keyboard.press("Escape")

        # ---- echo call from the admin
        step("echo call")
        page.get_by_role("link", name="Phone", exact=True).click()
        expect(page.locator(".badge", has_text="Registered")).to_be_visible(timeout=15000)
        page.get_by_label("Number to call").fill("600")
        shot(page, "06-dialer")
        page.get_by_role("button", name="Call", exact=True).click()
        expect(page.locator(".status.live")).to_be_visible(timeout=15000)
        d = audio_check(page, "echo call audio")
        shot(page, "07-in-call")
        if d["sent"] < 150 or d["sentLoud"] < 40 or d["recv"] < 150 or d["recvLoud"] < 40:
            print("FAIL: audio did not flow on the echo call", d)
            sys.exit(1)
        page.get_by_role("button", name="Keypad").click()
        page.get_by_role("button", name="5").click()
        page.get_by_role("button", name="Hide").click()
        page.get_by_role("button", name="Hold").click()
        expect(page.get_by_text("On hold")).to_be_visible()
        page.get_by_role("button", name="Resume").click()
        page.get_by_role("button", name="Hang up").click()
        expect(page.get_by_text("Call ended")).to_be_visible()
        time.sleep(3)
        expect(page.locator(".recent").first).to_contain_text("600")
        shot(page, "08-recents")

        # ---- bob adds his phone and receives a call
        step("bob signs in")
        bctx = new_context(browser, args, {"width": 420, "height": 860})
        bob = bctx.new_page()
        bob.on("pageerror", lambda e: errors.append("bob: " + str(e)))
        bob.goto(args.url)
        bob.get_by_label("Username").fill("bob")
        bob.get_by_label("Password").fill("Builder-Pass-77")
        bob.get_by_role("button", name="Sign in").click()
        bob.get_by_role("button", name="My phones").click()
        bob.get_by_role("button", name="Add phone").click()
        bob.get_by_label("Extension / SIP user").fill("2002")
        bob.get_by_label("Password (SIP secret)").fill("Test-2002-pw")
        bob.get_by_label("Label").fill("Bob mobile")
        bob.get_by_role("button", name="Check & save").click()
        expect(bob.get_by_text("Bob mobile")).to_be_visible()
        bob.get_by_role("link", name="Phone", exact=True).click()
        expect(bob.locator(".badge", has_text="Registered")).to_be_visible(timeout=15000)
        bob.get_by_role("button", name="Enable").click() if bob.get_by_role("button", name="Enable").is_visible() else None
        shot(bob, "09-bob-mobile")

        step("admin calls bob")
        page.get_by_label("Number to call").fill("2002")
        page.get_by_role("button", name="Call", exact=True).click()
        expect(bob.get_by_role("alertdialog")).to_be_visible(timeout=15000)
        shot(bob, "10-bob-incoming")
        bob.get_by_role("button", name="Answer").click()
        expect(bob.locator(".status.live")).to_be_visible(timeout=15000)
        expect(page.locator(".status.live")).to_be_visible(timeout=15000)
        da = audio_check(page, "admin side")
        db = bob.evaluate("({...window.__wipTest})")
        step(f"bob totals: {db}")
        shot(bob, "11-bob-in-call")
        bob.get_by_role("button", name="Hang up").click()
        expect(page.get_by_text("Call ended")).to_be_visible(timeout=10000)
        if da["recvLoud"] < 40 or db["recvLoud"] < 40:
            print("FAIL: audio between the two users", da, db)
            sys.exit(1)

        step("admin overview + audit")
        page.get_by_role("link", name="Admin", exact=True).click()
        shot(page, "12-admin-overview")
        page.get_by_role("tab", name="Audit log").click()
        expect(page.get_by_text("user.access").first).to_be_visible()
        shot(page, "13-audit")
        page.get_by_role("link", name="Settings", exact=True).click()
        page.get_by_role("tab", name="Security").click()
        page.get_by_role("button", name="Set up", exact=True).click()
        page.get_by_role("button", name="Set up two-factor authentication").click()
        expect(page.locator("img.qr")).to_be_visible()
        shot(page, "14-2fa")

        real_errors = [e for e in errors if "favicon" not in e]
        if real_errors:
            print("page errors:", real_errors)
            sys.exit(1)
        browser.close()
        print("BROWSER TEST PASSED")


if __name__ == "__main__":
    main()
