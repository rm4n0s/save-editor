// Runs the real code of RPG Maker MV and MZ on save files: the StorageManager that packs and
// unpacks them, JsonEx, and the lz-string and pako libraries. The engines' files are not in
// this repository; give the paths of a checkout.
//
//   MV:  the corescript of github.com/rpgtkoolmv/corescript (js/rpg_core/JsonEx.js,
//        js/rpg_core/JsExtensions.js, js/rpg_managers/StorageManager.js, js/libs/lz-string.js)
//   MZ:  a directory with rmmz_core.js, rmmz_managers.js and libs/pako.min.js, the files of
//        the js folder of an MZ game (or of github.com/stak/rmmz-corescript)
//
// As a program:
//
//   node engine.js dump <mv|mz> <engine dir> <save file>
//
// reads the file as the game does and prints the object graph it makes as JSON (see dump).
// gen.js uses the functions below to write the fixtures.
const vm = require("vm");
const fs = require("fs");
const nodePath = require("path");

// A fake of the fs module for the sandbox: the files are in memory, and the strings that
// the engines write are encoded as UTF-8, as the real fs does.
function fakeFS(files) {
  return {
    existsSync: (p) => files.has(p) || p === "/game/www/save" || p === "/game/save",
    mkdirSync: () => {},
    rmdirSync: () => {},
    unlinkSync: (p) => files.delete(p),
    renameSync: (a, b) => { files.set(b, files.get(a)); files.delete(a); },
    writeFileSync: (p, data) => files.set(p, Buffer.from(data, "utf8")),
    readFileSync: (p, opts) => {
      const b = files.get(p);
      if (b === undefined) throw new Error("no file " + p);
      return opts && opts.encoding ? b.toString(opts.encoding) : b;
    },
  };
}

function sandbox(files) {
  const ctx = {
    console,
    process: { mainModule: { filename: "/game/www/index.html" } },
    Utils: { isNwjs: () => true },
    setTimeout: () => {},
    require: (name) => (name === "fs" ? fakeFS(files) : require(name)),
  };
  ctx.window = ctx;
  return vm.createContext(ctx);
}

function run(ctx, code, name) {
  vm.runInContext(code, ctx, { filename: name });
}

// The part of a script from the line "function <name>()" to the next line of dashes.
function section(text, name) {
  const start = text.search(new RegExp("^function " + name + "\\(", "m"));
  if (start < 0) throw new Error("no " + name + " in the script");
  const rest = text.slice(start);
  const end = rest.search(/^\/\/-{20}/m);
  return end < 0 ? rest : rest.slice(0, end);
}

// An engine: the sandbox with the real code in it, and the files of its fake disk.
function load(kind, dir) {
  const files = new Map();
  const ctx = sandbox(files);
  const read = (...p) => fs.readFileSync(nodePath.join(dir, ...p), "utf8");
  if (kind === "mv") {
    run(ctx, read("js", "libs", "lz-string.js"), "lz-string.js");
    run(ctx, read("js", "rpg_core", "JsExtensions.js"), "JsExtensions.js");
    run(ctx, read("js", "rpg_core", "JsonEx.js"), "JsonEx.js");
    run(ctx, read("js", "rpg_managers", "StorageManager.js"), "StorageManager.js");
  } else {
    run(ctx, read("libs", "pako.min.js"), "pako.min.js");
    run(ctx, section(read("rmmz_core.js"), "JsonEx"), "rmmz_core.js:JsonEx");
    run(ctx, section(read("rmmz_managers.js"), "StorageManager"), "rmmz_managers.js:StorageManager");
  }
  return { kind, ctx, files, path: savePath(kind, ctx) };
}

// savePath is the path of a save file in the fake disk, as the engine's StorageManager says.
function savePath(kind, ctx) {
  const mv = kind === "mv";
  return vm.runInContext(
    mv ? "(id) => StorageManager.localFilePath(id)" : "(name) => StorageManager.filePath(name)",
    ctx
  );
}

// save writes the contents the way the game does: StorageManager.save(slot, JsonEx.stringify)
// in MV and StorageManager.saveObject(name, contents) in MZ. It returns the bytes of the file.
async function save(engine, slot, contents) {
  const { kind, ctx } = engine;
  ctx.$contents = contents;
  if (kind === "mv") {
    vm.runInContext("StorageManager.save(" + slot + ", JsonEx.stringify($contents))", ctx);
    return engine.files.get(engine.path(slot));
  }
  const name = slot > 0 ? "file" + slot : slot === 0 ? "global" : "config";
  await vm.runInContext("StorageManager.saveObject(" + JSON.stringify(name) + ", $contents)", ctx);
  return engine.files.get(engine.path(name));
}

// loadFile reads a file the way the game does and returns the object graph.
async function loadFile(engine, data) {
  const { kind, ctx } = engine;
  const slot = kind === "mv" ? 1 : "file1";
  engine.files.set(engine.path(slot), Buffer.from(data));
  // Classes the file names that the sandbox does not have: a game has them.
  const text = data.toString("utf8");
  const json = kind === "mv" ? vm.runInContext("LZString.decompressFromBase64($text)", Object.assign(ctx, { $text: text })) : null;
  for (const m of (json === null ? text : json).matchAll(/"@":"([A-Za-z_$][\w$]*)"/g)) {
    if (!(m[1] in ctx)) vm.runInContext("function " + m[1] + "() {}", ctx);
  }
  if (kind === "mv") {
    return vm.runInContext("JsonEx.parse(StorageManager.load(1))", ctx);
  }
  return vm.runInContext('StorageManager.loadObject("file1")', ctx);
}

// dump turns an object graph into plain JSON that the Go tests make from their own tree and
// compare: objects and arrays as they are, the name of the class of an instance under "@",
// and a container met a second time as {"$ref": n} where n counts the containers in the order
// they were first met, depth first, children in the order of the keys.
function dump(value) {
  const seen = new Map();
  const walk = (v) => {
    if (v === null || typeof v !== "object") {
      if (typeof v === "number" && !Number.isFinite(v)) return { $number: String(v) };
      return v === undefined ? null : v;
    }
    if (seen.has(v)) return { $ref: seen.get(v) };
    seen.set(v, seen.size);
    if (Array.isArray(v)) return v.map(walk);
    const out = {};
    const name = v.constructor && v.constructor.name;
    if (name && name !== "Object") out["@"] = name;
    for (const k of Object.keys(v)) out[k] = walk(v[k]);
    return out;
  };
  return walk(value);
}

module.exports = { load, save, loadFile, dump };

if (require.main === module) {
  const [cmd, kind, dir, file] = process.argv.slice(2);
  if (cmd !== "dump") throw new Error("usage: node engine.js dump <mv|mz> <engine dir> <save file>");
  const engine = load(kind, dir);
  Promise.resolve(loadFile(engine, fs.readFileSync(file)))
    .then((graph) => process.stdout.write(JSON.stringify(dump(graph))))
    .catch((e) => { console.error(e); process.exit(1); });
}
