# CLAUDE.md

Guidance for Claude Code when working in this repository (Go, hosted on GitHub at `github.com/rm4n0s/save-editor`).

## Project
A desktop editor for Ren'Py save files, in the spirit of saveeditonline.com but for Ren'Py only and offline. It opens a `.save`, lists the variables of the game's stores as a tree, lets the user change numbers, text and switches, and writes the save back, signed with the user's Ren'Py key so the game loads it without a prompt. The UI is built with [Glutter](https://github.com/rm4n0s/glutter), a cgo-free Go toolkit with Flutter's framework API (widgets, Material 3) on its own engine. Linux desktop comes first; Windows and macOS follow from Glutter.

## Commands
- Build: `go build ./...`. The app is cgo-free; `CGO_ENABLED=0 go build ./...` must always work
- Run: `go run . [file.save]`
- Test all: `go test -race ./...` (the race detector needs cgo, so this one needs a C compiler)
- Test the cgo-free path: `CGO_ENABLED=0 go test ./...`
- Test one package: `go test ./internal/pickle -run TestName -v`
- Update golden images: `go test . -run Golden -update-goldens`, then look at every changed PNG
- Vet: `go vet ./...`
- Lint: `golangci-lint run`
- Format: `gofmt -w .`
- Regenerate the pickle fixtures: `python3 -I internal/pickle/testdata/gen.py`
- Regenerate the save fixtures: `python3 -I internal/renpysave/testdata/gen.py <path to renpy/renpy/ecsign.py>` (Ren'Py's own signing code from a Ren'Py checkout; it is not part of this repository)
- A proof screenshot of the real app, headless: run the binary on a private `Xvfb` with `GLUTTER_BACKEND=x11 GLUTTER_SCREENSHOT=out.png GLUTTER_SCREENSHOT_AFTER=2s` and `HOME` set to a scratch folder (headless weston has no `wl_seat`, which Glutter's Wayland backend needs)
- After changing dependencies: `go mod tidy`

## Definition of done
Before reporting a task as finished, run `gofmt`, `go vet ./...`, `golangci-lint run` and `go test -race ./...`, and fix anything they report. Say so explicitly if any step was skipped or failed. Also:
- `CGO_ENABLED=0 go build ./...` and `CGO_ENABLED=0 go test ./...` pass
- Every new exported identifier has a doc comment
- Every changed golden image has been looked at, not just regenerated
- A change to how saves are read or written is checked against Python: the pickle tests run `internal/pickle/testdata/load.py` when `python3` is installed; a change to signing is checked with Ren'Py's `ecsign.py`

## Layout
- `main.go`: `main`, and where Ren'Py keeps its saves and keys on each platform
- `app.go`: the widgets (`EditorApp`, `EditorPage` and its state)
- `tree.go`: what the widgets show, without widgets: the rows of the variable tree and the parsing of typed values
- `internal/pickle/`: a reader of Python pickles (protocols 0 to 5) into a tree of values, and `Document.Patch`, which writes new primitive values into the original bytes
- `internal/renpysave/`: the save file (a zip: `screenshot.png`, `extra_info`, `json`, `renpy_version`, `log`, `signatures`), its variables, `Encode`, `WriteFile`, and the keys and signatures of `security_keys.txt`
- `testdata/goldens/`: golden images of the app; `internal/*/testdata/`: fixtures and the Python scripts that make and check them
- Tests live next to the code (`foo_test.go`)

## How saves are edited (read before changing `internal/`)
- The `log` entry is `pickle.dumps((roots, rollback_log))`. `roots` is a dict from `"store.name"` (or `"store.sub.name"`) to the value of each variable. Ren'Py 8 pickles with `pickle.HIGHEST_PROTOCOL` (5, with FRAME opcodes); Ren'Py 7 runs on Python 2 and uses protocol 2
- **Never re-pickle.** The log is full of Ren'Py and game classes that this app does not have. An edit replaces only the opcode that pushed the value (`pickle.Site`); every other byte is kept. A memoized literal is kept and popped (`... MEMOIZE POP <new>`), so later `GET`s of the old value are unchanged. Replacements use opcodes of protocol 2 or lower so a Ren'Py 7 save stays readable by Python 2. FRAME opcodes are dropped when anything changes (frames are optional)
- On load Ren'Py assigns each root, then rolls back to the last checkpoint (`RollbackLog.unfreeze_core`), so a variable the game changed during the statement it was saved on can come back from the rollback log
- Signatures (Ren'Py 7.6 / 8.1 and later): `signature <base64 SPKI DER> <base64 r||s>` lines, ECDSA P-256 over the SHA-1 of the log. `security_keys.txt` has `signing-key <SEC1 DER> <SPKI DER>` and `verifying-key <SPKI DER>` lines. A save whose signatures do not match its log is refused by Ren'Py after a prompt (`savetoken.check_load`), so `Encode` re-signs with the user's key or removes the signatures of a changed log; never keep stale signatures
- Ren'Py's own sources (`renpy/loadsave.py`, `renpy/savetoken.py`, `renpy/ecsign.py`, `renpy/rollback.py`, `renpy/compat/pickle.py`) are the reference. Read them from a Ren'Py checkout or GitHub; never vendor them

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
- Don't read, list or write the user's real Ren'Py folder (`~/.renpy`, `%APPDATA%\RenPy`, `~/Library/RenPy`) in tests or proofs: tests use the fixtures, and a proof run sets `HOME` to a scratch folder. If a real save is needed, ask the user for a copy
- Don't commit secrets, `.env` files or real users' keys (the keys in `internal/renpysave/testdata` were made for the tests)
- Don't leave processes running: every Xvfb, compositor or app started for a test or a proof runs under `timeout -k`, in its own process group, and `pgrep -a` shows nothing left before a task is reported done
- Don't write a save without a way back: `WriteFile` keeps the first version in `<file>.bak`
