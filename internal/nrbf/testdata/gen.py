#!/usr/bin/env python3
"""Makes the BinaryFormatter fixtures of internal/nrbf. Run: python3 -I internal/nrbf/testdata/gen.py

The writer follows [MS-NRBF] (https://learn.microsoft.com/openspecs/windows_protocols/ms-nrbf).
There is no .NET here, so the fixtures are made by this script:

- player.sav: a Unity-like save. A [Serializable] PlayerData class (library Assembly-CSharp) with
  every primitive type, a string referenced twice, a nested class referenced twice (the second
  time as a MemberReference), a List<int>, a List<string>, a Dictionary<string,int>, a HashSet<int>,
  a Hashtable, arrays of every kind of BinaryFormatter (string[], int[,], Vec2[], byte[], float[],
  object[]) and nulls. Names and shapes follow what BinaryFormatter writes for these classes.
- extra.sav: the rest of what BinaryFormatter writes: a class that refers to itself, a jagged int[][]
  (BinaryArray of ArraySinglePrimitive), an offset array with a lower bound, a system class that is not a
  collection (System.Version), a char that takes two bytes, a run of 300 nulls (ObjectNullMultiple), an empty
  array, a string shared by two members and a null string.
- array-root.sav and string-root.sav: streams whose root is an int[] and a string, not a class.
- spec-address.bin: the Address object of the example in section 3 of [MS-NRBF] (revision 13.0,
  the bytes of its hex dump from the BinaryLibrary record to the MessageEnd), with a
  SerializedStreamHeader in front of it whose RootId is that object. The bytes are Microsoft's;
  the specification may be used to build implementations (Open Specifications Promise).
- spec-address-edited.bin is not made: the tests edit spec-address.bin.
"""
import os
import struct

HERE = os.path.dirname(os.path.abspath(__file__))

# Record types
HEADER, CLASS_WITH_ID, SYS_CLASS_MEMBERS, CLASS_MEMBERS, SYS_CLASS_TYPES, CLASS_TYPES = 0, 1, 2, 3, 4, 5
OBJ_STRING, BIN_ARRAY, PRIM_TYPED, MEMBER_REF, OBJ_NULL, MSG_END, LIBRARY = 6, 7, 8, 9, 10, 11, 12
NULL256, NULL_MULTI, ARR_PRIM, ARR_OBJ, ARR_STR = 13, 14, 15, 16, 17
# Binary types
B_PRIM, B_STRING, B_OBJECT, B_SYSCLASS, B_CLASS, B_OBJARR, B_STRARR, B_PRIMARR = range(8)
# Primitive types
P_BOOL, P_BYTE, P_CHAR, P_DECIMAL, P_DOUBLE, P_I16, P_I32, P_I64, P_SBYTE, P_SINGLE = 1, 2, 3, 5, 6, 7, 8, 9, 10, 11
P_TIMESPAN, P_DATETIME, P_U16, P_U32, P_U64 = 12, 13, 14, 15, 16

MSCORLIB = "mscorlib, Version=4.0.0.0, Culture=neutral, PublicKeyToken=b77a5c561934e089"
ASM = "Assembly-CSharp, Version=0.0.0.0, Culture=neutral, PublicKeyToken=null"


def lp(s):
    b = s.encode("utf-8")
    n = len(b)
    out = bytearray()
    while n >= 0x80:
        out.append((n & 0x7F) | 0x80)
        n >>= 7
    out.append(n)
    return bytes(out) + b


def i32(n):
    return struct.pack("<i", n)


def prim(p, v):
    if p == P_BOOL:
        return bytes([1 if v else 0])
    if p == P_BYTE:
        return bytes([v])
    if p == P_SBYTE:
        return struct.pack("<b", v)
    if p == P_CHAR:
        return v.encode("utf-8")
    if p == P_DECIMAL:
        return lp(v)
    if p == P_DOUBLE:
        return struct.pack("<d", v)
    if p == P_SINGLE:
        return struct.pack("<f", v)
    fmt = {P_I16: "<h", P_I32: "<i", P_I64: "<q", P_U16: "<H", P_U32: "<I", P_U64: "<Q", P_TIMESPAN: "<q", P_DATETIME: "<Q"}[p]
    return struct.pack(fmt, v)


def header(root):
    return bytes([HEADER]) + i32(root) + i32(-1) + i32(1) + i32(0)


def library(lid, name):
    return bytes([LIBRARY]) + i32(lid) + lp(name)


def binstring(oid, s):
    return bytes([OBJ_STRING]) + i32(oid) + lp(s)


def ref(oid):
    return bytes([MEMBER_REF]) + i32(oid)


NULL = bytes([OBJ_NULL])


def typed(p, v):
    return bytes([PRIM_TYPED, p]) + prim(p, v)


def type_info(types):
    """types: list of (binary type, extra) where extra is a primitive type, a class name or (name, library id)."""
    out = bytes(t for t, _ in types)
    for t, extra in types:
        if t in (B_PRIM, B_PRIMARR):
            out += bytes([extra])
        elif t == B_SYSCLASS:
            out += lp(extra)
        elif t == B_CLASS:
            out += lp(extra[0]) + i32(extra[1])
    return out


def class_info(oid, name, names):
    return i32(oid) + lp(name) + i32(len(names)) + b"".join(lp(n) for n in names)


def cls(oid, name, members, lib=None):
    """ClassWithMembersAndTypes (lib given) or SystemClassWithMembersAndTypes; members: [(name, type, extra)]."""
    names = [m[0] for m in members]
    types = [(m[1], m[2]) for m in members]
    head = class_info(oid, name, names) + type_info(types)
    if lib is None:
        return bytes([SYS_CLASS_TYPES]) + head
    return bytes([CLASS_TYPES]) + head + i32(lib)


def class_with_id(oid, meta):
    return bytes([CLASS_WITH_ID]) + i32(oid) + i32(meta)


def arr_prim(oid, p, values):
    return bytes([ARR_PRIM]) + i32(oid) + i32(len(values)) + bytes([p]) + b"".join(prim(p, v) for v in values)


def arr_obj(oid, n):
    return bytes([ARR_OBJ]) + i32(oid) + i32(n)


def arr_str(oid, n):
    return bytes([ARR_STR]) + i32(oid) + i32(n)


def bin_array(oid, kind, lengths, elem_type, extra, lower=None):
    out = bytes([BIN_ARRAY]) + i32(oid) + bytes([kind]) + i32(len(lengths)) + b"".join(i32(n) for n in lengths)
    if lower:
        out += b"".join(i32(n) for n in lower)
    return out + type_info([(elem_type, extra)])


def generic(base, *args):
    return "%s`%d[%s]" % (base, len(args), ",".join("[%s, %s]" % (a, MSCORLIB) for a in args))


def ticks(y, mo, d, h=0, mi=0, s=0):
    import datetime
    delta = datetime.datetime(y, mo, d, h, mi, s) - datetime.datetime(1, 1, 1)
    return (delta.days * 86400 + delta.seconds) * 10_000_000


def player():
    LIST_INT = generic("System.Collections.Generic.List", "System.Int32")
    LIST_STR = generic("System.Collections.Generic.List", "System.String")
    DICT = generic("System.Collections.Generic.Dictionary", "System.String", "System.Int32")
    KVP = generic("System.Collections.Generic.KeyValuePair", "System.String", "System.Int32")
    CMP = generic("System.Collections.Generic.GenericEqualityComparer", "System.String")
    HSET = generic("System.Collections.Generic.HashSet", "System.Int32")
    out = bytearray()
    out += header(1)
    out += library(2, ASM)
    members = [
        ("<Name>k__BackingField", B_STRING, None),
        ("level", B_PRIM, P_I32),
        ("health", B_PRIM, P_SINGLE),
        ("gold", B_PRIM, P_I64),
        ("speed", B_PRIM, P_DOUBLE),
        ("isAlive", B_PRIM, P_BOOL),
        ("grade", B_PRIM, P_CHAR),
        ("rank", B_PRIM, P_BYTE),
        ("delta", B_PRIM, P_SBYTE),
        ("small", B_PRIM, P_I16),
        ("flags", B_PRIM, P_U16),
        ("count", B_PRIM, P_U32),
        ("big", B_PRIM, P_U64),
        ("rating", B_PRIM, P_DECIMAL),
        ("created", B_PRIM, P_DATETIME),
        ("playTime", B_PRIM, P_TIMESPAN),
        ("nickname", B_STRING, None),
        ("home", B_CLASS, ("Game.Address", 2)),
        ("work", B_CLASS, ("Game.Address", 2)),
        ("inventory", B_SYSCLASS, LIST_INT),
        ("scores", B_SYSCLASS, DICT),
        ("tags", B_STRARR, None),
        ("grid", B_SYSCLASS, "System.Int32[,]"),
        ("points", B_CLASS, ("Game.Vec2[]", 2)),
        ("blob", B_PRIMARR, P_BYTE),
        ("ratios", B_PRIMARR, P_SINGLE),
        ("nothing", B_OBJECT, None),
        ("boxed", B_OBJECT, None),
        ("missing", B_STRING, None),
        ("names", B_SYSCLASS, LIST_STR),
        ("extras", B_OBJARR, None),
        ("table", B_SYSCLASS, "System.Collections.Hashtable"),
        ("seen", B_SYSCLASS, HSET),
    ]
    out += cls(1, "Game.PlayerData", members, lib=2)
    # The values of the members, in order.
    out += binstring(3, "Ünity Hero")
    out += prim(P_I32, 42) + prim(P_SINGLE, 87.5) + prim(P_I64, 123456789012) + prim(P_DOUBLE, 3.75)
    out += prim(P_BOOL, True) + prim(P_CHAR, "A") + prim(P_BYTE, 200) + prim(P_SBYTE, -5) + prim(P_I16, -1234)
    out += prim(P_U16, 65000) + prim(P_U32, 4000000000) + prim(P_U64, 18446744073709551000)
    out += prim(P_DECIMAL, "12.50")
    out += prim(P_DATETIME, ticks(2024, 3, 5, 12) | (1 << 62))
    out += prim(P_TIMESPAN, (3600 + 120 + 3) * 10_000_000)
    out += ref(3)  # nickname: the same string as Name
    out += ref(4) + ref(4)  # home and work: the same Address
    out += ref(5) + ref(6) + ref(7) + ref(8) + ref(9) + ref(10) + ref(11)
    out += NULL + typed(P_I32, 77) + NULL
    out += ref(12) + ref(26) + ref(28) + ref(32)

    # 4: Address, with a string that nothing else shares
    out += cls(4, "Game.Address", [("street", B_STRING, None), ("number", B_PRIM, P_I32)], lib=2)
    out += binstring(13, "1 Main St") + prim(P_I32, 12)
    # 5: List<int>
    out += cls(5, LIST_INT, [("_items", B_PRIMARR, P_I32), ("_size", B_PRIM, P_I32), ("_version", B_PRIM, P_I32)])
    out += ref(14) + prim(P_I32, 5) + prim(P_I32, 5)
    out += arr_prim(14, P_I32, [10, 20, 30, 40, 50, 0, 0, 0])
    # 6: Dictionary<string,int>
    out += cls(6, DICT, [("Version", B_PRIM, P_I32), ("Comparer", B_SYSCLASS, CMP), ("HashSize", B_PRIM, P_I32),
                         ("KeyValuePairs", B_SYSCLASS, KVP + "[]")])
    out += prim(P_I32, 2) + ref(15) + prim(P_I32, 3) + ref(16)
    out += cls(15, CMP, [])
    out += bin_array(16, 0, [2], B_SYSCLASS, KVP)
    out += cls(17, KVP, [("key", B_STRING, None), ("value", B_PRIM, P_I32)])
    out += binstring(18, "wins") + prim(P_I32, 3)
    out += class_with_id(19, 17) + binstring(20, "losses") + prim(P_I32, 5)
    # 7: string[] with a null run
    out += arr_str(7, 3) + binstring(21, "fast") + bytes([NULL256, 2])
    # 8: int[2,3]
    out += bin_array(8, 2, [2, 3], B_PRIM, P_I32) + b"".join(prim(P_I32, v) for v in range(1, 7))
    # 9: Vec2[2]
    out += bin_array(9, 0, [2], B_CLASS, ("Game.Vec2", 2))
    out += cls(22, "Game.Vec2", [("x", B_PRIM, P_SINGLE), ("y", B_PRIM, P_SINGLE)], lib=2)
    out += prim(P_SINGLE, 1.5) + prim(P_SINGLE, 2.5)
    out += class_with_id(23, 22) + prim(P_SINGLE, 3.5) + prim(P_SINGLE, 4.5)
    # 10: byte[], 11: float[]
    out += arr_prim(10, P_BYTE, [1, 2, 3, 4, 5])
    out += arr_prim(11, P_SINGLE, [0.5, 1.5, 2.5])
    # 12: List<string> with a capacity of 4 and a shared string
    out += cls(12, LIST_STR, [("_items", B_STRARR, None), ("_size", B_PRIM, P_I32), ("_version", B_PRIM, P_I32)])
    out += ref(24) + prim(P_I32, 2) + prim(P_I32, 2)
    out += arr_str(24, 4) + binstring(25, "a") + ref(21) + bytes([NULL_MULTI]) + i32(2)
    # 26: object[3]
    out += arr_obj(26, 3) + typed(P_I32, 5) + binstring(27, "x") + NULL
    # 28: Hashtable
    out += cls(28, "System.Collections.Hashtable", [
        ("LoadFactor", B_PRIM, P_SINGLE), ("Version", B_PRIM, P_I32), ("Comparer", B_OBJECT, None),
        ("HashCodeProvider", B_OBJECT, None), ("HashSize", B_PRIM, P_I32), ("Keys", B_OBJARR, None),
        ("Values", B_OBJARR, None)])
    out += prim(P_SINGLE, 0.72) + prim(P_I32, 2) + NULL + NULL + prim(P_I32, 3) + ref(29) + ref(30)
    out += arr_obj(29, 2) + binstring(31, "k1") + binstring(33, "k2")
    out += arr_obj(30, 2) + typed(P_I32, 100) + typed(P_DOUBLE, 2.5)
    # 32: HashSet<int>
    out += cls(32, HSET, [("Version", B_PRIM, P_I32), ("Comparer", B_SYSCLASS, "System.Collections.Generic.GenericEqualityComparer`1[[System.Int32]]"),
                         ("Capacity", B_PRIM, P_I32), ("Elements", B_PRIMARR, P_I32)])
    out += prim(P_I32, 2) + NULL + prim(P_I32, 3) + ref(34)
    out += arr_prim(34, P_I32, [7, 8, 9])
    out += bytes([MSG_END])
    return bytes(out)


def extra():
    out = bytearray()
    out += header(1)
    out += library(2, ASM)
    members = [
        ("name", B_STRING, None),
        ("parent", B_CLASS, ("Game.Node", 2)),
        ("jagged", B_SYSCLASS, "System.Int32[][]"),
        ("offset", B_SYSCLASS, "System.Int32[]"),
        ("accent", B_PRIM, P_CHAR),
        ("version", B_SYSCLASS, "System.Version"),
        ("nulls", B_OBJARR, None),
        ("empty", B_OBJARR, None),
        ("twin", B_STRING, None),
        ("title", B_STRING, None),
    ]
    out += cls(1, "Game.Node", members, lib=2)
    out += binstring(3, "Root")
    out += ref(1)  # the object is its own parent
    out += ref(4) + ref(5)
    out += prim(P_CHAR, "\u00e9")
    out += ref(6) + ref(7) + ref(8)
    out += ref(3)
    out += NULL
    # 4: int[][] with two items written in place
    out += bin_array(4, 1, [2], B_PRIMARR, P_I32)
    out += arr_prim(9, P_I32, [1, 2, 3]) + arr_prim(10, P_I32, [4])
    # 5: int[3] whose lower bound is 5
    out += bin_array(5, 3, [3], B_PRIM, P_I32, lower=[5]) + b"".join(prim(P_I32, v) for v in (7, 8, 9))
    # 6: System.Version
    out += cls(6, "System.Version", [("_Major", B_PRIM, P_I32), ("_Minor", B_PRIM, P_I32),
                                     ("_Build", B_PRIM, P_I32), ("_Revision", B_PRIM, P_I32)])
    out += prim(P_I32, 1) + prim(P_I32, 2) + prim(P_I32, 3) + prim(P_I32, 4)
    # 7: object[300] that is all nulls; 8: object[0]
    out += arr_obj(7, 300) + bytes([NULL_MULTI]) + i32(300)
    out += arr_obj(8, 0)
    out += bytes([MSG_END])
    return bytes(out)


def array_root():
    return header(1) + arr_prim(1, P_I32, [3, 1, 2]) + bytes([MSG_END])


def string_root():
    return header(1) + binstring(1, "hello") + bytes([MSG_END])


# The example of [MS-NRBF] section 3: the bytes of its hex dump from the BinaryLibrary record
# (offset 0xA2) to the end, MessageEnd included.
SPEC_ADDRESS_HEX = (
    "0c0300000051444f4a52656d6f74696e674d657461646174612c2056657273696f6e3d312e302e323632322e33313332362c2043756c747572"
    "653d6e65757472616c2c205075626c69634b6579546f6b656e3d6e756c6c05020000001b444f4a52656d6f74696e674d65746164617461"
    "2e4164647265737304000000065374726565740443697479055374617465035a697001010101030000000604000000114f6e65204d6963"
    "726f736f6674205761790605000000075265646d6f6e6406060000000257410607000000053938303534" + "0b"
)


def spec_address():
    return header(2) + bytes.fromhex(SPEC_ADDRESS_HEX)


if __name__ == "__main__":
    with open(os.path.join(HERE, "player.sav"), "wb") as f:
        f.write(player())
    with open(os.path.join(HERE, "spec-address.bin"), "wb") as f:
        f.write(spec_address())
    for name, make in (("extra.sav", extra), ("array-root.sav", array_root), ("string-root.sav", string_root)):
        with open(os.path.join(HERE, name), "wb") as f:
            f.write(make())
