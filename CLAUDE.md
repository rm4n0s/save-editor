# CLAUDE.md

Guidance for Claude Code when working in this repository (Go, hosted on GitHub at `github.com/rm4n0s/save-editor`).

## Project
A desktop editor for the save files of games, in the spirit of saveeditonline.com but offline. It opens a save, lists the variables of the game as a tree, lets the user change numbers, text and switches, and writes the save back so the game loads it. It opens Ren'Py, RPG Maker 2000/2003, XP, VX, VX Ace, MV and MZ, Unreal Engine (GVAS), Unity PlayerPrefs, Unity .NET BinaryFormatter, Unity JSON and Easy Save 3 saves (SPEC.md says what each is). A Ren'Py save is signed with the user's Ren'Py key so the game loads it without a prompt. The UI is built with [Glutter](https://github.com/rm4n0s/glutter), a cgo-free Go toolkit with Flutter's framework API (widgets, Material 3) on its own engine. Linux desktop comes first; Windows and macOS follow from Glutter.

## Working with agents
- Opus 5.5 at high effort is the manager: it plans the work, splits it into tasks, writes the contracts between them (shared types, interfaces, file ownership), reviews what comes back and runs the Definition of done. It does not do the tasks itself
- Every task goes to a Sonnet 5.5 subagent at high effort (Agent tool with `model: "sonnet"` and `effort: "high"`). The brief names the goal, the files and packages the agent owns, the checks it must run, and what is out of scope
- Work goes through three files at the root. The manager writes `SPEC.md` (what the app does: engines, files, the model contract, editing rules, UI, quality) and `IMPLEMENTATION.md` (the plan as numbered steps, each with its goal, files, tests and screenshot). It keeps both true when plans change
- One Sonnet subagent implements the steps of `IMPLEMENTATION.md` in order, one at a time. After each step it appends an entry to `IMPLEMENTED.md` (what was done, what is and is not supported, the checks and their results, what was verified against an independent reference, a screenshot, open questions), then stops. The manager reviews the step's diff and entry and tells the same agent (SendMessage) to fix the step or start the next one
- Every step that changes what the app can open or show ends with a screenshot of the real app on the step's fixture, taken headless as described under Commands, saved as `docs/screenshots/step-NN-<what>.png` and linked from the entry. When a screenshot is not feasible, the entry says why
- The manager reads the diff of every step before accepting it and sends it back when it breaks a rule of this file. Commits are made only when the user asks

## Commands
- Build: `go build ./...`. The app is cgo-free; `CGO_ENABLED=0 go build ./...` must always work
- Run: `go run . [file]`
- Test all: `go test -race ./...` (the race detector needs cgo, so this one needs a C compiler)
- Test the cgo-free path: `CGO_ENABLED=0 go test ./...`
- Test one package: `go test ./internal/pickle -run TestName -v`
- Update golden images: `go test . -run Golden -update-goldens`, then look at every changed PNG. `-run TestGolden` is the Ren'Py one, `-run TestGoldenOfAnotherEngine` the RPG Maker MV one
- Vet: `go vet ./...`
- Lint: `golangci-lint run`
- Format: `gofmt -w .`
- After changing dependencies: `go mod tidy`
- A proof screenshot of the real app, headless: run the binary on a private `Xvfb` with `GLUTTER_BACKEND=x11 GLUTTER_SCREENSHOT=out.png GLUTTER_SCREENSHOT_AFTER=2s` and `HOME` set to a scratch folder (headless weston has no `wl_seat`, which Glutter's Wayland backend needs). Under `setsid -w timeout -k 2 30`, so that the shell waits for it

### Fixture generators
Every fixture is small, made for the tests, and comes with the script that made it. The scripts use only the Python standard library (and, for Easy Save, the `openssl` program); run each from the module root:
- Pickles: `python3 -I internal/pickle/testdata/gen.py`
- Ren'Py saves: `python3 -I internal/renpysave/testdata/gen.py <path to renpy/renpy/ecsign.py>` (Ren'Py's own signing code from a Ren'Py checkout; it is not part of this repository)
- Ruby Marshal dumps: `python3 -I internal/rubymarshal/testdata/gen.py` (the assembler is `rmgen.py`)
- RPG Maker XP, VX, VX Ace: `python3 -I internal/rgss/testdata/gen.py`
- RPG Maker 2000/2003: `python3 -I internal/lcf/testdata/gen.py` (and `... gen.py dump FILE ENCODING`, a second reader that the tests use)
- RPG Maker MV and MZ: `cd internal/rpgmv/testdata && node gen.js <MV corescript dir> <MZ dir> .` (it runs the engines' own scripts, which are not in this repository; the fixtures are in it, so this is only needed to make them again)
- Unreal Engine: `python3 -I internal/gvas/testdata/gen.py`
- Unity PlayerPrefs: `python3 -I internal/unityprefs/testdata/gen.py`
- .NET BinaryFormatter: `python3 -I internal/nrbf/testdata/gen.py`
- Unity JSON and Easy Save 3: `python3 -I internal/unityjson/testdata/gen.py` (needs the `openssl` program for the AES step)

### Reference checks
Where an independent reader exists, the tests run it and skip when it is missing. These are the switches (a test that skips says which one it wants):
- `python3` on the path: the pickle tests (`internal/pickle/testdata/load.py`), the PlayerPrefs tests (`internal/unityprefs/testdata/load.py`, with `plistlib`) and the LCF tests (`gen.py dump`)
- `RUBYMARSHAL_PYTHON=<a python that has the package rubymarshal>`: `internal/rgss` and `internal/rubymarshal` read the fixtures and the edited files with it. A venv with `pip install rubymarshal` is enough; nothing of the repository needs it otherwise
- `RPGMV_CORESCRIPT=<checkout of github.com/rpgtkoolmv/corescript>`, `RPGMZ_CORESCRIPT=<folder with rmmz_core.js, rmmz_managers.js and libs/pako.min.js>` and, optionally, `RPGMV_LZSTRING=<path list of lz-string.js files of version 1.4 or later>`, with `node` on the path: `go test -count=1 -v ./internal/rpgmv -run 'Engine|JavaScript|TestEdits'` runs the engines' own StorageManager, JsonEx and lz-string and pako on the fixtures and on the edited files (`testdata/engine.js`, `lzcheck.js`). The scripts are third-party code: do not fetch or run them without the user's say-so
- `openssl` on the path: `internal/unityjson` decrypts what the editor writes with OpenSSL's `kdf` and `enc`

## Definition of done
Before reporting a task as finished, run `gofmt`, `go vet ./...`, `golangci-lint run` and `go test -race ./...`, and fix anything they report. Say so explicitly if any step was skipped or failed. Also:
- `CGO_ENABLED=0 go build ./...` and `CGO_ENABLED=0 go test ./...` pass
- Every new exported identifier has a doc comment
- Every changed golden image has been looked at, not just regenerated
- A change to how saves are read or written is checked against the independent reader of its format where there is one (see Reference checks): the pickle tests run `load.py`, the RGSS and Marshal tests the Python package `rubymarshal`, the PlayerPrefs tests `plistlib`, the Easy Save tests `openssl`; a change to signing is checked with Ren'Py's `ecsign.py`

## Layout
- `main.go`: `main`, and where Ren'Py keeps its saves and keys on each platform
- `app.go`: the widgets (`EditorApp`, `EditorPage` and its state)
- `tree.go`: what the widgets show, without widgets: the rows of the variable tree and the parsing of typed values
- `formats.go`: `supportedFormats()` (the formats, in the order detection tries them), opening a file with the first format that detects it, and the filters of the file dialogs
- `internal/model/`: the contract between the formats and the app (`Format`, `Document`, `Value`, `Site`, `Edit`) and `WriteFile`. Changed only by the manager
- Format packages, each with `Format` (or `Formats`) and a `Document`:
  - `internal/pickle/`: a reader of Python pickles (protocols 0 to 5) into a tree of values, and `Document.Patch`, which writes new primitive values into the original bytes
  - `internal/renpysave/`: the Ren'Py save (a zip: `screenshot.png`, `extra_info`, `json`, `renpy_version`, `log`, `signatures`), its variables, `Encode`, `WriteFile`, and the keys and signatures of `security_keys.txt`
  - `internal/lcf/`: RPG Maker 2000/2003 `Save*.lsd` (the schema table is derived from EasyRPG's liblcf, credited in `schema.go`)
  - `internal/rubymarshal/`: Ruby Marshal 4.8 dumps; `internal/rgss/`: the XP, VX and VX Ace saves on top of it
  - `internal/jsondoc/`: JSON read into model values and patched byte for byte; `internal/rpgmv/`: RPG Maker MV (LZString) and MZ (zlib) on top of it
  - `internal/gvas/`: Unreal Engine `GVAS` saves
  - `internal/unityprefs/`: Unity PlayerPrefs (Linux XML, macOS binary plist)
  - `internal/nrbf/`: .NET BinaryFormatter streams (MS-NRBF)
  - `internal/unityjson/`: plain JSON saves and Easy Save 3 (gzip, AES) on top of `jsondoc`
- `testdata/goldens/`: golden images of the app; `internal/*/testdata/`: fixtures and the Python scripts that make and check them; `docs/screenshots/`: the proof screenshots of the steps
- Tests live next to the code (`foo_test.go`). At the root: `fixtures_test.go` (every fixture of the repository is detected by exactly one format), `registered_test.go` and `allformats_test.go` (every format opens, edits, saves and "saves as" in the app), `generic_test.go` (the app on a made-up format)

## How saves are edited (read before changing `internal/`)
Every format edits **in place**: it replaces the bytes of the edited values and fixes only what depends on them (a length, a size, a count, an offset, a compression or encryption layer around the file). Nothing that is not fully modeled is ever written again. `Encode` with no edits returns the original bytes. An edit keeps the kind of the old value, and a value outside the bounds of its site (the whole numbers a type can hold, `CheckText`) is refused. The reference of each format is named; read it from its source or specification, never vendor it.

### Ren'Py (`internal/pickle`, `internal/renpysave`)
- The `log` entry is `pickle.dumps((roots, rollback_log))`. `roots` is a dict from `"store.name"` (or `"store.sub.name"`) to the value of each variable. Ren'Py 8 pickles with `pickle.HIGHEST_PROTOCOL` (5, with FRAME opcodes); Ren'Py 7 runs on Python 2 and uses protocol 2
- **Never re-pickle.** The log is full of Ren'Py and game classes that this app does not have. An edit replaces only the opcode that pushed the value (`pickle.Site`); every other byte is kept. A memoized literal is kept and popped (`... MEMOIZE POP <new>`), so later `GET`s of the old value are unchanged. Replacements use opcodes of protocol 2 or lower so a Ren'Py 7 save stays readable by Python 2. FRAME opcodes are dropped when anything changes (frames are optional)
- On load Ren'Py assigns each root, then rolls back to the last checkpoint (`RollbackLog.unfreeze_core`), so a variable the game changed during the statement it was saved on can come back from the rollback log
- Signatures (Ren'Py 7.6 / 8.1 and later): `signature <base64 SPKI DER> <base64 r||s>` lines, ECDSA P-256 over the SHA-1 of the log. `security_keys.txt` has `signing-key <SEC1 DER> <SPKI DER>` and `verifying-key <SPKI DER>` lines. A save whose signatures do not match its log is refused by Ren'Py after a prompt (`savetoken.check_load`), so `Encode` re-signs with the user's key or removes the signatures of a changed log; never keep stale signatures
- Reference: Ren'Py's own sources (`renpy/loadsave.py`, `renpy/savetoken.py`, `renpy/ecsign.py`, `renpy/rollback.py`, `renpy/compat/pickle.py`), from a Ren'Py checkout or GitHub; Python's `pickle`

### RPG Maker 2000/2003 (`internal/lcf`)
- A save is EasyRPG's LCF: a header, then chunks (BER numbers for ids and sizes) holding structs, vectors of structs and plain values; text is in the game's code page (found from the file; shown as "Text encoding"). The ids, names and types of the chunks come from the schema table in `schema.go`, derived from EasyRPG's liblcf with credit. A chunk the table does not know is kept byte for byte and shown as bytes
- An edit replaces the data of one chunk (or an element of a vector, whose elements have a fixed size) and the sizes of the chunks around it
- Reference: EasyRPG's liblcf (`src/reader_lcf.cpp`, `src/reader_struct_impl.h`). Not checked by EasyRPG's own tools

### RPG Maker XP, VX, VX Ace (`internal/rubymarshal`, `internal/rgss`)
- A save is one or more Ruby Marshal 4.8 dumps (XP and VX: one per game object, in the order of the default Scene_Save / Scene_File scripts; VX Ace: the header and the contents). `rubymarshal` numbers objects as Ruby does, so a value the file shares (an `@` link) is one `*model.Value` with one site
- An edit splices the new bytes where the old ones were: a Fixnum stays an `i` (a Bignum an `l`, a Float an `f`), a String keeps its instance variables and changes only its length and bytes, so the numbering of the objects never moves. A dump a game script appended is shown as "extra N"
- Reference: Ruby's `marshal.c` and `doc/marshal.rdoc`; the Python package `rubymarshal` (checks, via `RUBYMARSHAL_PYTHON`)

### RPG Maker MV and MZ (`internal/jsondoc`, `internal/rpgmv`)
- The file is `JsonEx.stringify(contents)` packed by the engine. MV: `LZString.compressToBase64` (the layout of the library the game ships; a file of lz-string 1.4 or later differs only in how the last bits are padded and is written back in its own layout, found by encoding the text again). MZ: `pako.deflate(json, {to: "string", level: 1})`, a string with one character per byte of the zlib stream, which `fs.writeFileSync` writes as UTF-8 text (a byte of 0x80 or more takes two bytes)
- `jsondoc` replaces only the bytes of the edited tokens; then the text is packed again. MV's JsonEx bookkeeping (`@c` ids, `@a` array wrappers, `@r` references) stays in the file and is hidden; an object met again is the same value and site. Whole numbers are held to the JavaScript safe range
- Reference: the engines' `rpg_core.js` JsonEx, `rpg_managers.js` and `rmmz_managers.js` StorageManager, lz-string and pako (checks, via `RPGMV_CORESCRIPT`, `RPGMZ_CORESCRIPT`, `RPGMV_LZSTRING`)

### Unreal Engine (`internal/gvas`)
- A `GVAS` file: a header and property tags (the classic layout and the one of Unreal 5.4 and later, with type names). An edit replaces the bytes of the value and fixes the size fields of the properties that contain it; a value the package cannot decode is kept as bytes. A save a game wraps in its own compression or encryption does not start with `GVAS` and is not read
- Reference: Unreal's `FGameSaveHeader` and `FPropertyTag` as the open readers (uesave) document them. Not checked by any tool that did not come from the same reading

### Unity PlayerPrefs (`internal/unityprefs`)
- Linux: an XML file (`<pref name type>`; an int, a 32-bit float, or a string as base64 of UTF-8); an edit changes only the text of the element. macOS: a binary property list (`bplist00`) whose top object is a dictionary; an edit replaces the object, and the offset table is written again. A writer keeps one object for equal numbers and strings, so an edit of a place whose object other places use adds a new object and points that place at it (the others keep the old value); if more objects need more bytes per reference, every container is written again with larger references. Unity's own keys are Internal
- Reference: Python's `plistlib` and `xml.etree` (`testdata/load.py`); Unity's documentation and public samples for the layout

### .NET BinaryFormatter (`internal/nrbf`)
- An MS-NRBF stream: records with object ids; a string or class reached from several places (`MemberReference`) is one value and one site. An edit replaces a primitive or a string where it is (a string's length prefix is written again); nothing else in the stream stores an offset or a size. `List`, `Dictionary`, `Hashtable`, `HashSet` are shown as their items. The reader bounds nesting and the total number of items, since a run of nulls is a few bytes for many members
- Reference: the specification MS-NRBF (learn.microsoft.com/openspecs/windows_protocols/ms-nrbf); `spec-address.bin` is the example of its section 3

### Unity JSON and Easy Save 3 (`internal/jsondoc`, `internal/unityjson`)
- Plain JSON is patched like MV's text. An Easy Save 3 file is JSON (`{"key": {"__type": T, "value": V}}`), optionally gzip and optionally `IV || AES-128-CBC(PKCS7)` with `key = PBKDF2-HMAC-SHA1(password, salt = IV, 100 iterations, 16 bytes)`. The layers are found by looking at the bytes (the IV is random and may look like text, so a file of whole blocks is JSON or gzip only when it parses or inflates) and are written again in the order found: the gzip header is kept, the IV is new each time. Only the default password `password` is tried; another password is refused with a message
- Reference: the public descriptions in the package comment (Easy Save is closed source; the layout is unverified against it); `openssl` and Python's `hashlib` and `gzip` (checks)

## Adding a format
1. A package `internal/<name>` that exports `Format` (`model.Format`: `Name`, `Patterns`, `Detect`, `Parse`) and a `Document` that implements `model.Document` (`Variables`, `Details`, `Encode`), and `model.Screenshotter` when the save has a picture. `Detect` looks at the contents and at the name only when the contents cannot tell; it never panics and never takes a file of another format. Primitives that can be replaced where they are get a `*model.Site` (shared by every place that holds the same stored value), with the bounds of what the file can hold. Edit in place, as above
2. Fixtures made for the tests, with the script that made them in `testdata/gen.py` (standard library only); cover every kind the format can edit. Tests, table-driven: detection, parse of every fixture, `Encode(nil)` equal to the input, each editable kind edited and parsed back with every other value unchanged (and the bytes outside the value unchanged), refused edits, a test for the route of every error tag, and truncated and changed-byte input never panicking. Add an independent reader to the tests when one can run here without installing anything, gated by an environment variable or a program on the path, and say in `IMPLEMENTED.md` when none could run
3. Register it in `supportedFormats()` in `formats.go`, after the formats it must not take files from (plain JSON is last). Add every fixture to the table of `fixtures_test.go` and the saves to `registeredFixtures` in `registered_test.go`; add the fixture generator to Commands and the format to "How saves are edited" here, and to SPEC.md and README.md
4. Run the Definition of done, look at a screenshot of the real app on a fixture, and look at any golden that changed

## Go conventions
- Never ignore a returned error; handle it or return it
- Pass `context.Context` as the first parameter of anything that does I/O (files, dialogs). Name it `ctx`. Name a `BuildContext` `bc`
- Tests are table-driven
- No global mutable state. Package-level read-only values are fine
- Prefer the standard library. Allowed dependencies: `github.com/rm4n0s/glutter` and `github.com/rm4n0s/errors` (and what they pull in). Ask before adding any other
- **No cgo**: never write `import "C"`
- No `runtime.GOOS` checks. Platform-dependent behavior goes through `foundation.DefaultTargetPlatform()`
- Only the UI thread touches the framework. Other goroutines hand work over through `SetState` or the binding's `Post`. `State.Go` is called on the UI thread only: a goroutine that needs more work calls the goroutine body directly (`openIn`, `loadKeysIn`), not `open`
- Desktop dialogs (`desktop.OpenFiles`, `desktop.SaveFile`) block: call them from a `State.Go` goroutine

## Glutter API conventions (summary of Glutter's SPEC §12 and docs/GUIDE.md)
- Apps import one package: `m "github.com/rm4n0s/glutter/material"`
- A widget is made by `NewX(required..., opts ...XOpts)`; `child`/`children` come last. Pass at most one options value: more than one panics
- A nullable Flutter parameter is `m.Some(v)` / `m.None[T]()`; an enum's zero value is `XUnspecified`, the default
- `Theme.of` is `m.ThemeOf(bc)`, `Navigator.pop` is `m.NavigatorPop(bc)`; colors of a `ColorScheme` are methods (`scheme.Primary()`)
- A controller made in `InitState` is disposed in `Dispose`. A controller that leaves the tree in a rebuild is disposed after that frame (`Scheduler().AddPostFrameCallback`), never while its field is still mounted
- A `ListView.builder` whose rows can move (expand, filter) has keyed rows and a `FindChildIndexCallback`, so rows keep their state
- Keep the shape of the tree above a text field the same between builds: a `Container` that gains a `Color` (or a decoration, padding, alignment) wraps its child in a new widget, the field is made again, and the focus jumps away. Give it a transparent color instead (`TestTypingKeepsTheFocus`)

## Error handling
Use `github.com/rm4n0s/errors` (v0.2.0) for all errors, the way its `AI_GUIDE.md` says. Read the whole guide (`$(go env GOMODCACHE)/github.com/rm4n0s/errors@v0.2.0/AI_GUIDE.md`) before you write or change code that makes, returns, logs or tests an error. Never use `fmt.Errorf`, and never import the standard library `errors` package under the name `errors`.
- New failure: `errors.New("Tag", "message", "key", value, ...)`, all metadata in that one call. Formatted: `errors.NewErrf`
- An error from outside this module (standard library, Glutter, Python data): `errors.NewErr("Tag", err)`, once, where it first appears; at most one `.SetMetadata(...)`
- An error from this module is returned untouched: `return err`
- Call the constructor in the function that fails, never in a helper that builds it. Avoid range-over-func iterators (`strings.Lines`) around code that fails: their closures appear in the route
- Tags are constants at the end of the file that makes them (`const tagX = "X"`), exported only when another package matches on them. Branch on the tag or `errors.Is`, never on a message
- Functions return the plain `error` interface
- Log with `slog` and pass the error as `"err"`
- Every Tag has a test that asserts its route with `HasRoute("Outer->Inner.Tag")`
- Messages of the errors of `tree.go` are shown to the user under a field: write them for the user ("Enter a whole number")

## Git and GitHub
- Never commit or push directly to `main`; work on a branch named `feat/...` or `fix/...`
- Commit messages: imperative mood, e.g. "Add retry to client"
- PRs: short description, link the issue (`Closes #123`), keep them small
- No `.github/` directory and no GitHub Actions workflows: the Definition-of-done checks are run locally

## Don't
- Don't read, list or write the user's real game or save folders (`~/.renpy`, `%APPDATA%\RenPy`, `~/Library/RenPy`, `~/.config/unity3d`, `~/Library/Preferences`, a game's `save` folder, ...) in tests or proofs: tests use the fixtures, and a proof run sets `HOME` to a scratch folder. If a real save is needed, ask the user for a copy
- Don't commit secrets, `.env` files or real users' keys (the keys in `internal/renpysave/testdata` were made for the tests)
- Don't leave processes running: every Xvfb, compositor or app started for a test or a proof runs under `timeout -k`, in its own process group, and `pgrep -a` shows nothing left before a task is reported done
- Don't write a save without a way back: `WriteFile` keeps the first version in `<file>.bak`
