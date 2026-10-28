"""Writes the save files that the tests of package renpysave read.

The signatures are made with Ren'Py's own renpy/ecsign.py, which is not part of this
repository; give its path:

    python3 -I internal/renpysave/testdata/gen.py /path/to/renpy/renpy/ecsign.py
"""

import base64
import importlib.util
import json
import os
import pickle
import struct
import sys
import zipfile
import zlib

sys.dont_write_bytecode = True

here = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(here, "..", "..", "pickle", "testdata"))

import fakerenpy  # noqa: E402

spec = importlib.util.spec_from_file_location("ecsign", sys.argv[1])
ecsign = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ecsign)


def png(width, height, rgb):
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))

    rows = b"".join(b"\x00" + bytes(rgb) * width for _ in range(height))
    header = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b"")


def line(kind, a, b=None):
    fields = [kind, base64.b64encode(a).decode("ascii")]
    if b is not None:
        fields.append(base64.b64encode(b).decode("ascii"))
    return " ".join(fields) + "\n"


def write_save(name, log, signing_keys, screenshot, version):
    meta = {"_save_name": "Chapter 2", "_renpy_version": [8, 3, 4, 24120703], "_version": "1.0",
            "_ctime": 1761680000.5, "_game_runtime": 3723.25}
    with zipfile.ZipFile(os.path.join(here, name), "w", zipfile.ZIP_DEFLATED) as zf:
        if screenshot:
            zf.writestr("screenshot.png", screenshot)
        zf.writestr("extra_info", "Chapter 2".encode("utf-8"))
        zf.writestr("json", json.dumps(meta))
        zf.writestr("renpy_version", version)
        zf.writestr("log", log)
        if signing_keys is not None:
            zf.writestr("signatures", "".join(
                line("signature", sk.verifying_key.to_der(), sk.sign(log)) for sk in signing_keys))


mine = ecsign.SigningKey.generate(curve=ecsign.NIST256p)
other = ecsign.SigningKey.generate(curve=ecsign.NIST256p)
friend = ecsign.SigningKey.generate(curve=ecsign.NIST256p)

with open(os.path.join(here, "security_keys.txt"), "w") as f:
    f.write("# Ren'Py keys of the tests\n")
    f.write(line("signing-key", mine.to_der(), mine.verifying_key.to_der()))
    f.write(line("verifying-key", friend.verifying_key.to_der()))

with open(os.path.join(here, "other_keys.txt"), "w") as f:
    f.write(line("signing-key", other.to_der(), other.verifying_key.to_der()))

roots_log = fakerenpy.sample()
write_save("renpy8.save", pickle.dumps(roots_log, 5), [mine], png(4, 3, (200, 40, 90)), "8.3.4.24120703")
write_save("friend.save", pickle.dumps(roots_log, 5), [friend], None, "8.3.4.24120703")
write_save("renpy7.save", pickle.dumps(roots_log, 2), None, None, "7.4.11.2266")
