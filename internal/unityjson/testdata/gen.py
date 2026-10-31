#!/usr/bin/env python3
"""Makes the fixtures of internal/unityjson. Run: python3 -I internal/unityjson/testdata/gen.py

Everything but the AES step is Python's standard library (json text written by hand, gzip,
hashlib.pbkdf2_hmac). The AES-128-CBC step runs the `openssl enc` program with the key and IV
given explicitly, so the fixtures do not come from the Go code they test. The IVs and the
gzip times are fixed, so the files are the same each time.

Easy Save 3's files are made to the layout that public descriptions give (see the package
comment of internal/unityjson); no real Easy Save file was available, so the JSON is what ES3
is described to write, not a copy of one.

- plain.json: a JsonUtility-like save (object).
- root-array.json: a JSON array.
- es3-plain.es3: Easy Save JSON, no gzip, no encryption.
- es3-gzip.es3: the same, gzipped.
- es3-aes.es3: encrypted with the password "password" (the default of Easy Save's settings).
- es3-aes-gzip.es3: gzipped, then encrypted (the order Easy Save uses).
- es3-other-password.es3: encrypted with the password "hunter2": the editor refuses it.
"""
import gzip
import hashlib
import io
import os
import subprocess

HERE = os.path.dirname(os.path.abspath(__file__))

PLAIN = """{
	"playerName": "Ünity Hero",
	"level": 12,
	"health": 87.5,
	"alive": true,
	"nothing": null,
	"position": {"x": 1.5, "y": 0, "z": -2},
	"inventory": ["sword", "shield", 3],
	"stats": {"strength": 10, "luck": 0.25, "tags": []}
}
"""

ARRAY = '[1, "two", {"three": 3}, [4, 5], null, false]\n'


def entry(key, typ, value, last=False):
    return '\t"%s" : {\n\t\t"__type" : "%s",\n\t\t"value" : %s\n\t}%s\n' % (key, typ, value, "" if last else ",")


def es3_text():
    out = "{\n"
    out += entry("playerName", "string", '"Ünity Hero"')
    out += entry("level", "int", "12")
    out += entry("gold", "long", "5000000000")
    out += entry("health", "float", "87.5")
    out += entry("speed", "double", "3.75")
    out += entry("alive", "bool", "true")
    out += entry("rank", "byte", "200")
    out += entry("position", "UnityEngine.Vector3,UnityEngine.CoreModule", '{\n\t\t\t"x" : 1.5,\n\t\t\t"y" : 0,\n\t\t\t"z" : -2\n\t\t}')
    out += entry("inventory", "System.Collections.Generic.List`1[[System.String,mscorlib]],mscorlib", '[\n\t\t\t"sword",\n\t\t\t"shield"\n\t\t]')
    out += entry("nothing", "string", "null", last=True)
    out += "}"
    return out.encode("utf-8")


def gz(data):
    buf = io.BytesIO()
    with gzip.GzipFile(fileobj=buf, mode="wb", mtime=0) as f:
        f.write(data)
    return buf.getvalue()


def aes(data, password, iv):
    key = hashlib.pbkdf2_hmac("sha1", password.encode("utf-8"), iv, 100, 16)
    out = subprocess.run(
        ["openssl", "enc", "-aes-128-cbc", "-K", key.hex(), "-iv", iv.hex()],
        input=data, stdout=subprocess.PIPE, check=True,
    ).stdout  # openssl pads with PKCS7
    return iv + out


def write(name, data):
    with open(os.path.join(HERE, name), "wb") as f:
        f.write(data)


if __name__ == "__main__":
    write("plain.json", PLAIN.encode("utf-8"))
    write("root-array.json", ARRAY.encode("utf-8"))
    text = es3_text()
    write("es3-plain.es3", text)
    write("es3-gzip.es3", gz(text))
    write("es3-aes.es3", aes(text, "password", bytes(range(16))))
    write("es3-aes-gzip.es3", aes(gz(text), "password", bytes(range(16, 32))))
    write("es3-other-password.es3", aes(text, "hunter2", bytes(range(32, 48))))
