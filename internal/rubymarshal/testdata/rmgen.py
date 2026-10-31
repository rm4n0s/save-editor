"""A small assembler of Ruby Marshal 4.8 dumps, written from marshal.c and doc/marshal.rdoc.

It is a library for gen.py of this package and of internal/rgss. Nodes:

  None, True, False           nil, true, false
  int                         Fixnum 'i' when it fits 31 bits, else Bignum 'l'
  Big(n)                      Bignum 'l' even when n is small
  Flt(x)                      Float 'f'
  Sym(name)                   Symbol (a link ';' when the name was written before)
  Str(text, enc, key)         String; enc is None (Ruby 1.8: no flag), 'utf8', 'ascii',
                              'binary', or the name of another encoding such as 'Shift_JIS'
  Arr([...], key), Hash([(k, v)...], key, default=NOCHILD), Obj(cls, [(ivar, v)...], key),
  Struct(cls, [(member, v)...], key), UserDef(cls, bytes, key), Cls(name), Mod(name),
  Regexp(src, opts, enc), UClass(cls, node) ('C'), Extended(mod, node) ('e'),
  Data(cls, node) ('d'), UserMarshal(cls, node) ('U')
  Ref(key)                    an object link '@' to the node that was given that key

Objects are numbered the way Ruby numbers them: in the order they are written (arrays,
hashes, objects and structs before their parts), a UserDef after its data. Fixnums, nil,
true, false and symbols are not objects.
"""
import struct

NOCHILD = object()


class Big:
    def __init__(self, n):
        self.n = n


class Flt:
    def __init__(self, x, text=None, key=None):
        self.x, self.text, self.key = x, text, key


class Sym:
    def __init__(self, name):
        self.name = name


class Str:
    def __init__(self, text, enc=None, key=None):
        self.text, self.enc, self.key = text, enc, key


class Arr:
    def __init__(self, items, key=None):
        self.items, self.key = items, key


class Hash:
    def __init__(self, pairs, key=None, default=NOCHILD):
        self.pairs, self.key, self.default = pairs, key, default


class Obj:
    def __init__(self, cls, ivars, key=None):
        self.cls, self.ivars, self.key = cls, ivars, key


class Struct(Obj):
    pass


class UserDef:
    def __init__(self, cls, data, key=None, enc=None):
        self.cls, self.data, self.key, self.enc = cls, data, key, enc


class Cls:
    def __init__(self, name, mod=False, old=False):
        self.name, self.mod, self.old = name, mod, old


class Regexp:
    def __init__(self, src, opts=0, enc='ascii'):
        self.src, self.opts, self.enc = src, opts, enc


class UClass:
    def __init__(self, cls, node):
        self.cls, self.node = cls, node


class Extended:
    def __init__(self, mod, node):
        self.mod, self.node = mod, node


class Data:
    def __init__(self, cls, node, key=None):
        self.cls, self.node, self.key = cls, node, key


class UserMarshal(Data):
    pass


class Ref:
    def __init__(self, key):
        self.key = key


def w_long(n):
    if n == 0:
        return b"\x00"
    if 0 < n < 123:
        return bytes([n + 5])
    if -124 < n < 0:
        return bytes([(n - 5) & 0xFF])
    buf = bytearray()
    for i in range(1, 5):
        buf.append(n & 0xFF)
        n >>= 8
        if n == 0:
            return bytes([i]) + bytes(buf)
        if n == -1:
            return bytes([(-i) & 0xFF]) + bytes(buf)
    raise ValueError("too big for w_long")


def w_float(x):
    """The text of Ruby's w_float (marshal.c), from the shortest digits Python can give."""
    import math
    if math.isinf(x):
        return b"-inf" if x < 0 else b"inf"
    if math.isnan(x):
        return b"nan"
    if x == 0:
        return b"-0" if math.copysign(1, x) < 0 else b"0"
    sign = "-" if x < 0 else ""
    # repr gives the shortest digits; read them and the decimal point from a Decimal.
    from decimal import Decimal
    t = Decimal(repr(abs(x))).as_tuple()
    digits = "".join(map(str, t.digits)).rstrip("0") or "0"
    decpt = len(t.digits) + t.exponent
    digs = len(digits)
    if decpt < -3 or decpt > digs:
        s = digits[0] + ("." + digits[1:] if digs > 1 else "") + "e%d" % (decpt - 1)
    elif decpt > 0:
        s = digits[:decpt] + ("." + digits[decpt:] if digs > decpt else "")
    else:
        s = "0." + "0" * (-decpt) + digits
    return (sign + s).encode()


class Writer:
    def __init__(self):
        self.out = bytearray(b"\x04\x08")
        self.syms = {}
        self.nobj = 0
        self.keys = {}
        self.enc_names = {}

    def sym(self, name):
        if name in self.syms:
            self.out += b";" + w_long(self.syms[name])
            return
        self.syms[name] = len(self.syms)
        raw = name.encode()
        self.out += b":" + w_long(len(raw)) + raw

    def register(self, key=None):
        if key is not None:
            assert key not in self.keys, key
            self.keys[key] = self.nobj
        self.nobj += 1

    def raw_str(self, node):
        if isinstance(node.text, bytes):
            return node.text
        if node.enc in (None, "utf8", "binary"):
            return node.text.encode("utf-8")
        if node.enc == "ascii":
            return node.text.encode("ascii")
        return node.text.encode(node.enc.lower().replace("windows-31j", "cp932"))

    def ivar_pairs(self, enc, with_count=True):
        """The instance variables that give a String its encoding (Ruby 1.9+)."""
        if enc in (None, "binary"):
            return None
        if enc == "utf8":
            return [("E", True)]
        if enc == "ascii":
            return [("E", False)]
        return [("encoding", enc)]

    def write_ivars(self, pairs):
        self.out += w_long(len(pairs))
        for name, val in pairs:
            self.sym(name)
            if isinstance(val, bool):
                self.out += b"T" if val else b"F"
            else:  # an encoding name: a String object, written once and linked after
                if val in self.enc_names:
                    self.out += b"@" + w_long(self.enc_names[val])
                else:
                    self.enc_names[val] = self.nobj
                    self.register()
                    self.out += b'"' + w_long(len(val)) + val.encode()

    def node(self, n):
        o = self.out
        if n is None:
            o += b"0"
        elif n is True:
            o += b"T"
        elif n is False:
            o += b"F"
        elif isinstance(n, int):
            if -(1 << 30) <= n < (1 << 30):
                o += b"i" + w_long(n)
            else:
                self.node(Big(n))
        elif isinstance(n, Big):
            self.register()
            mag = abs(n.n)
            nbytes = (mag.bit_length() + 7) // 8
            nbytes += nbytes % 2
            o += b"l" + (b"-" if n.n < 0 else b"+") + w_long(nbytes // 2)
            o += mag.to_bytes(nbytes, "little")
        elif isinstance(n, Flt):
            self.register(n.key)
            text = n.text.encode() if n.text is not None else w_float(n.x)
            o += b"f" + w_long(len(text)) + text
        elif isinstance(n, Sym):
            self.sym(n.name)
        elif isinstance(n, Str):
            pairs = self.ivar_pairs(n.enc)
            if pairs:
                o += b"I"
            self.register(n.key)
            raw = self.raw_str(n)
            o += b'"' + w_long(len(raw)) + raw
            if pairs:
                self.write_ivars(pairs)
        elif isinstance(n, Arr):
            self.register(n.key)
            o += b"[" + w_long(len(n.items))
            for it in n.items:
                self.node(it)
        elif isinstance(n, Hash):
            self.register(n.key)
            o += (b"{" if n.default is NOCHILD else b"}") + w_long(len(n.pairs))
            for k, v in n.pairs:
                self.node(k)
                self.node(v)
            if n.default is not NOCHILD:
                self.node(n.default)
        elif isinstance(n, Obj):
            self.register(n.key)
            o += b"S" if isinstance(n, Struct) else b"o"
            self.sym(n.cls)
            o += w_long(len(n.ivars))
            for name, v in n.ivars:
                self.sym(name)
                self.node(v)
        elif isinstance(n, UserDef):
            pairs = self.ivar_pairs(n.enc) if n.enc else None
            if pairs:
                o += b"I"
            o += b"u"
            self.sym(n.cls)
            o += w_long(len(n.data)) + n.data
            if pairs:
                self.write_ivars(pairs)
            self.register(n.key)
        elif isinstance(n, Cls):
            self.register()
            o += (b"M" if n.old else b"m" if n.mod else b"c") + w_long(len(n.name)) + n.name.encode()
        elif isinstance(n, Regexp):
            pairs = self.ivar_pairs(n.enc)
            if pairs:
                o += b"I"
            self.register()
            o += b"/" + w_long(len(n.src)) + n.src.encode() + bytes([n.opts])
            if pairs:
                self.write_ivars(pairs)
        elif isinstance(n, UClass):
            o += b"C"
            self.sym(n.cls)
            self.node(n.node)
        elif isinstance(n, Extended):
            o += b"e"
            self.sym(n.mod)
            self.node(n.node)
        elif isinstance(n, UserMarshal):
            self.register(n.key)
            o += b"U"
            self.sym(n.cls)
            self.node(n.node)
        elif isinstance(n, Data):
            self.register(n.key)
            o += b"d"
            self.sym(n.cls)
            self.node(n.node)
        elif isinstance(n, Ref):
            o += b"@" + w_long(self.keys[n.key])
        else:
            raise TypeError(repr(n))


def dump(node):
    """Marshal.dump(node) as bytes."""
    w = Writer()
    w.node(node)
    return bytes(w.out)


def table(xs, ys, zs, cells):
    """The _dump of an RGSS Table."""
    dim = 1 + (ys > 1 or zs > 1) + (zs > 1)
    assert len(cells) == xs * ys * zs
    return struct.pack("<5i", dim, xs, ys, zs, len(cells)) + struct.pack("<%dh" % len(cells), *cells)


def color(r, g, b, a):
    """The _dump of a Color (or a Tone: red, green, blue, gray)."""
    return struct.pack("<4d", r, g, b, a)


def rect(x, y, w, h):
    """The _dump of a Rect."""
    return struct.pack("<4i", x, y, w, h)
