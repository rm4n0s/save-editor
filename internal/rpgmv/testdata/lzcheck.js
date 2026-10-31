// Runs the real lz-string on text given by the Go tests (lzstring_test.go).
//
//   node lzcheck.js <path to lz-string.js>   (the input is JSON on stdin)
//
// The input is a list of lists of UTF-16 code units. For each, the output has the
// compressToBase64 of the text and the code units that decompressFromBase64 gives back
// for the base64 text in "go" (what the Go code made).
const vm = require("vm");
const fs = require("fs");

const ctx = {};
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[2], "utf8"), ctx);
const LZ = ctx.LZString;

const cases = JSON.parse(fs.readFileSync(0, "utf8"));
const out = cases.map((c) => {
  let s = "";
  for (let i = 0; i < c.units.length; i += 4096) s += String.fromCharCode(...c.units.slice(i, i + 4096));
  const back = LZ.decompressFromBase64(c.go);
  const units = [];
  for (let i = 0; back !== null && i < back.length; i++) units.push(back.charCodeAt(i));
  return { compressed: LZ.compressToBase64(s), goBack: back === null ? null : units };
});
process.stdout.write(JSON.stringify(out));
