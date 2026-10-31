"""Makes the fixtures of package rgss: a save of RPG Maker XP, one of VX, one of VX Ace, and
an XP save with two dumps that a script appended.

    python3 -I internal/rgss/testdata/gen.py

Dumps are assembled by internal/rubymarshal/testdata/rmgen.py (from Ruby's marshal.c), in the
order of the default scripts: XP Scene_Save#write_save_data, VX Scene_File#write_save_data and
VX Ace DataManager.save_game_without_rescue. XP and VX run on Ruby 1.8, whose Strings carry no
encoding; VX Ace runs on Ruby 1.9, whose Strings carry :E (or :encoding).
"""
import importlib.util
import os

here = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location(
    "rmgen", os.path.join(here, "..", "..", "rubymarshal", "testdata", "rmgen.py"))
g = importlib.util.module_from_spec(spec)
spec.loader.exec_module(g)

Obj, Str, Arr, Hash, Ref, Flt, Big, UserDef = g.Obj, g.Str, g.Arr, g.Hash, g.Ref, g.Flt, g.Big, g.UserDef


def write(name, nodes):
    with open(os.path.join(here, name), "wb") as f:
        for n in nodes:
            f.write(g.dump(n))


def actors(s, extra_key_prefix=""):
    """Game_Actors with two actors that share the String of their character graphic."""
    k = extra_key_prefix
    actor1 = Obj("Game_Actor", [
        ("@actor_id", 1),
        ("@name", s("Aluxes", k + "name1")),
        ("@character_name", s("001-Fighter01", k + "graphic")),
        ("@character_hue", 0),
        ("@class_id", 1),
        ("@level", 5),
        ("@exp", 1234),
        ("@hp", 450),
        ("@sp", 100),
        ("@states", Arr([])),
    ], key=k + "actor1")
    actor2 = Obj("Game_Actor", [
        ("@actor_id", 2),
        ("@name", s("Basil", k + "name2")),
        ("@character_name", Ref(k + "graphic")),
        ("@character_hue", 30),
        ("@class_id", 2),
        ("@level", 4),
        ("@exp", 800),
        ("@hp", 300),
        ("@sp", 80),
        ("@states", Arr([2])),
    ], key=k + "actor2")
    return Obj("Game_Actors", [("@data", Arr([None, actor1, actor2]))]), k + "actor1", k + "actor2"


def switches():
    return Obj("Game_Switches", [("@data", Arr([None, True, False, True, False, True]))])


def variables():
    return Obj("Game_Variables", [("@data", Arr([None, 0, 100, Big(2 ** 40), -5, 1234567]))])


def party(s):
    """Game_Party. Each dump is a Marshal.dump of its own, so the actors of the party are
    copies of those of $game_actors, as they are in a real save; the two share a String."""
    return Obj("Game_Party", [
        ("@actors", Arr([
            Obj("Game_Actor", [("@actor_id", 1), ("@name", s("Aluxes")),
                               ("@character_name", s("001-Fighter01", "pgraphic")), ("@hp", 450)]),
            Obj("Game_Actor", [("@actor_id", 2), ("@name", s("Basil")),
                               ("@character_name", Ref("pgraphic")), ("@hp", 300)]),
        ])),
        ("@gold", 1234),
        ("@steps", 3000),
        ("@items", Hash([(1, 5), (2, 3)])),
        ("@weapons", Hash([])),
        ("@armors", Hash([])),
    ])


def game_map(s):
    return Obj("Game_Map", [
        ("@map_id", 1),
        ("@map", Obj("RPG::Map", [
            ("@width", 4), ("@height", 3),
            ("@bgm", Obj("RPG::AudioFile", [("@name", s("001-Battle01")), ("@volume", 100), ("@pitch", 100)])),
            ("@data", UserDef("Table", g.table(4, 3, 2, list(range(24))))),
        ])),
        ("@display_x", 0),
        ("@scroll_rest", Flt(0.0)),
    ])


def player(s):
    return Obj("Game_Player", [
        ("@x", 10), ("@y", 8), ("@direction", 2), ("@move_speed", 4),
        ("@character_name", s("001-Fighter01")), ("@through", False),
    ])


def plain(text, key=None):
    """A String of Ruby 1.8: no encoding."""
    return Str(text, None, key)


# --- XP ---------------------------------------------------------------------------
def xp(extra=()):
    s = plain
    act, a1, a2 = actors(s)
    screen = Obj("Game_Screen", [
        ("@tone", UserDef("Tone", g.color(0.0, -34.0, 17.5, 0.0))),
        ("@brightness", 255),
        ("@pictures", Arr([None, Obj("Game_Picture", [
            ("@number", 1), ("@name", s("title")),
            ("@zoom_x", Flt(100.0, key="zoom")), ("@zoom_y", Ref("zoom")), ("@opacity", Flt(255.0)),
        ])])),
    ])
    nodes = [
        Arr([Arr([s("001-Fighter01"), 0]), Arr([s("002-Fighter02"), 30])]),
        144000,
        Obj("Game_System", [
            ("@save_disabled", False), ("@menu_disabled", False), ("@encounter_disabled", False),
            ("@message_position", 2), ("@message_frame", 0), ("@save_count", 7), ("@magic_number", 12345),
        ]),
        switches(),
        variables(),
        Obj("Game_SelfSwitches", [("@data", Hash([(Arr([1, 3, s("A")]), True)]))]),
        screen,
        act,
        party(s),
        Obj("Game_Troop", [("@enemies", Arr([]))]),
        game_map(s),
        player(s),
    ]
    return nodes + list(extra)


write("xp.rxdata", xp())
write("xp-extra.rxdata", xp([Hash([(plain("quest"), 3), (plain("flag"), True)]), plain("hello")]))


# --- VX ---------------------------------------------------------------------------
def vx():
    s = plain
    act, a1, a2 = actors(s)
    return [
        Arr([Arr([s("Actor1"), 0]), Arr([s("Actor2"), 3])]),
        216000,
        Obj("RPG::BGM", [("@name", s("Theme1")), ("@volume", 90), ("@pitch", 100)]),
        None,
        Obj("Game_System", [
            ("@save_disabled", False), ("@menu_disabled", False), ("@encounter_disabled", False),
            ("@save_count", 3), ("@version_id", 0),
        ]),
        Obj("Game_Message", [("@texts", Arr([])), ("@background", 0), ("@position", 2)]),
        switches(),
        variables(),
        Obj("Game_SelfSwitches", [("@data", Hash([(Arr([1, 3, s("A")]), True)]))]),
        act,
        party(s),
        Obj("Game_Troop", [("@enemies", Arr([]))]),
        game_map(s),
        player(s),
    ]


write("vx.rvdata", vx())


# --- VX Ace -----------------------------------------------------------------------
def ace():
    def u(text, key=None):
        return Str(text, "utf8", key)

    def a(text, key=None):
        return Str(text, "ascii", key)

    actor1 = Obj("Game_Actor", [
        ("@actor_id", 1),
        ("@name", u("Eric", "name1")),
        ("@nickname", Str("勇者", "Shift_JIS", "nick1")),
        ("@character_name", a("Actor1", "graphic")),
        ("@character_index", 0),
        ("@level", 5),
        ("@hp", 450),
        ("@mp", 88),
        ("@exp", Arr([None, 0, 0, 0, 0, 0, 0, 0, 0, 0])),
    ], key="actor1")
    actor2 = Obj("Game_Actor", [
        ("@actor_id", 2),
        ("@name", u("ラルフ", "name2")),
        ("@nickname", Str("騎士", "Shift_JIS", "nick2")),
        ("@character_name", Ref("graphic")),
        ("@character_index", 1),
        ("@level", 4),
        ("@hp", 300),
        ("@mp", 40),
        ("@exp", Arr([None, 0, 0, 0, 0, 0, 0, 0, 0, 0])),
    ], key="actor2")
    header = Hash([
        (g.Sym("characters"), Arr([Arr([u("Actor1"), 0]), Arr([u("Actor1"), 1])])),
        (g.Sym("playtime_s"), a("01:00:00")),
    ])
    contents = Hash([
        (g.Sym("system"), Obj("Game_System", [
            ("@save_disabled", False), ("@save_count", 12), ("@version_id", 1234),
            ("@frames_on_save", 216000), ("@window_tone", UserDef("Tone", g.color(0.0, 0.0, 0.0, 0.0))),
        ])),
        (g.Sym("timer"), Obj("Game_Timer", [("@count", 0), ("@working", False)])),
        (g.Sym("message"), Obj("Game_Message", [("@texts", Arr([])), ("@position", 2)])),
        (g.Sym("switches"), switches()),
        (g.Sym("variables"), variables()),
        (g.Sym("self_switches"), Obj("Game_SelfSwitches", [("@data", Hash([(Arr([1, 3, a("A")]), True)]))])),
        (g.Sym("actors"), Obj("Game_Actors", [("@data", Arr([None, actor1, actor2]))])),
        (g.Sym("party"), Obj("Game_Party", [
            ("@actors", Arr([1, 2])), ("@gold", 1234), ("@steps", 3000),
            ("@items", Hash([(1, 5)])), ("@leader", Ref("actor1")),
        ])),
        (g.Sym("troop"), Obj("Game_Troop", [("@enemies", Arr([]))])),
        (g.Sym("map"), Obj("Game_Map", [
            ("@map_id", 1),
            ("@map", Obj("RPG::Map", [("@width", 4), ("@height", 3),
                                      ("@data", UserDef("Table", g.table(4, 3, 2, list(range(24)))))])),
            ("@display_x", Flt(0.5)),
        ])),
        (g.Sym("player"), Obj("Game_Player", [("@x", 10), ("@y", 8), ("@direction", 2),
                                              ("@character_name", a("Actor1"))])),
        (g.Sym("custom"), Hash([(g.Sym("quest"), u("Find the key"))])),
    ])
    return [header, contents]


write("ace.rvdata2", ace())
