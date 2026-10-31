# SPEC: Save Editor

What the editor does. How it is built step by step is in [IMPLEMENTATION.md](IMPLEMENTATION.md); what is done is
in [IMPLEMENTED.md](IMPLEMENTED.md); how to work in the repository is in [CLAUDE.md](CLAUDE.md).

## 1. Purpose
A desktop editor for the save files of games, offline, in the spirit of saveeditonline.com. It opens a save,
shows the game's variables as a tree, lets the user change numbers, text and switches, and writes the save back
so the game loads it. It is one Go program built with Glutter, cgo-free, Linux first, then Windows and macOS.

## 2. Supported engines and files

Each format has a name, which the app shows and the dialogs use.

| Name in the app | Engine, files | Layers of the file | Package |
|---|---|---|---|
| Ren'Py | Ren'Py 7 and 8, `*.save` | zip: `log` = pickle (protocol 2 or 5), `json`, `extra_info`, `screenshot.png`, `renpy_version`, `signatures` | `internal/pickle`, `internal/renpysave` |
| RPG Maker 2000/2003 | `Save*.lsd` (and EasyRPG Player's) | LCF chunks (BER ids and sizes), strings in the game's code page | `internal/lcf` |
| RPG Maker XP | `*.rxdata` | consecutive Ruby Marshal 4.8 dumps (Ruby 1.8) | `internal/rubymarshal`, `internal/rgss` |
| RPG Maker VX | `*.rvdata` | consecutive Ruby Marshal 4.8 dumps (Ruby 1.8) | `internal/rubymarshal`, `internal/rgss` |
| RPG Maker VX Ace | `*.rvdata2` | header dump, contents dump (Ruby 1.9) | `internal/rubymarshal`, `internal/rgss` |
| RPG Maker MV | `*.rpgsave` | LZString `compressToBase64` of JsonEx JSON | `internal/jsondoc`, `internal/rpgmv` |
| RPG Maker MZ | `*.rmmzsave` | zlib (pako, level 1) of JsonEx JSON, the stream written as UTF-8 text | `internal/jsondoc`, `internal/rpgmv` |
| Unity PlayerPrefs | Linux `prefs` (XML, base64 strings), macOS `unity.*.plist` (binary plist) | as named | `internal/unityprefs` |
| Unity / .NET BinaryFormatter | any name (`.dat`, `.sav`, `.bin`, ...) | MS-NRBF records | `internal/nrbf` |
| Easy Save 3 | `*.es3` (any name when the contents tell) | JSON in ES3's dialect; optionally gzip and/or AES-128-CBC with the default password `password` (key: PBKDF2-HMAC-SHA1, the IV as salt, 100 iterations) | `internal/jsondoc`, `internal/unityjson` |
| Unity JSON | `*.json`, `*.txt`, any name when the contents tell | a JSON object or array | `internal/jsondoc`, `internal/unityjson` |
| Unreal Engine | UE4 and UE5 `*.sav` (`GVAS`) | GVAS header and property tags (classic and 5.4+) | `internal/gvas` |

The registered order is Ren'Py, Unreal Engine, RPG Maker 2000/2003, XP, VX, VX Ace, MV, MZ, Unity PlayerPrefs, Unity / .NET BinaryFormatter, Easy Save 3, Unity JSON.

Out of scope: RPG Maker 95, RPG Maker Unite and console versions; saves a browser keeps (MV and MZ in the browser's storage); Windows PlayerPrefs (the registry is not a file); GVAS wrapped in a game's own compression or encryption; Unity saves in a game's own binary layout or encryption; Easy Save 3 encrypted with a password other than the default one (the app cannot ask for a password), and Easy Save's non-JSON formats; adding or removing variables.

## 3. The model (the contract)
`internal/model/model.go` is the contract between the formats and the app. It is changed only by the manager.
- A format package exports one or more `model.Format` values (`Name`, `Patterns`, `Detect`, `Parse`) and a
  `Document` type that implements `model.Document` (`Variables`, `Details`, `Encode`), and `model.Screenshotter`
  when the save has a picture.
- A value is a `*model.Value` of a `Kind` (null, bool, int, float, string, bytes, list, map, object, opaque) with
  the engine's own type name in `Type`. A value reached from two places is the same pointer.
- A primitive that can be replaced where it is in the file has a `*model.Site`, shared by every place that refers
  to the same stored value. The site gives the bounds of what the file can hold (`IntMin`, `IntMax`,
  `CheckText`); `Value.Float32` marks single precision.
- `model.WriteFile(ctx, path, data, backup)` writes a save safely (temporary file, then rename; the first
  version kept as `<file>.bak`).

## 4. Detection
The app reads the file and asks each registered format in order (`supportedFormats()` in `formats.go`); the
first whose `Detect` says yes parses it. Detection looks at the contents first (magic bytes, structure) and at
the name only when the contents cannot tell. Every fixture of every format is detected by exactly its own
format and by no other, under its own name and under any other name (`fixtures_test.go`). A file no format
knows is refused with "This file is not a save of a supported engine".
- Plain JSON is last: it takes an object or array that parses and is not Easy Save shaped (every key an object
  with a text `__type`). A `.json` or `.txt` name matters only for a file that begins as JSON but has a mistake in
  it, so that the mistake is shown.
- Easy Save 3 takes a file that has a gzip or AES layer around JSON, or plain JSON of its shape. The layers are
  found by their bytes, not by the name. A file of whole 16-byte blocks that is not text and that the default
  password does not open cannot be told from random bytes: it is taken only when its name ends in `.es3`, and
  opening it is refused with "This Easy Save 3 file is encrypted with a password other than the default one".
- Unity / .NET BinaryFormatter is taken by its header (record 0, version 1.0) alone.

## 5. Editing rules
- **Edit in place.** Encode replaces only the bytes of the edited values, then fixes only what depends on them:
  lengths and sizes of enclosing records, counts, offsets, checksums, and a compression, encoding or encryption
  layer around the whole file. Never re-serialize what is not fully modeled.
- Parse then Encode with no edits gives the original bytes (or, where an outer layer is redone, the same inner
  bytes).
- An edit keeps the kind of the old value. Encode refuses an edit whose site is not from the document, whose
  kind differs, or whose value is outside the site's bounds.
- A value that the file stores once and refers to from several places (a Ruby `@` link, a JsonEx `@r`, a
  BinaryFormatter `MemberReference`, a pickle `GET`) is one value and one site: an edit changes it everywhere.
  A binary plist is different: its writer keeps one object for equal numbers and strings only to save space, so
  an edit changes the one place it was made in (a new object is added for it).
- Signatures: a Ren'Py save is re-signed with the user's key, or its signatures are removed when the log changed;
  stale signatures are never kept.
- An Easy Save 3 file is written back with the layers it had (the gzip header kept, a new random IV for the AES
  layer). Whole numbers are held to the range of the type the file declares.
- After Encode the app parses the result with the same format before writing; a result that does not read back
  is not written.

## 6. The app
- Opening: command-line argument, the open button / Ctrl+O. The open dialog has "All supported saves", one
  filter per format, and "All files". Save (Ctrl+S) overwrites, keeping the first version in `.bak`; Save as
  (Ctrl+Shift+S) offers the open document's format.
- Left panel: the screenshot when the document has one (Ren'Py), the file, the format, the document's `Details`;
  for Ren'Py also the save name field and the signature card with the key file.
- Right panel: search by name or value, an "Internals" chip when the document has internal variables, the count
  of changes with Discard, and the tree. Bools are switches; ints, floats and strings are text fields checked
  as the user types against the site's bounds (messages written for the user, e.g. "Enter a whole number from
  0 to 255"); a changed row is highlighted and has an undo button; containers expand (an object with one part reads "(1 part)");
  a value that contains itself is not expanded again.
- The status bar says what happened (opened, saved, why not saved).

## 7. Quality
- Every format has small fixtures made for the tests, with the script that made them, covering every kind it
  can edit. Tests: detection, parse of every fixture, round trip, each editable kind edited and parsed back with
  every other value unchanged, refused edits, every error tag's route, truncated and corrupted input never
  panics.
- Where an independent reader exists and can run here without installing third-party code (python3 standard
  library, the `openssl` program, node with the engine's own scripts given by path), tests check fixtures and
  edited output against it and skip when it is missing. Where none could run, IMPLEMENTED.md says so and gives
  the command a user can run. Not verified against the engine or a real file: MV and MZ (the scripts of the
  engines were not run), Unreal GVAS, RPG Maker 2000/2003, .NET BinaryFormatter (except the example of the
  specification) and Easy Save 3 (its layout is taken from public descriptions); CLAUDE.md lists the switches.
- The Definition of done of CLAUDE.md holds for the whole repository after every step.
