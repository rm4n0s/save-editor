"""Reads a (roots, log) pickle from stdin with the stand-in Ren'Py classes, and writes the
roots and the stores of the log as JSON, so a test can compare what Python reads.

Run it with: python3 -I internal/pickle/testdata/load.py < file.pickle
"""

import json
import os
import pickle
import sys

sys.dont_write_bytecode = True

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import fakerenpy  # noqa: E402


def plain(v):
    if isinstance(v, (list, tuple)):
        return [plain(i) for i in v]
    if isinstance(v, set):
        return sorted(plain(i) for i in v)
    if isinstance(v, dict):
        return {str(k): plain(i) for k, i in v.items()}
    if isinstance(v, int) and not isinstance(v, bool) and abs(v) > 2**53:
        return str(v)
    if hasattr(v, "__dict__"):
        return {"__class__": type(v).__name__, **{k: plain(i) for k, i in v.__dict__.items()}}
    return v


roots, log = pickle.loads(sys.stdin.buffer.read(), encoding="utf-8", errors="surrogateescape")
json.dump({"roots": plain(roots), "log": [plain(r.stores) for r in log.log]}, sys.stdout, ensure_ascii=False, sort_keys=True)
