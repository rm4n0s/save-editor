package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/glutter/gluttertest"
	m "github.com/rm4n0s/glutter/material"
	"github.com/rm4n0s/save-editor/internal/model"
)

// The fake format is a text file for the tests of the generic path of the editor: the line
// "FAKESAVE", then lines "name=kind:value" with the kinds i (a byte, 0 to 255), f (a float32),
// b (a bool) and s (an ASCII text), and optionally the line "#picture". It is nothing like a
// real format, and shares nothing with Ren'Py.
const fakeMagic = "FAKESAVE\n"

type fakeDoc struct {
	lines []string // the lines after the magic, kept as they were
	vars  []model.Variable
	sites map[*model.Site]int // the line of each site
}

// fakePictureDoc is a fakeDoc with a screenshot.
type fakePictureDoc struct{ *fakeDoc }

func (fakePictureDoc) Screenshot() []byte {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for x := range 4 {
		img.Set(x, 1, color.RGBA{R: 200, A: 255})
	}
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

var fakeFormat = model.Format{
	Name:     "Fake",
	Patterns: []string{"*.fake"},
	Detect:   func(_ string, data []byte) bool { return bytes.HasPrefix(data, []byte(fakeMagic)) },
	Parse:    parseFake,
}

func parseFake(_ string, data []byte) (model.Document, error) {
	d := &fakeDoc{sites: map[*model.Site]int{}}
	hasPicture := false
	for i, line := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(string(data), fakeMagic), "\n"), "\n") {
		d.lines = append(d.lines, line)
		if line == "#picture" {
			hasPicture = true
			continue
		}
		name, rest, ok := strings.Cut(line, "=")
		kind, text, ok2 := strings.Cut(rest, ":")
		if !ok || !ok2 {
			return nil, errors.New("FakeBadLine", "the line is not name=kind:value", "line", i)
		}
		v := &model.Value{}
		site := &model.Site{ID: i}
		switch kind {
		case "i":
			n, err := strconv.Atoi(text)
			if err != nil {
				return nil, errors.NewErr("FakeBadNumber", err)
			}
			v.Kind, v.Type, v.Int = model.KindInt, "byte", big.NewInt(int64(n))
			site.IntMin, site.IntMax = big.NewInt(0), big.NewInt(255)
		case "f":
			f, err := strconv.ParseFloat(text, 32)
			if err != nil {
				return nil, errors.NewErr("FakeBadNumber", err)
			}
			v.Kind, v.Type, v.Float, v.Float32 = model.KindFloat, "single", f, true
		case "b":
			v.Kind, v.Type, v.Bool = model.KindBool, "flag", text == "true"
		case "s":
			v.Kind, v.Type, v.Str = model.KindString, "text", text
			site.CheckText = func(t string) error {
				for _, r := range t {
					if r > 0x7f || r == '\n' {
						return errors.New("FakeText", "Use only ASCII letters")
					}
				}
				return nil
			}
		default:
			return nil, errors.New("FakeBadKind", "unknown kind", "kind", kind)
		}
		d.sites[site] = i
		d.vars = append(d.vars, model.Variable{Name: name, Internal: strings.HasPrefix(name, "_"), Slot: model.Slot{Value: v, Site: site}})
	}
	if hasPicture {
		return fakePictureDoc{d}, nil
	}
	return d, nil
}

func (d *fakeDoc) Variables() []model.Variable { return d.vars }

func (d *fakeDoc) Details() []model.Detail {
	return []model.Detail{{Label: "Lines", Text: strconv.Itoa(len(d.lines))}}
}

func (d *fakeDoc) Encode(edits []model.Edit) ([]byte, error) {
	lines := append([]string(nil), d.lines...)
	for _, e := range edits {
		i, ok := d.sites[e.Site]
		if !ok {
			return nil, errors.New("FakeForeignSite", "the site is not of this file")
		}
		name, rest, _ := strings.Cut(lines[i], "=")
		kind, _, _ := strings.Cut(rest, ":")
		var text string
		switch kind {
		case "i":
			if e.Value.Kind != model.KindInt || e.Value.Int.Sign() < 0 || e.Value.Int.Cmp(big.NewInt(255)) > 0 {
				return nil, errors.New("FakeRange", "out of range")
			}
			text = e.Value.Int.String()
		case "f":
			text = strconv.FormatFloat(e.Value.Float, 'g', -1, 32)
		case "b":
			text = strconv.FormatBool(e.Value.Bool)
		default:
			text = e.Value.Str
		}
		lines[i] = name + "=" + kind + ":" + text
	}
	return []byte(fakeMagic + strings.Join(lines, "\n") + "\n"), nil
}

func fakeFormats() []model.Format { return []model.Format{fakeFormat, supportedFormats()[0]} }

func writeFake(t *testing.T, name string, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(fakeMagic+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFake(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(string(data), fakeMagic)
}

func TestGenericFormatShowsNoRenpyParts(t *testing.T) {
	ts, _ := newTesterWith(t, writeFake(t, "a.fake", "hp=i:10", "gold=i:5"), "", fakeFormats())
	for _, text := range []string{"Format", "Fake", "Lines", "hp", "gold"} {
		if !ts.Exists(gluttertest.ByText(text)) {
			t.Errorf("%q is not shown", text)
		}
	}
	if ts.Exists(gluttertest.ByKey(m.NewValueKey("save name"))) || ts.Exists(gluttertest.ByTextContaining("Sign with")) {
		t.Error("the Ren'Py name and signature are shown for another format")
	}
	if ts.Exists(gluttertest.ByText("Internals")) {
		t.Error("the Internals chip is shown without internal variables")
	}
	if ts.Exists(gluttertest.ByType[*m.Image]()) {
		t.Error("a screenshot is shown for a document without one")
	}
}

func TestGenericFormatShowsInternalsAndPicture(t *testing.T) {
	ts, _ := newTesterWith(t, writeFake(t, "b.fake", "#picture", "hp=i:10", "_debug=b:true"), "", fakeFormats())
	if !ts.Exists(gluttertest.ByType[*m.Image]()) {
		t.Error("the screenshot is not shown")
	}
	if ts.Exists(gluttertest.ByText("_debug")) {
		t.Error("an internal variable is shown without the chip")
	}
	ts.Tap(gluttertest.ByText("Internals"))
	ts.Pump()
	if !ts.Exists(gluttertest.ByText("_debug")) {
		t.Error("the chip does not show the internal variable")
	}
}

func TestGenericEditRespectsTheBoundsAndSaves(t *testing.T) {
	path := writeFake(t, "c.fake", "hp=i:10", "speed=f:0.5", "alive=b:true", "name=s:Ann")
	original := readFake(t, path)
	ts, _ := newTesterWith(t, path, "", fakeFormats())

	tests := []struct {
		id, text, err string
	}{
		{"hp", "256", "Enter a whole number from 0 to 255"},
		{"hp", "-1", "Enter a whole number from 0 to 255"},
		{"hp", "many", "Enter a whole number from 0 to 255"},
		{"speed", "1e39", "This number is too large or too small"},
		{"name", "Ωμέγα", "Use only ASCII letters"},
	}
	for _, tt := range tests {
		ts.EnterText(fieldOf(tt.id), tt.text)
		ts.Pump()
		if !ts.Exists(gluttertest.ByText(tt.err)) {
			t.Errorf("%s = %q: the error %q is not shown", tt.id, tt.text, tt.err)
		}
		ts.Tap(gluttertest.ByTooltip("Save (Ctrl+S)"))
		ts.Pump()
		if got := state(ts).status; got != "Fix the values marked in red before saving." {
			t.Errorf("%s = %q: status %q", tt.id, tt.text, got)
		}
		if readFake(t, path) != original {
			t.Fatalf("%s = %q: the file was written", tt.id, tt.text)
		}
		ts.EnterText(fieldOf(tt.id), editText(state(ts).vars[indexOf(t, state(ts), tt.id)].Slot.Value))
		ts.Pump()
	}
	if n := len(state(ts).edits); n != 0 {
		t.Fatalf("%d edits are left after typing the old values back", n)
	}

	ts.EnterText(fieldOf("hp"), "255")
	ts.EnterText(fieldOf("speed"), "0.1")
	ts.EnterText(fieldOf("name"), "Bob")
	ts.PumpAndSettle()
	ts.Tap(gluttertest.Descendant(gluttertest.ByKey(m.NewValueKey("alive")), gluttertest.ByType[*m.Switch]()))
	ts.Pump()
	if !ts.Exists(gluttertest.ByText("4 changes")) {
		t.Fatalf("changes not counted; status %q", state(ts).status)
	}
	ts.Tap(gluttertest.ByTooltip("Save (Ctrl+S)"))
	waitFor(t, ts, "the save", func() bool { return len(state(ts).edits) == 0 && !state(ts).busy })

	if got, want := readFake(t, path), "hp=i:255\nspeed=f:0.1\nalive=b:false\nname=s:Bob\n"; got != want {
		t.Errorf("file %q, want %q", got, want)
	}
	if bak, err := os.ReadFile(path + ".bak"); err != nil || strings.TrimPrefix(string(bak), fakeMagic) != original {
		t.Errorf("the backup is not the original: %v", err)
	}
	if got := state(ts).format.Name; got != "Fake" {
		t.Errorf("the open format is %q", got)
	}
	if v := state(ts).vars[indexOf(t, state(ts), "speed")].Slot.Value; v.Float != float64(float32(0.1)) {
		t.Errorf("speed read back as %v", v.Float)
	}
}

func TestGenericSaveAsUsesTheFormatOfTheDocument(t *testing.T) {
	path := writeFake(t, "d.fake", "hp=i:10")
	ts, fd := newTesterWith(t, path, "", fakeFormats())
	ts.EnterText(fieldOf("hp"), "42")
	ts.Pump()
	ts.Tap(gluttertest.ByTooltip("Save as (Ctrl+Shift+S)"))
	fd.WaitFor("the file dialog", func() bool { return fd.PendingFileChoosers() == 1 })
	target := filepath.Join(filepath.Dir(path), "e.fake")
	fd.AnswerFileChooser(target)
	waitFor(t, ts, "the save", func() bool { return state(ts).path == target })
	if got := readFake(t, target); got != "hp=i:42\n" {
		t.Errorf("saved %q", got)
	}
	if got := readFake(t, path); got != "hp=i:10\n" {
		t.Errorf("the first file changed: %q", got)
	}
	if _, err := os.Stat(target + ".bak"); !os.IsNotExist(err) {
		t.Error("save as made a backup")
	}
}

func TestOpensAnyRegisteredFormat(t *testing.T) {
	ts, fd := newTesterWith(t, "", "", fakeFormats())
	if !ts.Exists(gluttertest.ByTextContaining("Fake, Ren'Py")) {
		t.Error("the empty page does not list the formats")
	}
	ts.Tap(gluttertest.ByText("Open save…"))
	fd.WaitFor("the file dialog", func() bool { return fd.PendingFileChoosers() == 1 })
	fd.AnswerFileChooser(writeFake(t, "f.fake", "hp=i:1"))
	waitFor(t, ts, "the save", func() bool { return state(ts).doc != nil })
	if state(ts).format.Name != "Fake" || state(ts).renpy != nil {
		t.Errorf("format %q, Ren'Py document %v", state(ts).format.Name, state(ts).renpy)
	}
	// Another file, of the other format, replaces it.
	state(ts).open(copySave(t, "renpy8.save"))
	waitFor(t, ts, "the Ren'Py save", func() bool { return state(ts).renpy != nil })
	ts.Pump()
	if !ts.Exists(gluttertest.ByKey(m.NewValueKey("save name"))) {
		t.Error("the Ren'Py name is not shown for a Ren'Py save")
	}
}

func TestFakeFormatRefusesBadInput(t *testing.T) {
	tests := []struct {
		name  string
		data  string
		route string
	}{
		{"a line without a kind", fakeMagic + "hp\n", "parseFake.FakeBadLine"},
		{"an unknown kind", fakeMagic + "hp=x:1\n", "parseFake.FakeBadKind"},
		{"a bad number", fakeMagic + "hp=i:one\n", "parseFake.FakeBadNumber"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseFake("x.fake", []byte(tt.data))
			appErr, ok := errors.FromError(err)
			if !ok || !appErr.HasRoute(tt.route) {
				t.Errorf("error %v", err)
			}
		})
	}
	doc, err := parseFake("x.fake", []byte(fakeMagic+"hp=i:1\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []model.Edit{
		{Site: &model.Site{}, Value: &model.Value{Kind: model.KindInt, Int: big.NewInt(1)}},
		{Site: doc.Variables()[0].Slot.Site, Value: &model.Value{Kind: model.KindInt, Int: big.NewInt(math.MaxInt16)}},
	} {
		if _, err := doc.Encode([]model.Edit{e}); err == nil {
			t.Errorf("edit %+v was accepted", e)
		}
	}
}
