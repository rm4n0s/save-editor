#!/usr/bin/env python3
"""Reads a PlayerPrefs file with Python and prints its variables as JSON, in a form that the
Go tests compare with their own: a list of [name, value], where a value is {"t": type, "v": text}
(and "c" for the items of a container). Run: python3 -I load.py <file>
"""
import base64
import json
import plistlib
import struct
import sys
import xml.etree.ElementTree as ET


def double_hex(x):
    return struct.pack(">d", x).hex()


def norm(v):
    if isinstance(v, bool):
        return {"t": "bool", "v": str(v).lower()}
    if isinstance(v, int):
        return {"t": "int", "v": str(v)}
    if isinstance(v, float):
        return {"t": "float", "v": double_hex(v)}
    if isinstance(v, str):
        return {"t": "string", "v": v}
    if isinstance(v, bytes):
        return {"t": "data", "v": v.hex()}
    if isinstance(v, dict):
        return {"t": "dict", "c": [[k, norm(x)] for k, x in v.items()]}
    if isinstance(v, list):
        return {"t": "array", "c": [["[%d]" % i, norm(x)] for i, x in enumerate(v)]}
    return {"t": "date", "v": ""}


def load_xml(path):
    root = ET.parse(path).getroot()
    assert root.tag == "unity_prefs"
    out = []
    for p in root:
        typ, text = p.get("type"), p.text or ""
        if typ == "int":
            v = {"t": "int", "v": str(int(text))}
        elif typ == "float":
            f = struct.unpack(">f", struct.pack(">f", float(text)))[0]
            v = {"t": "float", "v": double_hex(f)}
        else:
            raw = base64.b64decode(text)
            try:
                v = {"t": "string", "v": raw.decode("utf-8")}
            except UnicodeDecodeError:
                v = {"t": "bytes", "v": raw.hex()}
        out.append([p.get("name"), v])
    return out


def main():
    path = sys.argv[1]
    with open(path, "rb") as f:
        head = f.read(8)
    if head == b"bplist00":
        with open(path, "rb") as f:
            d = plistlib.load(f)
        out = [[k, norm(v)] for k, v in d.items()]
    else:
        out = load_xml(path)
    json.dump(out, sys.stdout)


main()
