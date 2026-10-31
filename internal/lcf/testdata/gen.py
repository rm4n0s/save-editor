#!/usr/bin/env python3
"""Makes the fixtures of the lcf package, and dumps a save file as text.

    python3 -I internal/lcf/testdata/gen.py            writes the fixtures next to this script
    python3 -I internal/lcf/testdata/gen.py dump FILE ENCODING
                                                         prints every value of FILE, one per line

The fixtures are assembled from the format as EasyRPG's liblcf describes it
(https://github.com/EasyRPG/liblcf, MIT license): the ids, names and types of the chunks are read from
the schema table in ../schema.go, so there is one table. Everything else here is independent of the
Go code: the BER numbers, the chunk nesting and the reading follow liblcf's src/reader_lcf.cpp
(LcfReader::ReadInt, Read<int32_t>(vector), ...) and src/reader_struct_impl.h (Struct<S>::ReadLcf).
The dump mode is a second reader, used by the Go tests to cross-check the first.

No tool of EasyRPG (lcf2xml) has checked these files.
"""
import json
import os
import re
import struct
import sys

HERE = os.path.dirname(os.path.abspath(__file__))


# ---- the schema table --------------------------------------------------------------------------

def load_schema():
    src = open(os.path.join(HERE, '..', 'schema.go'), encoding='utf-8').read()
    spec = re.search(r'const schemaSpec = `(.*?)`', src, re.S).group(1)
    structs, bases, cur = {}, {}, None
    for line in spec.split('\n'):
        parts = line.split()
        if not parts:
            continue
        if parts[0] == 'struct':
            cur = {'name': parts[1], 'id': 'id' in parts[2:], 'fields': []}
            for opt in parts[2:]:
                if opt.startswith('base='):
                    bases[cur['name']] = opt[5:]
            structs[cur['name']] = cur
            continue
        cur['fields'].append({'id': int(parts[0], 0), 'name': parts[1], 'type': parts[2],
                              '2k3': len(parts) > 3})
    for name, base in bases.items():
        structs[name]['fields'] = structs[base]['fields'] + structs[name]['fields']
    for s in structs.values():
        s['byid'] = {f['id']: f for f in s['fields']}
        s['byname'] = {f['name']: f for f in s['fields']}
    return structs


SCHEMA = load_schema()


# ---- writing -----------------------------------------------------------------------------------

def ber(n):
    """A BER number: 7-bit groups, first group first, high bit set on all but the last byte. A
    negative number is its 32-bit unsigned value (LcfWriter::WriteInt)."""
    n &= 0xFFFFFFFF
    groups = [n & 0x7F]
    n >>= 7
    while n:
        groups.append(n & 0x7F)
        n >>= 7
    groups.reverse()
    return bytes(g | 0x80 for g in groups[:-1]) + bytes([groups[-1]])


def chunk(cid, data):
    return ber(cid) + ber(len(data)) + data


def encode_value(typ, v):
    if typ == 'i32':
        return ber(v)
    if typ == 'bool':
        return bytes([1 if v else 0])
    if typ == 'str':
        return v
    if typ == 'f64':
        return struct.pack('<d', v)
    if typ in ('i8',):
        return struct.pack('<b', v)
    if typ in ('u8', 'flags'):
        return struct.pack('<B', v)
    if typ == 'u32':
        return struct.pack('<I', v)
    if typ == 'vbool':
        return bytes(1 if x else 0 for x in v)
    if typ == 'v16':
        return b''.join(struct.pack('<h', x) for x in v)
    if typ == 'v32':
        return b''.join(struct.pack('<i', x) for x in v)
    if typ == 'v8':
        return bytes(v)
    if typ == 'bytes':
        return v
    raise ValueError(typ)


def make_struct(sname, /, extra=(), terminate=True, **values):
    """The chunks of struct `sname` for the given values, by field name, in the order of their ids,
    with the unknown chunks `extra` (a list of (id, bytes)) in their place, ended by an id 0. A
    vector gets the chunk with its element count before it (the 'count' fields)."""
    sdef = SCHEMA[sname]
    items = []
    for fname, v in values.items():
        f = sdef['byname'][fname]
        t = f['type']
        if t.startswith('s:') or t.startswith('a:'):
            data = v  # already encoded by struct_ / array_
        else:
            data = encode_value(t, v)
            cnt = sdef['byname'].get(fname + '_count')
            if cnt is not None:
                n = len(v)
                items.append((cnt['id'], chunk(cnt['id'], ber(n))))
        items.append((f['id'], chunk(f['id'], data)))
    for cid, data in extra:
        items.append((cid, chunk(cid, data)))
    items.sort(key=lambda it: it[0])
    out = b''.join(d for _, d in items)
    return out + (b'\x00' if terminate else b'')


def array_(name, elems):
    """Array<name>: a count, then each element: its number if the struct has one, and its chunks.
    `elems` is a list of (number, struct bytes)."""
    out = ber(len(elems))
    for num, body in elems:
        if SCHEMA[name]['id']:
            out += ber(num)
        out += body
    return out


# ---- the fixtures ------------------------------------------------------------------------------

def music(name):
    return make_struct('Music', name=name, fadein=0, volume=100, tempo=100, balance=50)


def exec_state(frames):
    stack = array_('SaveEventExecFrame', [
        (i, make_struct('SaveEventExecFrame', commands=b'\x00\x01\x02', current_command=cc,
                        event_id=ev, triggered_by_decision_key=False, subcommand_path=[255, 1]))
        for i, (cc, ev) in enumerate(frames, 1)])
    return make_struct('SaveEventExecState', stack=stack, show_message=False, wait_time=3)


def build(edition, hero, actors, music_name, items, easyrpg_codepage=None, face=b'FACE01'):
    """One save. `hero` and the strings in `actors` and `music_name` are already encoded."""
    is2k3 = edition == 2003
    title = make_struct('SaveTitle', timestamp=45000.75, hero_name=hero, hero_level=12,
                        hero_hp=345, face1_name=face, face1_id=3)
    switches = [i % 3 == 0 for i in range(12)]
    # Variable 3 is 70000 and variable 4 is -2147483648, to have BER numbers of every length.
    variables = [0, 1, 127, 128, 300, 70000, -1, -2147483648, 2147483647, 5]
    system = make_struct(
        'SaveSystem', scene=5, frame_count=123456, graphics_name=b'System', message_stretch=0,
        font_id=0, switches=switches, variables=variables, message_position=2,
        message_prevent_overlap=1, face_name=b'', face_id=0, face_right=True,
        title_music=music(music_name), cursor_se=make_struct('Sound', name=b'Cursor', volume=100,
                                                            tempo=100, balance=50),
        transition_out=-1, transition_in=-1, teleport_allowed=True, save_allowed=True,
        background=b'Back', save_count=7, save_slot=1,
        **({'atb_mode': 1} if is2k3 else {}),
        extra=[(0xF1, b'custom \xff')])
    screen = make_struct('SaveScreen', tint_finish_red=100, tint_current_red=100.0,
                         flash_continuous=False, shake_strength=2, weather=1)
    pic_extra = {'spritesheet_cols': 4, 'spritesheet_rows': 2} if is2k3 else {}
    pictures = array_('SavePicture', [
        (1, make_struct('SavePicture', name=b'Pic01', start_x=1.5, start_y=-2.25, current_x=1.5,
                        flags=97, map_layer=7, **pic_extra)),
        (3, make_struct('SavePicture', name=b'Pic03', fixed_to_map=True, finish_magnify=50,
                        flags=0x21)),
    ])
    party = make_struct('SavePartyLocation', active=True, map_id=5, position_x=12, position_y=7,
                        direction=2, facing=2, move_speed=4, boarding=False, vehicle=0,
                        preboard_move_speed=4, pan_state=1, pan_current_x=-1, map_save_count=2)
    vehicle = lambda v: make_struct('SaveVehicleLocation', map_id=0, position_x=v, position_y=v,
                                    vehicle=v, orig_sprite_name=b'Boat', orig_sprite_id=0)
    actor_chunks = []
    for num, (name, ttl, sprite) in enumerate(actors, 1):
        kw = dict(name=name, title=ttl, sprite_name=sprite, sprite_id=0, level=5 * num,
                  exp=1234 * num, skills=[1, 2, 300], equipped=[1, 0, 0, 0, 0],
                  current_hp=99, current_sp=-1, status=[0, 0, 1], class_id=num)
        if is2k3:
            kw.update(battle_commands=[1, 2, 3, -1, -1, -1, -1], row=num % 2, two_weapon=bool(num % 2))
        extra = [(0x77, b'\x09\x08')] if num == 1 else []
        actor_chunks.append((num, make_struct('SaveActor', extra=extra, **kw)))
    actors_b = array_('SaveActor', actor_chunks)
    inventory = make_struct('SaveInventory', party=[1, 2], item_ids=items, item_counts=[3, 2, 99],
                            item_usage=[0, 0, 0], gold=1500, timer1_frames=119, timer1_active=True,
                            steps=40000)
    targets = array_('SaveTarget', [(1, make_struct('SaveTarget', map_id=3, map_x=4, map_y=5,
                                                    switch_on=True, switch_id=17))])
    mevent = lambda n, frames: make_struct(
        'SaveMapEvent', active=True, map_id=5, position_x=n, position_y=2 * n, direction=2,
        facing=2, move_speed=4, move_route=make_struct('MoveRoute', move_commands=b'\x01\x02',
                                                       repeat=True, skippable=False),
        waiting_execution=False, parallel_event_execstate=exec_state(frames))
    map_info = make_struct('SaveMapInfo', position_x=8, position_y=9, encounter_steps=-1, chipset_id=1,
                           events=array_('SaveMapEvent', [(1, mevent(1, [(2, 1)])), (2, mevent(2, []))]),
                           lower_tiles=list(range(8)), upper_tiles=[0] * 8, parallax_name=b'',
                           parallax_horz=False)
    panorama = make_struct('SavePanorama', pan_x=3, pan_y=-4)
    top = {
        'title': title, 'system': system, 'screen': screen, 'pictures': pictures,
        'party_location': party, 'boat_location': vehicle(1), 'ship_location': vehicle(2),
        'airship_location': vehicle(3), 'actors': actors_b, 'inventory': inventory,
        'targets': targets, 'map_info': map_info, 'panorama': panorama,
        'foreground_event_execstate': exec_state([]),
        'common_events': array_('SaveCommonEvent', [(1, make_struct(
            'SaveCommonEvent', parallel_event_execstate=exec_state([(1, 0)])))]),
    }
    if easyrpg_codepage:
        top['easyrpg_data'] = make_struct('SaveEasyRpgData', version=1, codepage=easyrpg_codepage)
    # The Save struct has no ending 0 (liblcf: conditional_zero_writer).
    body = make_struct('Save', terminate=False, extra=[(0x7F, b'\x01\x02\x03\x04')], **top)
    return ber(11) + b'LcfSaveData' + body


FIXTURES = {
    # RPG Maker 2000, Japanese: Shift-JIS (code page 932)
    'rm2k_sjis.lsd': dict(
        edition=2000, hero='勇者アリス'.encode('cp932'),
        actors=[('アリス'.encode('cp932'), '戦士'.encode('cp932'), b'Actor1'),
                ('ボブ'.encode('cp932'), b'', b'Actor2')],
        music_name='タイトル'.encode('cp932'), items=[1, 5, 9]),
    # RPG Maker 2003, Western: Windows-1252 (code page 1252)
    'rm2k3_cp1252.lsd': dict(
        edition=2003, hero='René Zoë'.encode('cp1252'),
        actors=[('Chloé'.encode('cp1252'), b'Magicien', b'Actor1'),
                ('Zoë'.encode('cp1252'), b'', b'Actor2')],
        music_name='Thème'.encode('cp1252'), items=[2, 4]),
    # RPG Maker 2003, Russian: Windows-1251, no code page in the file
    'rm2k3_cp1251.lsd': dict(
        edition=2003, hero='Алиса'.encode('cp1251'),
        actors=[('Борис'.encode('cp1251'), 'Воин'.encode('cp1251'), b'Actor1')],
        music_name='Тема'.encode('cp1251'), items=[7]),
    # Written by EasyRPG Player: the code page (1250) is in easyrpg_data
    'rm2k3_easyrpg_cp1250.lsd': dict(
        edition=2003, hero='Łukasz'.encode('cp1250'),
        actors=[('Żaneta'.encode('cp1250'), b'', b'Actor1')],
        music_name=b'Theme', items=[1], easyrpg_codepage=1250),
}


# ---- reading (a second implementation, after liblcf's LcfReader and Struct<S>::ReadLcf) ----------

class Reader:
    def __init__(self, data, encoding):
        self.d, self.enc, self.lines = data, encoding, []

    def read_ber(self, pos, limit):
        v, n = 0, 0
        while True:
            assert pos + n < limit, 'BER number runs past the data'
            b = self.d[pos + n]
            n += 1
            v = ((v << 7) | (b & 0x7F)) & 0xFFFFFFFF
            if not b & 0x80:
                return v, n
            assert n < 5, 'BER number too long'

    def emit(self, path, text):
        self.lines.append('%s = %s' % (path, text))

    def chunks(self, sname, pos, limit, path):
        sdef = SCHEMA[sname]
        while pos < limit:
            cid, n = self.read_ber(pos, limit)
            pos += n
            if cid == 0:
                return pos
            size, n = self.read_ber(pos, limit)
            pos += n
            end = pos + size
            assert end <= limit, 'chunk runs past its parent'
            f = sdef['byid'].get(cid)
            if f is None:
                self.emit(path + 'unknown_0x%02X' % cid, 'bytes ' + self.d[pos:end].hex())
            else:
                self.value(f, pos, end, path + f['name'])
            pos = end
        return pos

    def value(self, f, pos, end, path):
        t, raw = f['type'], self.d[pos:end]
        if t == 'count':
            return
        if t == 'i32':
            self.emit(path, str(0 if not raw else self.signed(self.read_ber(pos, end)[0])))
        elif t == 'bool':
            self.emit(path, 'true' if raw[0] else 'false')
        elif t == 'str':
            self.emit(path, json.dumps(raw.decode(self.enc)))
        elif t == 'f64':
            self.emit(path, repr(struct.unpack('<d', raw)[0]))
        elif t == 'i8':
            self.emit(path, str(struct.unpack('<b', raw)[0]))
        elif t in ('u8', 'flags'):
            self.emit(path, str(raw[0]))
        elif t == 'u32':
            self.emit(path, str(struct.unpack('<I', raw)[0]))
        elif t in ('vbool', 'v8', 'v16', 'v32'):
            width = {'vbool': 1, 'v8': 1, 'v16': 2, 'v32': 4}[t]
            numbered = f['name'] in ('switches', 'variables')
            for i in range(len(raw) // width):
                b = raw[i * width:(i + 1) * width]
                label = '%04d' % (i + 1) if numbered else '[%d]' % i
                if t == 'vbool':
                    text = 'true' if b[0] else 'false'
                elif t == 'v8':
                    text = str(b[0])
                else:
                    text = str(struct.unpack('<h' if t == 'v16' else '<i', b)[0])
                self.emit(path + '.' + label, text)
        elif t.startswith('s:'):
            self.chunks(t[2:], pos, end, path + '.')
        elif t.startswith('a:'):
            sname = t[2:]
            count, n = self.read_ber(pos, end)
            pos += n
            for i in range(count):
                num = i
                if SCHEMA[sname]['id']:
                    num, n = self.read_ber(pos, end)
                    pos += n
                pos = self.chunks(sname, pos, end, '%s.[%d].' % (path, num))
        elif t == 'bytes':
            self.emit(path, 'bytes ' + raw.hex())
        else:
            raise ValueError(t)

    @staticmethod
    def signed(v):
        return v - (1 << 32) if v & 0x80000000 else v


def dump(path, encoding):
    data = open(path, 'rb').read()
    r = Reader(data, encoding)
    assert r.read_ber(0, len(data)) == (11, 1) and data[1:12] == b'LcfSaveData', 'not an LCF save'
    r.chunks('Save', 12, len(data), '')
    print('\n'.join(r.lines))


def self_test():
    assert ber(0) == b'\x00' and ber(127) == b'\x7f' and ber(128) == b'\x81\x00'
    assert ber(300) == b'\x82\x2c' and ber(16383) == b'\xff\x7f' and ber(16384) == b'\x81\x80\x00'
    assert ber(-1) == b'\x8f\xff\xff\xff\x7f' and ber(2**31 - 1) == b'\x87\xff\xff\xff\x7f'
    assert ber(-2**31) == b'\x88\x80\x80\x80\x00'
    r = Reader(ber(-2**31), '')
    assert Reader.signed(r.read_ber(0, 5)[0]) == -2**31


def main():
    self_test()
    if len(sys.argv) == 4 and sys.argv[1] == 'dump':
        dump(sys.argv[2], sys.argv[3])
        return
    for name, kw in FIXTURES.items():
        data = build(**kw)
        with open(os.path.join(HERE, name), 'wb') as f:
            f.write(data)
        print('wrote', name, len(data), 'bytes')


main()
