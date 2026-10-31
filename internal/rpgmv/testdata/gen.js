// Makes the fixtures of the tests of internal/rpgmv with the real code of RPG Maker MV and MZ.
//
//   node gen.js <MV corescript dir> <MZ dir> <output dir>
//
// See engine.js for what the two directories are. Each fixture is a file as the game writes
// it, with the JSON text it holds next to it (the file name and ".json"), made by the same
// JsonEx. Run it from internal/rpgmv/testdata.
const fs = require("fs");
const nodePath = require("path");
const vm = require("vm");
const engine = require("./engine.js");

const [mvDir, mzDir, outDir] = process.argv.slice(2);

// The game's classes, in the engine's sandbox, and the contents of a save made of them.
const GAME = `
function Game_System() { this._saveEnabled = true; this._saveCount = 12; this._versionId = 4711;
  this._framesOnSave = 216000 + 60 * 61 + 5; this._bgmOnSave = null; this._windowTone = null;
  this._battleCount = 3; this._winCount = 2; this._escapeCount = 0; }
function Game_Screen() { this._brightness = 255; this._zoomScale = 1.5; this._pictures = [null, null]; }
function Game_Timer() { this._frames = 0; this._working = false; }
function Game_Switches() { this._data = [null, true, false, true, null, false]; }
function Game_Variables() { this._data = [null, 100, -5, 0.25, "text", null, 9007199254740991]; }
function Game_SelfSwitches() { this._data = { "1,2,A": true, "1,3,B": false }; }
function Game_Item() { this._dataClass = "weapon"; this._itemId = 7; }
function Game_Actor(id) { this._actorId = id; this._name = id === 1 ? "Ünï 😀 ジュリア" : "Bob";
  this._level = 10 * id; this._hp = 123 + id; this._mp = 0; this._exp = { 1: 100, 2: 4000 }; this._skills = [1, 2, 3];
  this._equips = [new Game_Item(), new Game_Item(), null]; this._profile = 'He said "hi"\\n\\\\o/'; this._ratio = 0.5; }
function Game_Actors() { this._data = [null]; }
function Game_Party() { this._gold = 1234; this._steps = 56789; this._lastItem = { _itemId: 1 };
  this._menuActorId = 0; this._targetActorId = 0; this._actors = [1, 2]; this._items = { 1: 5, 7: 1 };
  this._weapons = {}; this._armors = { 3: 2 }; }
function Game_Map() { this._interpreter = null; this._mapId = 3; this._tilesetId = 1; this._events = []; this._displayX = 1.25; this._displayY = 0; }
function Game_Player() { this._x = 12; this._y = 8; this._realX = 12; this._realY = 8; this._direction = 2; this._moveSpeed = 4; this._transparent = false; }
function Plugin_Quest() { this.name = "Dragon — é"; this.done = [true, false, [1, [2, 3]]]; this.empty = []; this.none = {}; }
`;

function contents(ctx, kind) {
  const code = `(function (kind) {
    var actors = new Game_Actors();
    actors._data[1] = new Game_Actor(1);
    actors._data[2] = new Game_Actor(2);
    var party = new Game_Party();
    var c = {};
    c.system = new Game_System();
    c.screen = new Game_Screen();
    c.timer = new Game_Timer();
    c.switches = new Game_Switches();
    c.variables = new Game_Variables();
    c.selfSwitches = new Game_SelfSwitches();
    c.actors = actors;
    c.party = party;
    c.map = new Game_Map();
    c.player = new Game_Player();
    c.quest = new Plugin_Quest();
    // MV writes a second reference to an object as {"@r": id}, and handles a circle.
    // MZ has no references: it writes the object again, and a circle is an error.
    var item = new Game_Item();
    actors._data[1]._equips[2] = item;
    actors._data[2]._equips[2] = item;
    actors._data[2]._friends = actors._data[1]._skills;
    if (kind === "mv") {
      actors._data[1]._party = party;
      party._leaderActor = actors._data[1];
      c.quest.self = c.quest;
    }
    return c;
  })(${JSON.stringify(kind)})`;
  return vm.runInContext(code, ctx);
}

async function write(kind, dir, ext) {
  const e = engine.load(kind, dir);
  vm.runInContext(GAME, e.ctx);
  const outputs = {};
  for (const slot of [1, 0, -1]) {
    let c;
    if (slot === 1) c = contents(e.ctx, kind);
    else if (slot === 0) c = vm.runInContext(`[null, { title: "Dragon — é", characters: [["Actor1", 0], ["Actor2", 3]], faces: [["Actor1", 0]], playtime: "00:12:34", timestamp: 1700000000000 }]`, e.ctx);
    else c = vm.runInContext(`({ alwaysDash: false, commandRemember: true, bgmVolume: 80, bgsVolume: 90, meVolume: 100, seVolume: 70 })`, e.ctx);
    const name = slot > 0 ? "file" + slot : slot === 0 ? "global" : "config";
    const bytes = await engine.save(e, slot, c);
    fs.writeFileSync(nodePath.join(outDir, `${kind}-${name}.${ext}`), bytes);
    // The JSON text that was packed, from the same JsonEx.
    e.ctx.$c = c;
    const json = vm.runInContext("JsonEx.stringify($c)", e.ctx);
    fs.writeFileSync(nodePath.join(outDir, `${kind}-${name}.${ext}.json`), json);
    outputs[name] = bytes.length;
  }
  console.log(kind, outputs);
}

(async () => {
  fs.mkdirSync(outDir, { recursive: true });
  await write("mv", mvDir, "rpgsave");
  await write("mz", mzDir, "rmmzsave");
})().catch((e) => { console.error(e); process.exit(1); });
