"""Makes the fixtures of package rubymarshal: sample.marshal and types.marshal.

    python3 -I internal/rubymarshal/testdata/gen.py

The dumps are assembled by rmgen.py, which follows Ruby's marshal.c. sample.marshal also
parses with the Python package rubymarshal (see check.py), which the test uses as a second
reader when it is installed; types.marshal holds type codes that package does not read.
"""
import importlib.util
import os

here = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("rmgen", os.path.join(here, "rmgen.py"))
g = importlib.util.module_from_spec(spec)
spec.loader.exec_module(g)

# sample.marshal: three consecutive dumps, as RGSS writes them.
ints = [0, 1, 122, 123, 255, 256, 65535, 65536, 1 << 24, (1 << 30) - 1,
        -1, -123, -124, -255, -256, -65536, -(1 << 24), -(1 << 30)]
big = [1 << 30, -(1 << 30) - 1, (1 << 64) + 5, -(1 << 100), 1 << 200]

dump1 = g.Hash([
    (g.Sym("nils"), g.Arr([None, True, False])),
    (g.Sym("ints"), g.Arr(ints)),
    (g.Sym("bigs"), g.Arr(big)),
    (g.Sym("floats"), g.Arr([
        g.Flt(100.0), g.Flt(0.25, key="quarter"), g.Ref("quarter"), g.Flt(-1234567.5),
        g.Flt(1e-5), g.Flt(3.14159), g.Flt(0.0), g.Flt(float("inf")), g.Flt(float("-inf")),
        g.Flt(float("nan")), g.Flt(1e100), g.Flt(0, text="0\x00junk"),
    ])),
    (g.Sym("strings"), g.Arr([
        g.Str("héllo", "utf8", "hello"), g.Ref("hello"),
        g.Str("plain", "ascii"),
        g.Str("日本語", "Shift_JIS"), g.Str("勇者", "Shift_JIS"),
        g.Str("café", "ISO-8859-1"),
        g.Str("legacy"), g.Str(b"\xff\xfe", "binary"),
        g.Str(""),
    ])),
    (g.Sym("symbols"), g.Arr([g.Sym("a"), g.Sym("b"), g.Sym("a"), g.Sym("ints")])),
])

actor1 = g.Obj("Game_Actor", [
    ("@name", g.Str("Eric", "utf8", "name")),
    ("@class_name", g.Ref("name")),
    ("@hp", 450),
    ("@exp", g.Big(1 << 33)),
    ("@zoom", g.Flt(1.5, key="zoom")),
    ("@zoom2", g.Ref("zoom")),
], key="actor1")
dump2 = g.Obj("Game_Party", [
    ("@actors", g.Arr([actor1, g.Ref("actor1")])),
    ("@gold", 1234),
    ("@steps", 0),
    ("@color", g.UserDef("Color", g.color(255.0, 128.0, 0.0, 255.0))),
    ("@tone", g.UserDef("Tone", g.color(0.0, -34.0, 17.5, 0.0), enc="ascii")),
    ("@rect", g.UserDef("Rect", g.rect(1, -2, 640, 480))),
    ("@table", g.UserDef("Table", g.table(3, 2, 1, [1, -2, 3, 4, 5, 32767]))),
    ("@time", g.UserDef("Time", bytes(range(8)), enc="ascii")),
    ("@keys", g.Hash([
        (g.Arr([1, 3, g.Str("A", "utf8")]), True),
        (g.Str("k", "utf8"), g.Arr([])),
        (7, None),
    ])),
])
dump3 = g.Arr([1, 2, 3])

with open(os.path.join(here, "sample.marshal"), "wb") as f:
    for d in (dump1, dump2, dump3):
        f.write(g.dump(d))

# types.marshal: one dump per type code that the reader has to understand.
types = [
    None, True, False, 1000000,
    g.Arr([g.Sym("x"), g.Sym("x"), g.Sym("y")]),
    g.Big(5),
    g.Cls("Game_Map"), g.Cls("Comparable", mod=True), g.Cls("Kernel", mod=True, old=True),
    g.Data("Wrapper", g.Arr([1, 2])),
    g.Hash([(g.Sym("a"), 1)], default=9),
    g.Obj("Point", [("@x", 3), ("@y", 4)]),
    g.Regexp("a.c", 5),
    g.Str("x", "utf8"),
    g.Struct("Pair", [("left", 1), ("right", g.Str("r", "utf8"))]),
    g.UClass("MyString", g.Str("sub", "utf8")),
    g.UClass("MyArray", g.Arr([1])),
    g.UserDef("Table", g.table(1, 1, 1, [7])),
    g.UserMarshal("Set", g.Hash([(g.Sym("k"), True)])),
    g.Extended("Comparable", g.Obj("Thing", [("@a", 1)])),
    # a Float shared between two arrays elements and a String in a hash key
    g.Arr([g.Flt(2.5, key="f"), g.Ref("f"), g.Hash([(g.Str("key", "utf8", "k"), g.Ref("k"))])]),
]
with open(os.path.join(here, "types.marshal"), "wb") as f:
    for d in types:
        f.write(g.dump(d))
