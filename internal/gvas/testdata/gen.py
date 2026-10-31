"""Assembles the small Unreal Engine SaveGame (GVAS) fixtures of internal/gvas, and, for each, an
.expected file that lists every value the file holds.

Run: python3 -I internal/gvas/testdata/gen.py

The layout follows Unreal's FGameSaveHeader and FPropertyTag, as the open-source readers
(uesave, trumank/uesave-rs) document them. No Unreal Engine or uesave was available when these
were written, so nothing here has been checked by a tool that did not come from the same
reading of the format.

Every builder returns (bytes, expect): expect is a list of (path, description) in the order the
Go side lists the values of the tree. The first path of a property is its name; the children of
a struct, array or map follow under "name/label".
"""
import json
import os
import struct
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


# ---------------------------------------------------------------- primitives
def u8(n): return struct.pack('<B', n)
def i8(n): return struct.pack('<b', n)
def u16(n): return struct.pack('<H', n)
def i16(n): return struct.pack('<h', n)
def u32(n): return struct.pack('<I', n)
def i32(n): return struct.pack('<i', n)
def i64(n): return struct.pack('<q', n)
def u64(n): return struct.pack('<Q', n)
def f32(x): return struct.pack('<f', x)
def f64(x): return struct.pack('<d', x)


def fstr(s):
    """An FString: ASCII with a null terminator, UTF-16 with a negative length otherwise."""
    if s == '':
        return i32(0)
    if all(ord(c) < 0x80 for c in s):
        return i32(len(s) + 1) + s.encode('ascii') + b'\0'
    u = s.encode('utf-16-le') + b'\0\0'
    return i32(-(len(u) // 2)) + u


def guid_bytes(hexs):
    """Bytes of an FGuid from the 32 digits UE prints (four little-endian words)."""
    out = b''
    for i in range(0, 32, 8):
        out += struct.pack('<I', int(hexs[i:i + 8], 16))
    return out


def jstr(s):
    return json.dumps(s, ensure_ascii=False)


def fl(x):
    return repr(float(x))


# ---------------------------------------------------------------- values
# A value is (bytes, expect-of-the-value); expect paths are relative ('' is the value itself).
def v_int(n, w, signed=True):
    b = n.to_bytes(w, 'little', signed=signed)
    return b, [('', 'int %d' % n)]


def v_float(x): return f32(x), [('', 'float ' + fl(x))]
def v_double(x): return f64(x), [('', 'float ' + fl(x))]
def v_str(s): return fstr(s), [('', 'str ' + jstr(s))]
def v_bool(b): return u8(1 if b else 0), [('', 'bool ' + ('true' if b else 'false'))]


def v_vec(xyz, double, names=('x', 'y', 'z'), typ='Vector'):
    pack = f64 if double else f32
    b = b''.join(pack(c) for c in xyz)
    return b, [('', 'object ' + typ)] + [(n, 'float ' + fl(c)) for n, c in zip(names, xyz)]


def v_color(bgra):
    b = bytes(bgra)
    bb, g, r, a = bgra
    return b, [('', 'object Color'), ('r', 'int %d' % r), ('g', 'int %d' % g), ('b', 'int %d' % bb), ('a', 'int %d' % a)]


def v_lincolor(rgba):
    return b''.join(f32(c) for c in rgba), [('', 'object LinearColor')] + [(n, 'float ' + fl(c)) for n, c in zip('rgba', rgba)]


def v_guid(hexs):
    return guid_bytes(hexs), [('', 'str ' + jstr(hexs))]


def v_intpoint(x, y):
    return i32(x) + i32(y), [('', 'object IntPoint'), ('x', 'int %d' % x), ('y', 'int %d' % y)]


def v_ticks(n): return i64(n), [('', 'int %d' % n)]


def prefixed(name, expect):
    return [(name + ('/' + p if p else ''), d) for p, d in expect]


# ---------------------------------------------------------------- classic tags
def tag(name, typ, size, hdr=b'', index=0, guid=True):
    return fstr(name) + fstr(typ) + u32(size) + u32(index) + hdr + (b'\0' if guid else b'')


def prop(name, typ, val, hdr=b'', index=0, label=None):
    body, exp = val
    label = label or name
    return tag(name, typ, len(body), hdr, index) + body, prefixed(label, exp)


def P_bool(name, b):
    return tag(name, 'BoolProperty', 0, u8(1 if b else 0)), [(name, 'bool ' + ('true' if b else 'false'))]


def P_int(name, n, typ='IntProperty', w=4, signed=True, index=0):
    label = name + ('[%d]' % index if index else '')
    return prop(name, typ, v_int(n, w, signed), index=index, label=label)


def P_str(name, s, typ='StrProperty'): return prop(name, typ, v_str(s))


def P_enumbyte(name, enum, value):
    if enum is None:
        return prop(name, 'ByteProperty', v_int(value, 1, False), hdr=fstr('None'))
    return prop(name, 'ByteProperty', v_str(value), hdr=fstr(enum))


def P_struct(name, sname, val):
    return prop(name, 'StructProperty', val, hdr=fstr(sname) + b'\0' * 16)


def plist(items, typ):
    """A struct as a list of properties; items are (bytes, expect) of properties."""
    body = b''.join(b for b, _ in items) + fstr('None')
    exp = [('', 'object ' + typ)]
    for _, e in items:
        exp += e
    return body, exp


def P_plist(name, sname, items):
    return P_struct(name, sname, plist(items, sname))


def elems(vals):
    """Elements without tags: (bytes, expect) with labels [i]."""
    body, exp = b'', []
    for i, (b, e) in enumerate(vals):
        body += b
        exp += prefixed('[%d]' % i, e)
    return body, exp


def P_array(name, inner, vals, extra=b''):
    body, exp = elems(vals)
    return tag(name, 'ArrayProperty', 4 + len(extra) + len(body), fstr(inner)) + u32(len(vals)) + extra + body, \
        [(name, 'list %d' % len(vals))] + prefixed(name, exp)


def P_struct_array(name, sname, vals):
    """Array of structs, classic layout: the elements are preceded by an inner tag."""
    body, exp = elems(vals)
    inner = fstr(name) + fstr('StructProperty') + u32(len(body)) + u32(0) + fstr(sname) + b'\0' * 16 + b'\0'
    total = u32(len(vals)) + inner + body
    return tag(name, 'ArrayProperty', len(total), fstr('StructProperty')) + total, \
        [(name, 'list %d' % len(vals))] + prefixed(name, exp)


def P_set(name, inner, vals):
    body, exp = elems(vals)
    total = u32(0) + u32(len(vals)) + body
    return tag(name, 'SetProperty', len(total), fstr(inner)) + total, [(name, 'list %d' % len(vals))] + prefixed(name, exp)


def P_map(name, ktype, vtype, entries, hdr_k=None):
    """entries: list of (label or None, key (bytes, expect), value (bytes, expect)). A label
    means the key is shown as the label; None means entries are shown as [i] with key/value."""
    body, exp = b'', []
    for i, (label, k, v) in enumerate(entries):
        body += k[0] + v[0]
        if label is not None:
            exp += prefixed(label, v[1])
        else:
            exp.append(('[%d]' % i, 'object MapEntry'))
            exp += prefixed('[%d]/key' % i, k[1]) + prefixed('[%d]/value' % i, v[1])
    total = u32(0) + u32(len(entries)) + body
    return tag(name, 'MapProperty', len(total), fstr(ktype) + fstr(vtype)) + total, \
        [(name, 'map %d' % len(entries))] + prefixed(name, exp)


def P_text_base(name, ns, key, src):
    body = u32(0) + i8(0) + fstr(ns) + fstr(key) + fstr(src)
    return tag(name, 'TextProperty', len(body)) + body, [(name, 'str ' + jstr(src))]


def P_text_none(name, s):
    body = u32(0) + i8(-1) + u32(1) + fstr(s)
    return tag(name, 'TextProperty', len(body)) + body, [(name, 'str ' + jstr(s))]


def P_raw(name, typ, body, hdr=b''):
    return tag(name, typ, len(body), hdr) + body, [(name, 'bytes ' + body.hex())]


# ---------------------------------------------------------------- header
CUSTOMS = [(bytes(range(16)), 1), (bytes(range(16, 32)), 7)]


def header(sgv, ue4, ue5, ver, branch, cls, tagbyte=None, customs=CUSTOMS):
    out = b'GVAS' + i32(sgv) + i32(ue4)
    if sgv >= 3:
        out += i32(ue5)
    out += u16(ver[0]) + u16(ver[1]) + u16(ver[2]) + u32(ver[3]) + fstr(branch)
    if sgv >= 2:
        out += u32(3) + u32(len(customs))
        for g, v in customs:
            out += g + i32(v)
    out += fstr(cls)
    if tagbyte is not None:
        out += u8(tagbyte)
    return out


def finish(head, props):
    body = b''.join(b for b, _ in props) + fstr('None') + u32(0)
    exp = []
    for _, e in props:
        exp += e
    return head + body, exp


def write(name, data, exp):
    with open(os.path.join(HERE, name), 'wb') as f:
        f.write(data)
    with open(os.path.join(HERE, name + '.expected'), 'w', encoding='utf-8', newline='\n') as f:
        for p, d in exp:
            f.write('%s\t%s\n' % (p, d))


G1 = '00112233445566778899AABBCCDDEEFF'
G2 = 'FFEEDDCCBBAA99887766554433221100'


# ---------------------------------------------------------------- UE 4.27
def ue4():
    head = header(2, 522, None, (4, 27, 2, 17155), '++UE4+Release-4.27', '/Game/Blueprints/BP_Save.BP_Save_C')
    props = [
        P_bool('bAlive', True),
        P_bool('bMuted', False),
        P_int('Level', 7),
        P_int('Gold', 5_000_000_000, 'Int64Property', 8),
        P_int('Small', -5, 'Int8Property', 1),
        P_int('Short', -300, 'Int16Property', 2),
        P_int('Port', 65535, 'UInt16Property', 2, False),
        P_int('Flags', 4_000_000_000, 'UInt32Property', 4, False),
        P_int('Big', 2**63 + 5, 'UInt64Property', 8, False),
        prop('Speed', 'FloatProperty', v_float(2.25)),
        prop('Precise', 'DoubleProperty', v_double(0.1)),
        P_str('PlayerName', 'Hero'),
        P_str('Empty', ''),
        P_str('Greeting', 'héllo ☃'),
        P_str('NickName', 'Bob', 'NameProperty'),
        P_enumbyte('Raw', None, 200),
        P_enumbyte('Color', 'EColor', 'EColor::Red'),
        prop('Mode', 'EnumProperty', v_str('EMode::Hard'), hdr=fstr('EMode')),
        P_text_base('Title', '', 'KEY', 'Hello World'),
        P_text_none('Plain', 'Invariant'),
        P_raw('Table', 'TextProperty', u32(0) + i8(11) + fstr('Table') + fstr('Key')),
        P_str('Target', '/Game/Maps/Level1.Level1', 'ObjectProperty'),
        prop('Soft', 'SoftObjectProperty', (fstr('/Game/Items/Sword.Sword') + fstr(''),
                                            [('', 'object SoftObjectProperty'), ('asset', 'str "/Game/Items/Sword.Sword"'), ('subpath', 'str ""')])),
        P_struct('Pos', 'Vector', v_vec((1.0, 2.5, -3.0), False)),
        P_struct('Rot', 'Rotator', v_vec((10.0, 90.0, 0.0), False, ('pitch', 'yaw', 'roll'), 'Rotator')),
        P_struct('Q', 'Quat', v_vec((0.0, 0.0, 0.0, 1.0), False, ('x', 'y', 'z', 'w'), 'Quat')),
        P_struct('Pos2', 'Vector2D', v_vec((1.5, 2.5), False, ('x', 'y'), 'Vector2D')),
        P_struct('Tint', 'LinearColor', v_lincolor((1.0, 0.5, 0.25, 1.0))),
        P_struct('Col', 'Color', v_color((30, 20, 10, 255))),
        P_struct('Id', 'Guid', v_guid(G1)),
        P_struct('When', 'DateTime', v_ticks(637000000000000000)),
        P_struct('Dur', 'Timespan', v_ticks(36000000000)),
        P_struct('Cell', 'IntPoint', v_intpoint(3, -4)),
        P_plist('Inventory', 'FInventory', [
            P_int('Gold', 5),
            P_array('Items', 'StrProperty', [v_str('Sword'), v_str('Shield')]),
            P_plist('Owner', 'FOwner', [P_str('Name', 'Ann'), P_struct('Pos', 'Vector', v_vec((0.5, 0.5, 0.5), False))]),
        ]),
        (tag('Mystery', 'StructProperty', 5, fstr('FMystery') + b'\0' * 16) + bytes([1, 2, 3, 4, 5]),
         [('Mystery', 'bytes 0102030405')]),
        (tag('Odd', 'FooProperty', 3) + b'abc', [('Odd', 'bytes ' + b'abc'.hex())]),
        P_int('Arr', 1, index=0),
        P_int('Arr', 2, index=1),
        P_array('Scores', 'IntProperty', [v_int(10, 4), v_int(20, 4), v_int(30, 4)]),
        P_array('Names', 'StrProperty', [v_str('a'), v_str('über')]),
        P_array('Bytes', 'ByteProperty', [v_int(1, 1, False), v_int(2, 1, False), v_int(3, 1, False)]),
        P_array('Colors', 'ByteProperty', [v_str('EColor::Red'), v_str('EColor::Blue')]),
        P_array('Switches', 'BoolProperty', [v_bool(True), v_bool(False)]),
        P_array('Floats', 'FloatProperty', [v_float(0.5), v_float(-1.0)]),
        P_array('NoItems', 'IntProperty', []),
        P_array('Objs', 'ObjectProperty', [v_str('/Game/A.A'), v_str('/Game/B.B')]),
        P_struct_array('Points', 'FPoint', [plist([P_int('X', 1), P_str('Name', 'one')], 'FPoint'),
                                             plist([P_int('X', 2), P_str('Name', 'two')], 'FPoint')]),
        P_struct_array('Positions', 'Vector', [v_vec((1.0, 2.0, 3.0), False), v_vec((4.0, 5.0, 6.0), False)]),
        P_set('Seen', 'IntProperty', [v_int(1, 4), v_int(2, 4), v_int(3, 4)]),
        P_set('Tags', 'StrProperty', [v_str('x'), v_str('y')]),
        P_map('Counts', 'StrProperty', 'IntProperty',
              [('apples', v_str('apples'), v_int(3, 4)), ('pears', v_str('pears'), v_int(5, 4))]),
        P_map('Names2', 'IntProperty', 'StrProperty', [('1', v_int(1, 4), v_str('one')), ('2', v_int(2, 4), v_str('two'))]),
        P_map('ById', 'StructProperty', 'IntProperty',
              [(G1, v_guid(G1), v_int(1, 4)), (G2, v_guid(G2), v_int(2, 4))]),
        P_map('Slots', 'StrProperty', 'StructProperty',
              [('a', v_str('a'), plist([P_int('Count', 2), P_str('Name', 'x')], 'Struct')),
               ('b', v_str('b'), plist([P_int('Count', 3), P_str('Name', 'y')], 'Struct'))]),
        P_map('StructKeys', 'StructProperty', 'IntProperty',
              [(None, plist([P_int('A', 1)], 'Struct'), v_int(10, 4)),
               (None, plist([P_int('A', 2)], 'Struct'), v_int(20, 4))]),
        P_map('EnumMap', 'ByteProperty', 'IntProperty',
              [('EColor::Red', v_str('EColor::Red'), v_int(1, 4)), ('EColor::Blue', v_str('EColor::Blue'), v_int(2, 4))]),
    ]
    # A map of Vector values: the file does not name the struct, so it is kept as bytes.
    vb = f32(1.0) + f32(2.0) + f32(3.0)
    body = u32(0) + u32(1) + fstr('v') + vb
    props.append((tag('VecMap', 'MapProperty', len(body), fstr('StrProperty') + fstr('StructProperty')) + body,
                  [('VecMap', 'bytes ' + body.hex())]))
    data, exp = finish(head, props)
    write('ue4.sav', data, exp)


# ---------------------------------------------------------------- UE 5.1 (classic tags, doubles)
def ue5():
    head = header(3, 522, 1008, (5, 1, 1, 23901000), '++UE5+Release-5.1', '/Game/BP_Save5.BP_Save5_C')
    props = [
        P_bool('bAlive', True),
        P_int('Level', 12),
        prop('Speed', 'FloatProperty', v_float(0.5)),
        P_str('PlayerName', 'Ünï'),
        P_struct('Pos', 'Vector', v_vec((1.0, 2.5, -3.0), True)),
        P_struct('Rot', 'Rotator', v_vec((10.0, 90.0, 0.0), True, ('pitch', 'yaw', 'roll'), 'Rotator')),
        P_struct('Tint', 'LinearColor', v_lincolor((1.0, 0.5, 0.25, 1.0))),
        P_struct_array('Positions', 'Vector', [v_vec((1.0, 2.0, 3.0), True), v_vec((4.0, 5.0, 6.0), True)]),
        P_struct_array('Points', 'FPoint', [plist([P_int('X', 1)], 'FPoint')]),
        P_enumbyte('Color', 'EColor', 'EColor::Green'),
        prop('Soft', 'SoftObjectProperty', (fstr('/Game/Items') + fstr('Sword') + fstr(''),
                                            [('', 'object SoftObjectProperty'), ('package', 'str "/Game/Items"'),
                                             ('asset', 'str "Sword"'), ('subpath', 'str ""')])),
        P_text_base('Title', 'NS', 'KEY', 'Hi'),
        P_map('Counts', 'StrProperty', 'IntProperty', [('a', v_str('a'), v_int(1, 4))]),
        P_set('Seen', 'IntProperty', [v_int(5, 4), v_int(6, 4)]),
    ]
    data, exp = finish(head, props)
    write('ue5.sav', data, exp)


# ---------------------------------------------------------------- UE 5.4 (type-name tags)
def node(name, kids=()):
    return fstr(name) + u32(len(kids)) + b''.join(kids)


def pnode(asset, package='/Script/CoreUObject'):
    return node(asset, [node(package)])


def tag54(name, tnode, size, flags=0, index=None, guid=False):
    out = fstr(name) + tnode + u32(size) + u8(flags)
    if index is not None:
        out += u32(index)
    if guid:
        out += bytes(range(100, 116))
    return out


def prop54(name, tnode, val, flags=0, index=None, guid=False, label=None):
    body, exp = val
    f = flags | (0x01 if index is not None else 0) | (0x02 if guid else 0)
    return tag54(name, tnode, len(body), f, index, guid) + body, prefixed(label or name, exp)


T_INT = node('IntProperty')
T_STR = node('StrProperty')
T_FLOAT = node('FloatProperty')


def T_STRUCT(asset, package='/Script/CoreUObject'):
    return node('StructProperty', [pnode(asset, package)])


def ue54():
    head = header(3, 522, 1012, (5, 4, 2, 34000000), '++UE5+Release-5.4', '/Game/BP_Save54.BP_Save54_C', tagbyte=0)
    vec = lambda xyz: v_vec(xyz, True)
    props = [
        (tag54('bAlive', node('BoolProperty'), 0, 0x10), [('bAlive', 'bool true')]),
        (tag54('bMuted', node('BoolProperty'), 0, 0), [('bMuted', 'bool false')]),
        prop54('Level', T_INT, v_int(9, 4)),
        prop54('Speed', T_FLOAT, v_float(2.25)),
        prop54('PlayerName', T_STR, v_str('Hero')),
        prop54('Raw', node('ByteProperty'), v_int(7, 1, False)),
        prop54('Color', node('ByteProperty', [pnode('EColor', '/Game/Enums')]), v_str('EColor::Red')),
        prop54('Mode', node('EnumProperty', [pnode('EMode', '/Game/Enums'), node('ByteProperty')]), v_str('EMode::Hard')),
        prop54('Pos', T_STRUCT('Vector'), vec((1.0, 2.5, -3.0)), flags=0x08),
        prop54('Col', T_STRUCT('Color'), v_color((30, 20, 10, 255)), flags=0x08),
        prop54('Inventory', T_STRUCT('FInventory', '/Game/BP_Save54'),
               plist([prop54('Gold', T_INT, v_int(5, 4)), prop54('Name', T_STR, v_str('Ann'))], 'FInventory')),
        prop54('Mystery', T_STRUCT('FMystery', '/Game/BP_Save54'), (bytes([9, 8, 7]), [('', 'bytes 090807')]), flags=0x08),
        prop54('Scores', node('ArrayProperty', [T_INT]), (u32(2) + i32(1) + i32(2), [('', 'list 2'), ('[0]', 'int 1'), ('[1]', 'int 2')])),
        prop54('Points', node('ArrayProperty', [T_STRUCT('FPoint', '/Game/BP_Save54')]),
               (lambda e: (u32(2) + e[0][0] + e[1][0],
                           [('', 'list 2')] + prefixed('[0]', e[0][1]) + prefixed('[1]', e[1][1])))(
                   [plist([prop54('X', T_INT, v_int(1, 4))], 'FPoint'), plist([prop54('X', T_INT, v_int(2, 4))], 'FPoint')])),
        prop54('Positions', node('ArrayProperty', [T_STRUCT('Vector')]),
               (u32(2) + vec((1.0, 2.0, 3.0))[0] + vec((4.0, 5.0, 6.0))[0],
                [('', 'list 2')] + prefixed('[0]', vec((1.0, 2.0, 3.0))[1]) + prefixed('[1]', vec((4.0, 5.0, 6.0))[1]))),
        prop54('Slots', node('MapProperty', [T_STR, T_STRUCT('FSlot', '/Game/BP_Save54')]),
               (lambda v: (u32(0) + u32(1) + fstr('a') + v[0], [('', 'map 1')] + prefixed('a', v[1])))(
                   plist([prop54('Count', T_INT, v_int(3, 4))], 'FSlot'))),
        prop54('Keys', node('MapProperty', [T_STRUCT('FKey', '/Game/BP_Save54'), T_INT]),
               (lambda k: (u32(0) + u32(1) + k[0] + i32(10),
                           [('', 'map 1'), ('[0]', 'object MapEntry')] + prefixed('[0]/key', k[1]) + [('[0]/value', 'int 10')]))(
                   plist([prop54('A', T_INT, v_int(1, 4))], 'FKey'))),
        prop54('Tags', node('SetProperty', [T_STR]), (u32(0) + u32(2) + fstr('x') + fstr('y'),
                                                     [('', 'list 2'), ('[0]', 'str "x"'), ('[1]', 'str "y"')])),
        prop54('Arr', T_INT, v_int(1, 4)),
        prop54('Arr', T_INT, v_int(2, 4), index=1, guid=True, label='Arr[1]'),
    ]
    data, exp = finish(head, props)
    write('ue54.sav', data, exp)


if __name__ == '__main__':
    ue4()
    ue5()
    ue54()
    print('wrote fixtures to', HERE, file=sys.stderr)
