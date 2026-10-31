# IMPLEMENTATION: steps

The plan that turns [SPEC.md](SPEC.md) into code. One implementing agent does the steps in order, one at a time.
After each step it appends an entry to [IMPLEMENTED.md](IMPLEMENTED.md) and stops for the manager's review; it
starts the next step only when told to.

## How every step is done
- Read CLAUDE.md, SPEC.md and this file first. Before writing code that makes, returns or tests an error, read
  the whole `$(go env GOMODCACHE)/github.com/rm4n0s/errors@v0.2.0/AI_GUIDE.md`.
- Do not change `internal/model/model.go`; if the step needs a change, stop and ask for it in the entry.
- Do not download, build or run third-party code (no git clone and build, no pip/npm/cargo installs). Reading
  sources and specs on the web is fine. Do not copy engine code into the repository; a table derived from a
  spec, with credit, is fine.
- Never read the user's real game or save folders; a proof run sets `HOME` to a scratch folder.
- Finish every step with the Definition of done of CLAUDE.md on the whole repository (`gofmt -l .`,
  `go vet ./...`, `golangci-lint run`, `go test -race ./...`, `CGO_ENABLED=0 go build ./...`,
  `CGO_ENABLED=0 go test ./...`). A step is not done while any of them fails.
- **Screenshot**: when the step changes what the app can open or show, run the real app headless on the step's
  fixture as CLAUDE.md describes (private Xvfb under `timeout -k`, own process group, `HOME` in a scratch
  folder, `GLUTTER_SCREENSHOT`), save it as `docs/screenshots/step-NN-<what>.png`, look at it, and link it from
  the entry. When a screenshot is not feasible (the step changes nothing visible, or the run fails), say why in
  the entry. `pgrep -a` shows nothing left running.
- Do not commit; commits are made only when the user asks.

## Entry format in IMPLEMENTED.md
```
## Step NN: <title>
- Done: what was built or changed (files)
- Supported / not supported: specific and honest
- Checks: each command of the Definition of done and its result
- Verified against: the independent reference, or "none" and the command a user can run
- Screenshot: ![...](docs/screenshots/step-NN-....png) or why there is none
- Open questions for the manager
```

## Steps

### Step 01: Baseline
`go mod tidy` (golang.org/x/text becomes a direct requirement). Make the Definition of done pass on the whole
repository as it is now; the unfinished packages (`internal/rpgmv`, `internal/nrbf`) may be fixed just enough
to build and vet, or have their failing tests marked for step 03/05 in the entry. Start IMPLEMENTED.md with a
"Step 00: before this plan" entry that lists what already exists (Glutter v1.9.9, `internal/model`, the
Ren'Py format and app on the model, `internal/gvas`, `internal/lcf`, `internal/rubymarshal`, `internal/rgss`,
and the unfinished `internal/jsondoc`, `internal/rpgmv`, `internal/unityprefs`, `internal/nrbf`). Screenshot:
the app on `internal/renpysave/testdata/renpy8.save`.

### Step 02: Register the finished formats
Add `gvas.Format`, `lcf.Format` and `rgss.Formats...` to `supportedFormats()`. Test that every fixture of
every registered format is detected by its own format and no other, opens in the app (the page shows its
variables and details), and that an edit of an int through the app is saved and reads back. Screenshots: one
GVAS, one LSD and one VX Ace fixture.

### Step 03: RPG Maker MV and MZ
Review and finish `internal/jsondoc` and `internal/rpgmv` against SPEC.md and CLAUDE.md. Fix the failing tests
(`TestLayerOfLZStringFourteen`, `TestEdits/int` where an edit through a JsonEx `$ref`/`@r` reference must change
the shared value, `TestParseErrors` routes). Decide from the engines' sources how MZ writes its zlib string to
disk and test exactly that. The node checks in `testdata/engine.js` run the engines' own scripts given by path
and skip without them: keep them that way, write in the entry how a user runs them, and do not fetch the
scripts. Register the formats. Screenshot: an MV and an MZ fixture.

### Step 04: Unity PlayerPrefs
Review and finish `internal/unityprefs` (Linux XML prefs, macOS binary plist) against SPEC.md; the plist checks
use python3's standard `plistlib`. Unity's own keys are Internal. Register the format. Screenshot: the Linux
prefs fixture.

### Step 05: .NET BinaryFormatter
Review and finish `internal/nrbf` and write its tests (it has none): every record type BinaryFormatter writes,
shared objects and strings, List/Dictionary shown as items, every primitive type with its bounds, string edits,
refused edits, corrupted input. Fixtures from `testdata/gen.py`. Register the format and make sure detection
does not take files of other formats. Screenshot: `player.sav`.

### Step 06: Unity JSON and Easy Save 3
New package `internal/unityjson` on `internal/jsondoc`: plain JSON saves (detected by contents; a `.json` or
`.txt` name alone is not enough when the contents are not a JSON object or array), and Easy Save 3 `.es3` files
(ES3's JSON dialect with `__type` entries; gzip; AES-128-CBC with a PBKDF2 key from a password, ES3's default
password "password" tried automatically; verify the exact ES3 layout, IV placement, iteration count and key
derivation from ES3's documentation and public sources). A file encrypted with an unknown password is refused
with a message saying so. Use only the standard library (`crypto/aes`, `crypto/pbkdf2`, `compress/gzip`).
Register the formats, ordered so that more specific formats detect before plain JSON. Screenshot: an ES3
fixture.

### Step 07: Detection and the app across all formats
One table-driven test over every fixture in the repository: detected by exactly one format, the expected one.
App tests: open each format, the Internals chip only when there are internal variables, the screenshot only for
Ren'Py, Save as offers the document's format. A container with one part reads "(1 part)", not "(1 parts)"
(the row text of the app; use `model.Value.Repr`'s wording). Add a golden image of a non-Ren'Py document (`-update-goldens`,
look at it). Screenshot: the empty page listing all formats.

### Step 08: Documentation
Update README.md (what the editor opens, where each engine keeps its saves, limits per engine, a screenshot)
and CLAUDE.md (Project, Layout, Commands for every fixture generator, "How saves are edited" for every format,
how to add a format). Keep SPEC.md true to what was built. No screenshot needed unless the README uses a new one.

### Step 09: Final check
The full Definition of done, `pgrep -a` clean, every fixture opened in the real app once (a screenshot of any
that looks wrong), and an IMPLEMENTED.md summary: what works, what is unverified against an independent tool,
and what a user should test with real saves.
