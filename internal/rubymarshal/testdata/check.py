"""Reads Marshal dumps with the Python package rubymarshal and writes them as JSON in the
normal form that the Go tests write for their own tree, so the two can be compared.

    python3 -I check.py FILE...   (JSON, one list of dumps per file, on standard output)

The package must be importable (a venv with `pip install rubymarshal`).
"""
import io
import json
import math
import struct
import sys

from rubymarshal.classes import RubyObject, RubyString, Symbol, UserDef
from rubymarshal.reader import load


def num(x):
    if math.isnan(x):
        return "nan"
    if math.isinf(x):
        return "inf" if x > 0 else "-inf"
    return x


def label(x):
    if isinstance(x, Symbol):
        return ":" + x.name
    if isinstance(x, bool):
        return "true" if x else "false"
    if x is None:
        return "nil"
    if isinstance(x, int):
        return str(x)
    if isinstance(x, bytes):
        x = x.decode("utf-8")
    if isinstance(x, (str, RubyString)):
        return json.dumps(str(x), ensure_ascii=False)
    if isinstance(x, (list, tuple)):
        return "Array (%d item%s)" % (len(x), "" if len(x) == 1 else "s")
    raise TypeError(repr(x))


def norm(x):
    if x is None:
        return {"k": "nil"}
    if isinstance(x, bool):
        return {"k": "bool", "v": x}
    if isinstance(x, int):
        return {"k": "int", "v": str(x)}
    if isinstance(x, float):
        return {"k": "float", "v": num(x)}
    if isinstance(x, (str, RubyString)):
        return {"k": "str", "v": str(x)}
    if isinstance(x, bytes):
        try:
            return {"k": "str", "v": x.decode("utf-8")}
        except UnicodeDecodeError:
            return {"k": "bytes", "hex": x.hex()}
    if isinstance(x, Symbol):
        return {"k": "sym", "v": x.name}
    if isinstance(x, (list, tuple)):
        return {"k": "arr", "v": [norm(i) for i in x]}
    if isinstance(x, dict):
        return {"k": "hash", "v": [[label(k), norm(v)] for k, v in x.items()]}
    if isinstance(x, UserDef):
        data = x._private_data
        c = x.ruby_class_name
        if c == "Table":
            dim, xs, ys, zs, n = struct.unpack("<5i", data[:20])
            return {"k": "u", "c": c, "dims": [xs, ys, zs],
                    "cells": list(struct.unpack("<%dh" % n, data[20:]))}
        if c in ("Color", "Tone"):
            return {"k": "u", "c": c, "f": [num(f) for f in struct.unpack("<4d", data)]}
        if c == "Rect":
            return {"k": "u", "c": c, "i": list(struct.unpack("<4i", data))}
        return {"k": "u", "c": c, "hex": data.hex()}
    if isinstance(x, RubyObject):
        return {"k": "obj", "c": x.ruby_class_name,
                "v": [[n, norm(v)] for n, v in x.attributes.items()]}
    raise TypeError(repr(type(x)))


def main(paths):
    out = {}
    for p in paths:
        data = open(p, "rb").read()
        fd = io.BytesIO(data)
        dumps = []
        while fd.tell() < len(data):
            dumps.append(norm(load(fd)))
        out[p] = dumps
    json.dump(out, sys.stdout)


main(sys.argv[1:])
