#!/usr/bin/env python3
"""Makes the PlayerPrefs fixtures of internal/unityprefs. Run: python3 -I internal/unityprefs/testdata/gen.py

- linux.prefs: the XML file of Linux, in the layout of a real file (tab indented,
  <unity_prefs version_major="1" version_minor="1">, strings in base64). The values are made up.
- mac-plistlib.plist: a binary plist written by Python's plistlib (reals are 8 bytes).
- mac-array-root.plist: a binary plist whose top object is an array, which is not a PlayerPrefs file.
- mac-float32.plist: a binary plist written by hand: 4-byte reals, shared objects, a string that
  is a key and a value, objects in another order than their numbers, a gap between objects,
  2-byte offsets and a 2-byte object number size.
"""
import base64
import datetime
import os
import plistlib
import struct

HERE = os.path.dirname(os.path.abspath(__file__))


def b64(s):
    return base64.b64encode(s.encode("utf-8")).decode("ascii")


def linux():
    prefs = [
        ("DataJson", "string", b64('{"SettingsFullScreen":"1","SettingsResolution":"1366x768"}')),
        ("Screenmanager Is Fullscreen mode", "int", "1"),
        ("Screenmanager Resolution Height", "int", "768"),
        ("Screenmanager Resolution Width", "int", "1366"),
        ("UnitySelectMonitor", "int", "-1"),
        ("UnityGraphicsQuality", "int", "5"),
        ("Gold", "int", "1500"),
        ("Volume", "float", "0.8"),
        ("Gravity", "float", "-9.81"),
        ("Tiny", "float", "1E-05"),
        ("PlayerName", "string", b64("Ünity Hero ✓")),
        ("Empty", "string", ""),
        ("a&amp;b", "int", "7"),
        ("Binary", "string", base64.b64encode(b"\xff\xfe\x00").decode()),
    ]
    out = ['<unity_prefs version_major="1" version_minor="1">']
    for name, typ, text in prefs:
        out.append('\t<pref name="%s" type="%s">%s</pref>' % (name, typ, text))
    out.append('\t<pref name="Notes" type="string" />')
    out.append("</unity_prefs>")
    with open(os.path.join(HERE, "linux.prefs"), "w", newline="\n") as f:
        f.write("\n".join(out) + "\n")


def mac_plistlib():
    d = {
        "Gold": 1500,
        "Volume": 0.8,
        "PlayerName": "Hero",
        "Greeting": "Grüße ✓ \U0001F600",
        "Long": "x" * 40,
        "Negative": -5,
        "Big": 3000000000,
        "Huge": 2**63 + 5,
        "Flag": True,
        "Blob": b"\x00\x01\x02",
        "When": datetime.datetime(2020, 5, 17, 12, 30, 15),
        "Nested": {"Level": 3, "Names": ["a", "b", 2.5]},
        "unity.player_session_count": 12,
        "UnityGraphicsQuality": 5,
        "Screenmanager Resolution Width": 1920,
    }
    with open(os.path.join(HERE, "mac-plistlib.plist"), "wb") as f:
        plistlib.dump(d, f, fmt=plistlib.FMT_BINARY, sort_keys=False)


def mac_array_root():
    with open(os.path.join(HERE, "mac-array-root.plist"), "wb") as f:
        plistlib.dump([1, 2, 3], f, fmt=plistlib.FMT_BINARY)


def count_header(marker, n):
    if n < 15:
        return bytes([marker | n])
    return bytes([marker | 0xF, 0x10, n]) if n < 256 else bytes([marker | 0xF, 0x11]) + struct.pack(">H", n)


def o_ascii(s):
    b = s.encode("ascii")
    return count_header(0x50, len(b)) + b


def o_utf16(s):
    b = s.encode("utf-16-be")
    return count_header(0x60, len(b) // 2) + b


def o_int(n, size):
    return bytes([0x10 | {1: 0, 2: 1, 4: 2, 8: 3}[size]]) + n.to_bytes(size, "big", signed=(size == 8))


def o_real32(x):
    return b"\x22" + struct.pack(">f", x)


def o_bool(v):
    return b"\x09" if v else b"\x08"


def o_array(refs, rs):
    return count_header(0xA0, len(refs)) + b"".join(r.to_bytes(rs, "big") for r in refs)


def o_dict(keys, vals, rs):
    return count_header(0xD0, len(keys)) + b"".join(r.to_bytes(rs, "big") for r in keys + vals)


def mac_float32():
    rs, os_ = 2, 2
    names = ["Volume", "Gold", "Name", "Alias", "Cheat", "Neg", "Big", "Greeting", "Nested", "List"]
    objs = {}
    # 0 is the top dictionary; 1..10 are its keys; 11.. are the values.
    for i, n in enumerate(names):
        objs[1 + i] = o_ascii(n)
    objs[11] = o_real32(0.8)
    objs[12] = o_int(1500, 2)
    objs[13] = o_ascii("Hero")  # value of Name and Alias, and a key of Nested
    objs[14] = o_bool(True)
    objs[15] = o_int(-5, 8)
    objs[16] = o_int(3000000000, 4)
    objs[17] = o_utf16("Grüße ✓")
    objs[18] = o_dict([13], [19], rs)
    objs[19] = o_int(7, 1)
    objs[20] = o_array([11, 12, 18], rs)
    keys = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
    vals = [11, 12, 13, 13, 14, 15, 16, 17, 18, 20]
    objs[0] = o_dict(keys, vals, rs)
    order = [0] + list(range(20, 0, -1))
    body = b"bplist00"
    offsets = {}
    for i in order:
        offsets[i] = len(body)
        body += objs[i]
        if i == 12:
            body += b"\x00\x00\x00"  # a gap between two objects
    table_off = len(body)
    table = b"".join(offsets[i].to_bytes(os_, "big") for i in range(len(objs)))
    trailer = b"\x00" * 5 + b"\x00" + bytes([os_, rs]) + struct.pack(">QQQ", len(objs), 0, table_off)
    with open(os.path.join(HERE, "mac-float32.plist"), "wb") as f:
        f.write(body + table + trailer)


if __name__ == "__main__":
    linux()
    mac_plistlib()
    mac_float32()
    mac_array_root()
