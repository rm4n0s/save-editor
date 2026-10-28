"""Stand-ins for the Ren'Py classes that a save's pickle names.

They are registered under Ren'Py's module names, so a pickle made with them names the same
classes as one Ren'Py makes, and pickles and unpickles the same way (RevertableSet keeps its
items in a dict state, like Ren'Py's).
"""

import sys
import types


def _module(name):
    m = types.ModuleType(name)
    sys.modules[name] = m
    parent, _, child = name.rpartition(".")
    if parent:
        setattr(sys.modules[parent], child, m)
    return m


renpy = _module("renpy")
revertable = _module("renpy.revertable")
rollback = _module("renpy.rollback")
character = _module("renpy.character")
store = _module("store")


def _register(module, cls):
    cls.__module__ = module.__name__
    setattr(module, cls.__name__, cls)
    return cls


@lambda c: _register(revertable, c)
class RevertableList(list):
    pass


@lambda c: _register(revertable, c)
class RevertableDict(dict):
    pass


@lambda c: _register(revertable, c)
class RevertableSet(set):
    def __setstate__(self, state):
        if isinstance(state, tuple):
            self.update(state[0].keys())
        else:
            self.update(state)

    def __getstate__(self):
        return ({i: True for i in self},)

    __reduce__ = object.__reduce__
    __reduce_ex__ = object.__reduce_ex__


@lambda c: _register(revertable, c)
class RevertableObject(object):
    def __eq__(self, other):
        return type(self) is type(other) and self.__dict__ == other.__dict__


@lambda c: _register(rollback, c)
class Rollback(object):
    def __init__(self, stores=None):
        self.stores = stores or {}


@lambda c: _register(rollback, c)
class RollbackLog(object):
    def __init__(self, log=None):
        self.log = log or []


@lambda c: _register(character, c)
class ADVCharacter(object):
    def __init__(self, name):
        self.name = name
        self.what_args = {}


@lambda c: _register(store, c)
class Hero(RevertableObject):
    def __init__(self, name, hp):
        self.name = name
        self.hp = hp


def sample(large=False):
    """The (roots, log) tuple that a save pickles, with the shapes Ren'Py's has."""
    name = "Ann" * 2  # one str object, reached from several places through the memo
    roots = {
        "store.money": 150,
        "store.big": 2**70,
        "store.neg": -129,
        "store.ratio": 0.75,
        "store.player_name": name,
        "store.nickname": name,
        "store.has_key": True,
        "store.flag_off": False,
        "store.nothing": None,
        "store.greeting": "Καλημέρα ☕",
        "store.inventory": RevertableList(["sword", "potion", name]),
        "store.stats": RevertableDict({"str": 10, "dex": 12.5}),
        "store.seen": RevertableSet({"intro"}),
        "store.hero": Hero(name, 30),
        "store.pair": (1, "a"),
        "store.e": ADVCharacter("Eileen"),
        "store._internal": 1,
    }
    if large:
        roots["store.long_text"] = "x" * 70000
    log = RollbackLog([Rollback({"store": {"money": 100, "player_name": name}})])
    return roots, log
