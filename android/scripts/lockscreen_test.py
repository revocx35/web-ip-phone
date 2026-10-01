#!/usr/bin/env python3
"""Incoming call while the screen is off: the call screen must come up over the lock screen.

Uses the helpers of e2e.py. Prerequisites as for e2e.py (tools/dev-stack.sh up); works with any
build type (no test hooks needed): E2E_PKG=io.github.revocx35.webipphone for the release build.

    E2E_PKG=... E2E_PASSWORD=... python3 scripts/lockscreen_test.py [--no-signin]
"""
import re
import subprocess
import sys
import time

import e2e
from e2e import adb, find, shot, step, tap, type_into, wait, hide_keyboard


def wakefulness():
    out = adb("shell", "dumpsys", "power")
    m = re.search(r"mWakefulness=(\w+)", out)
    return m.group(1) if m else "?"


def top_activity():
    out = adb("shell", "dumpsys", "activity", "activities")
    m = re.search(r"topResumedActivity=.*? ([\w.]+/[\w.]+)", out) or re.search(r"mResumedActivity: .*? ([\w.]+/[\w.]+)", out)
    return m.group(1) if m else "?"


def keyguard_showing():
    out = adb("shell", "dumpsys", "window")
    return "mShowingLockscreen=true" in out or "isKeyguardShowing=true" in out or re.search(r"mKeyguardShowing=true", out) is not None


def originate():
    subprocess.run(["docker", "exec", e2e.ASTERISK, "asterisk", "-rx", "channel originate PJSIP/2001 application Milliwatt m"],
                   capture_output=True)


def hangup_all():
    subprocess.run(["docker", "exec", e2e.ASTERISK, "asterisk", "-rx", "channel request hangup all"], capture_output=True)


def sign_in():
    pkg = e2e.PKG
    adb("shell", "pm", "clear", pkg)
    for p in ["android.permission.RECORD_AUDIO", "android.permission.POST_NOTIFICATIONS"]:
        adb("shell", "pm", "grant", pkg, p, check=False)
    adb("shell", "am", "start", "-W", "-n", f"{pkg}/io.github.revocx35.webipphone.MainActivity")
    type_into(wait("server field", rid="server", timeout=20), e2e.SERVER)
    hide_keyboard()
    tap(wait("continue", text="Continue"))
    if find(text="Allow local network access") is not None:
        tap(wait("allow", text="Allow"))
        time.sleep(1.5)
        tap(wait("system allow", text="Allow"))
        tap(wait("continue", text="Continue"))
    tap(wait("trust", text="Trust", timeout=15))
    type_into(wait("username", rid="username"), e2e.USER)
    type_into(wait("password", rid="password"), e2e.PASSWORD)
    hide_keyboard()
    tap(wait("sign in", rid="signin"))
    wait("keypad", rid="phone-picker", timeout=25)
    e2e.pick_phone("Reception · 2001")
    wait("registered", contains="Registered", timeout=20)


def main():
    if "--no-signin" not in sys.argv:
        step("sign in")
        sign_in()
    # Dismiss any permission prompt the app may show on the main screen.
    for label in ["Not now", "Later"]:
        n = find(text=label)
        if n is not None:
            tap(n)

    step("screen off")
    adb("shell", "input", "keyevent", "KEYCODE_SLEEP")
    time.sleep(4)
    step(f"wakefulness={wakefulness()}")
    originate()
    time.sleep(6)
    w, top = wakefulness(), top_activity()
    step(f"after incoming call: wakefulness={w} top={top}")
    shot("lock-1-incoming")
    ok = w == "Awake" and "IncomingCallActivity" in top

    hangup_all()
    time.sleep(2)
    adb("shell", "input", "keyevent", "KEYCODE_WAKEUP")
    adb("shell", "wm", "dismiss-keyguard")
    print("LOCKSCREEN CALL SCREEN:", "PASSED" if ok else "FAILED")
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
