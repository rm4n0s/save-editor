package jsondoc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// dump writes a value as one line: kinds and values, with "!" after a primitive that has no
// site.
func dump(sb *strings.Builder, s model.Slot) {
	v := s.Value
	switch v.Kind {
	case model.KindList, model.KindMap:
		open, shut := "[", "]"
		if v.Kind == model.KindMap {
			open, shut = "{", "}"
		}
		sb.WriteString(open)
		for i, c := range v.Children {
			if i > 0 {
				sb.WriteString(",")
			}
			if v.Kind == model.KindMap {
				sb.WriteString(strconv.Quote(c.Label) + ":")
			}
			dump(sb, c.Slot)
		}
		sb.WriteString(shut)
	default:
		sb.WriteString(v.Kind.String() + ":" + v.Repr())
		if s.Site == nil {
			sb.WriteString("!")
		}
	}
}

func dumpOf(d *Doc) string {
	var sb strings.Builder
	dump(&sb, d.Root)
	return sb.String()
}

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"null", `null`, `null:null`},
		{"bool", ` true `, `bool:true`},
		{"int", `-12`, `int:-12`},
		{"minus zero", `-0`, `int:0`},
		{"float", `1.5e3`, `float:1500.0`},
		{"exponent int-looking", `1E2`, `float:100.0`},
		{"huge int stays exact", `123456789012345678901234567890`, `int:123456789012345678901234567890`},
		{"float overflow is inf", `1e999`, `float:inf`},
		{"string", `"a\"b\\c\/d\b\f\n\r\tAé"`, "string:\"a\\\"b\\\\c/d\\b\\f\\n\\r\\t" + `Aé"`},
		{"astral escape", `"😀"`, `string:"😀"`},
		{"raw astral", `"😀 ü"`, `string:"😀 ü"`},
		{"lone surrogate has no site", `"a\ud800b"`, "string:\"a�b\"!"},
		{"lone low surrogate", `"\udc00"`, "string:\"�\"!"},
		{"high then non-low", `"\ud800A"`, "string:\"�A\"!"},
		{"empty containers", `{"a":[],"b":{}}`, `{"a":[],"b":{}}`},
		{"order and duplicates kept", `{"b":1,"a":2,"b":3}`, `{"b":int:1,"a":int:2,"b":int:3}`},
		{"nested", "\n{ \"a\" : [ 1 , 2.0 , [ null ] ] ,\t\"b\":{\"c\":false}}\r\n", `{"a":[int:1,float:2.0,[null:null]],"b":{"c":bool:false}}`},
		{"bom", "\xEF\xBB\xBF[1]", `[int:1]`},
		{"empty key", `{"":0}`, `{"":int:0}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Parse([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if got := dumpOf(d); got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
			out, err := d.Patch(nil)
			if err != nil || string(out) != tt.in {
				t.Errorf("Patch(nil) = %q, %v; want the input back", out, err)
			}
		})
	}
}

func TestParseLabels(t *testing.T) {
	d, err := Parse([]byte(`{"k":[10,20]}`))
	if err != nil {
		t.Fatal(err)
	}
	list := d.Root.Value.Children[0].Slot.Value
	if d.Root.Value.Children[0].Label != "k" || list.Children[1].Label != "[1]" {
		t.Errorf("labels: %q %q", d.Root.Value.Children[0].Label, list.Children[1].Label)
	}
}

func TestSites(t *testing.T) {
	d, err := Parse([]byte(`[1,"x",true,null,2.5,[7]]`))
	if err != nil {
		t.Fatal(err)
	}
	if d.SiteCount() != 6 {
		t.Fatalf("SiteCount = %d", d.SiteCount())
	}
	seen := map[int]bool{}
	for i, c := range d.Root.Value.Children {
		if c.Slot.Value.Kind == model.KindList {
			continue
		}
		s := c.Slot.Site
		if s == nil || seen[s.ID] || d.SiteByID(s.ID) != s {
			t.Fatalf("child %d: bad site %+v", i, s)
		}
		seen[s.ID] = true
		sp := s.Ref.(*Span)
		if sp.Kind != c.Slot.Value.Kind {
			t.Errorf("child %d: span kind %v", i, sp.Kind)
		}
		if (c.Slot.Value.Kind == model.KindInt) != (s.IntMax != nil && s.IntMin != nil) {
			t.Errorf("child %d: bounds %v %v", i, s.IntMin, s.IntMax)
		}
	}
	if d.SiteByID(-1) != nil || d.SiteByID(99) != nil {
		t.Error("SiteByID of an unknown ID is not nil")
	}
	want := new(big.Int).Lsh(big.NewInt(1), 53)
	want.Sub(want, big.NewInt(1))
	s := d.Root.Value.Children[0].Slot.Site
	if s.IntMax.Cmp(want) != 0 || s.IntMin.Cmp(new(big.Int).Neg(want)) != 0 {
		t.Errorf("default bounds %v %v", s.IntMin, s.IntMax)
	}
}

func TestOptions(t *testing.T) {
	d, err := ParseOptions([]byte(`[5]`), Options{IntMin: big.NewInt(0), IntMax: big.NewInt(9), MaxDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	s := d.Root.Value.Children[0].Slot.Site
	if s.IntMin.Int64() != 0 || s.IntMax.Int64() != 9 {
		t.Errorf("bounds %v %v", s.IntMin, s.IntMax)
	}
	if _, err := ParseOptions([]byte(`[[[1]]]`), Options{MaxDepth: 3}); err != nil {
		t.Errorf("depth 3 within limit 3: %v", err)
	}
	_, err = ParseOptions([]byte(`[[[[1]]]]`), Options{MaxDepth: 3})
	assertRoute(t, err, "ParseOptions->parser.parseDocument->parser.parseValue->parser.parseArray->parser.parseValue->parser.parseArray->parser.parseValue->parser.parseArray->parser.parseValue->parser.parseArray->parser.enter.JsonTooDeep")
}

func assertRoute(t *testing.T, err error, route string) {
	t.Helper()
	e, ok := errors.FromError(err)
	if !ok {
		t.Fatalf("want an *errors.Error, got %T %v", err, err)
	}
	if !e.HasRoute(route) {
		t.Errorf("route %s\nwant %s", e.Route(), route)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		tag   string
		route string
		msg   string
	}{
		{"empty", ``, tagSyntax, "Parse->ParseOptions->parser.parseDocument.JsonSyntax", "no JSON value"},
		{"only space", " \n", tagSyntax, "parser.parseDocument.JsonSyntax", "no JSON value"},
		{"trailing", `1 2`, tagSyntax, "parser.parseDocument.JsonSyntax", "after the JSON value"},
		{"bom in the middle", "[1]\xEF\xBB\xBF", tagSyntax, "parser.parseDocument.JsonSyntax", "after the JSON value"},
		{"double bom", "\xEF\xBB\xBF\xEF\xBB\xBF1", tagSyntax, "parser.parseValue.JsonSyntax", "Unexpected"},
		{"unexpected", `?`, tagSyntax, "parser.parseValue.JsonSyntax", "Unexpected '?'"},
		{"nothing where value", `[`, tagSyntax, "parser.parseValue.JsonSyntax", "ends where a value"},
		{"single quotes", `{'a':1}`, tagSyntax, "parser.parseObject.JsonSyntax", "string key"},
		{"comment", `[1,/*x*/2]`, tagSyntax, "parser.parseValue.JsonSyntax", "Unexpected '/'"},
		{"trailing comma array", `[1,]`, tagSyntax, "parser.parseValue.JsonSyntax", "Unexpected ']'"},
		{"trailing comma object", `{"a":1,}`, tagSyntax, "parser.parseObject.JsonSyntax", "string key"},
		{"missing colon", `{"a" 1}`, tagSyntax, "parser.parseObject.JsonSyntax", "Expected ':'"},
		{"missing comma array", `[1 2]`, tagSyntax, "parser.parseArray.JsonSyntax", "Expected ',' or ']'"},
		{"missing comma object", `{"a":1 "b":2}`, tagSyntax, "parser.parseObject.JsonSyntax", "Expected ',' or '}'"},
		{"unclosed array", `[1`, tagSyntax, "parser.parseArray.JsonSyntax", "ends inside an array"},
		{"unclosed object", `{"a":1`, tagSyntax, "parser.parseObject.JsonSyntax", "ends inside an object"},
		{"unclosed object key", `{`, tagSyntax, "parser.parseObject.JsonSyntax", "ends inside an object"},
		{"nan", `NaN`, tagSyntax, "parser.parseValue.JsonSyntax", "Unexpected 'N'"},
		{"bad literal", `tru`, tagSyntax, "parser.literal.JsonSyntax", "Expected true"},
		{"literal prefix", `nullx`, tagSyntax, "parser.literal.JsonSyntax", "Expected null"},
		{"leading plus", `+1`, tagSyntax, "parser.parseValue.JsonSyntax", "Unexpected '+'"},
		{"leading zero", `01`, tagSyntax, "parser.parseNumber.JsonSyntax", "in a number"},
		{"lone minus", `-`, tagSyntax, "parser.parseNumber.JsonSyntax", "digit"},
		{"hex", `0x10`, tagSyntax, "parser.parseNumber.JsonSyntax", "in a number"},
		{"dot without digits", `1.`, tagSyntax, "parser.parseNumber.JsonSyntax", "after the '.'"},
		{"leading dot", `.5`, tagSyntax, "parser.parseValue.JsonSyntax", "Unexpected '.'"},
		{"exponent without digits", `1e+`, tagSyntax, "parser.parseNumber.JsonSyntax", "exponent"},
		{"unclosed string", `"abc`, tagSyntax, "parser.parseString.JsonSyntax", "not closed"},
		{"raw newline in string", "\"a\nb\"", tagSyntax, "parser.parseString.JsonSyntax", "control character"},
		{"raw tab in string", "\"a\tb\"", tagSyntax, "parser.parseString.JsonSyntax", "control character"},
		{"bad escape", `"\x41"`, tagSyntax, "parser.parseString.JsonSyntax", "unknown escape"},
		{"short u escape", `"\u12"`, tagSyntax, "parser.parseString.JsonSyntax", "four hexadecimal"},
		{"non-hex u escape", `"\u12G4"`, tagSyntax, "parser.parseString.JsonSyntax", "four hexadecimal"},
		{"escape at end", `"\`, tagSyntax, "parser.parseString.JsonSyntax", "not closed"},
		{"invalid utf-8", "\"a\xffb\"", tagSyntax, "parser.parseString.JsonSyntax", "UTF-8"},
		{"truncated utf-8", "\"a\xe2\x82\"", tagSyntax, "parser.parseString.JsonSyntax", "UTF-8"},
		{"too deep", strings.Repeat("[", 100000), tagTooDeep, "parser.enter.JsonTooDeep", "nested too deeply"},
		{"too deep objects", strings.Repeat(`{"a":`, 100000), tagTooDeep, "parser.enter.JsonTooDeep", "nested too deeply"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Parse([]byte(tt.in))
			if err == nil {
				t.Fatalf("parsed to %s", dumpOf(d))
			}
			e, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("not an *errors.Error: %T", err)
			}
			if e.Tag != tt.tag || !strings.Contains(e.Message, tt.msg) || !e.HasRoute(tt.route) {
				t.Errorf("tag %s, message %q, route %s\nwant tag %s, message with %q, route %s",
					e.Tag, e.Message, e.Route(), tt.tag, tt.msg, tt.route)
			}
		})
	}
}

func TestErrorPosition(t *testing.T) {
	_, err := Parse([]byte("{\n  \"é\": ?\n}"))
	e, ok := errors.FromError(err)
	if !ok || e.Metadata["at"] != "line 2, column 8" {
		t.Errorf("metadata %v", e)
	}
}

func TestDeepAtLimit(t *testing.T) {
	in := strings.Repeat("[", DefaultMaxDepth) + strings.Repeat("]", DefaultMaxDepth)
	if _, err := Parse([]byte(in)); err != nil {
		t.Errorf("depth %d: %v", DefaultMaxDepth, err)
	}
}

func TestPatch(t *testing.T) {
	const src = "\xEF\xBB\xBF{ \"n\" : 12 ,\n \"f\":1.50,\"s\":\"hi\\u00e9\",\"b\":false,\"z\":null,\"l\":[1,2,3], \"d\":0.1e1}"
	bigInt := func(s string) *big.Int { n, _ := new(big.Int).SetString(s, 10); return n }
	tests := []struct {
		name string
		path string
		new  model.Value
		want string
	}{
		{"int", "n", model.Value{Kind: model.KindInt, Int: big.NewInt(-7)}, "{ \"n\" : -7 ,\n \"f\":1.50,\"s\":\"hi\\u00e9\",\"b\":false,\"z\":null,\"l\":[1,2,3], \"d\":0.1e1}"},
		{"int to the bound", "n", model.Value{Kind: model.KindInt, Int: bigInt("9007199254740991")}, "9007199254740991"},
		{"float", "f", model.Value{Kind: model.KindFloat, Float: 0.1}, `"f":0.1,`},
		{"float whole", "f", model.Value{Kind: model.KindFloat, Float: 3}, `"f":3.0,`},
		{"float exponent", "f", model.Value{Kind: model.KindFloat, Float: 1e22}, `"f":1e+22,`},
		{"float tiny", "f", model.Value{Kind: model.KindFloat, Float: 1.5e-7}, `"f":1.5e-07,`},
		{"float negative zero", "f", model.Value{Kind: model.KindFloat, Float: math.Copysign(0, -1)}, `"f":-0.0,`},
		{"float32", "f", model.Value{Kind: model.KindFloat, Float: float64(float32(0.1)), Float32: true}, `"f":0.1,`},
		{"string", "s", model.Value{Kind: model.KindString, Str: "a\"b\\c\n\x01é😀/<>"}, `"s":"a\"b\\c\n\u0001é😀/<>",`},
		{"empty string", "s", model.Value{Kind: model.KindString}, `"s":"",`},
		{"bool", "b", model.Value{Kind: model.KindBool, Bool: true}, `"b":true,`},
		{"null", "z", model.Value{Kind: model.KindNull}, `"z":null,`},
		{"list element", "l.1", model.Value{Kind: model.KindInt, Int: big.NewInt(100)}, `"l":[1,100,3]`},
		{"later number", "d", model.Value{Kind: model.KindFloat, Float: 2.5}, `"d":2.5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Parse([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			slot := find(t, d, tt.path)
			out, err := d.Patch([]model.Edit{{Site: slot.Site, Value: &tt.new}})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(out), "\xEF\xBB\xBF{ \"n\"") {
				t.Errorf("the start was not kept: %q", out)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Errorf("got %q\nwant it to contain %q", out, tt.want)
			}
			// Parsed back: the edited value changed, every other value is as it was.
			d2, err := Parse(out)
			if err != nil {
				t.Fatalf("the output is not valid: %v\n%s", err, out)
			}
			got := find(t, d2, tt.path).Value
			if got.Kind != tt.new.Kind {
				t.Errorf("kind %v, want %v", got.Kind, tt.new.Kind)
			}
			switch tt.new.Kind {
			case model.KindInt:
				if got.Int.Cmp(tt.new.Int) != 0 {
					t.Errorf("int %v", got.Int)
				}
			case model.KindFloat:
				if got.Float != tt.new.Float && !(tt.new.Float32 && float32(got.Float) == float32(tt.new.Float)) {
					t.Errorf("float %v", got.Float)
				}
			case model.KindString:
				if got.Str != tt.new.Str {
					t.Errorf("string %q", got.Str)
				}
			case model.KindBool:
				if got.Bool != tt.new.Bool {
					t.Errorf("bool %v", got.Bool)
				}
			}
			before, after := dumpWithout(d, tt.path), dumpWithout(d2, tt.path)
			if before != after {
				t.Errorf("another value changed:\n%s\n%s", before, after)
			}
		})
	}
}

// find returns the slot at a path of keys and indexes separated by dots.
func find(t *testing.T, d *Doc, path string) model.Slot {
	t.Helper()
	s := d.Root
	for _, step := range strings.Split(path, ".") {
		label := step
		if s.Value.Kind == model.KindList {
			label = "[" + step + "]"
		}
		found := false
		for _, c := range s.Value.Children {
			if c.Label == label {
				s, found = c.Slot, true
				break
			}
		}
		if !found {
			t.Fatalf("no %q in the path %s", step, path)
		}
	}
	return s
}

// dumpWithout dumps the document with the value at the path left out.
func dumpWithout(d *Doc, path string) string {
	var sb strings.Builder
	var walk func(prefix string, s model.Slot)
	walk = func(prefix string, s model.Slot) {
		if prefix == path {
			sb.WriteString("<edited>")
			return
		}
		v := s.Value
		if v.Kind == model.KindList || v.Kind == model.KindMap {
			for _, c := range v.Children {
				label := strings.Trim(c.Label, "[]")
				if prefix != "" {
					label = prefix + "." + label
				}
				sb.WriteString(label + "=")
				walk(label, c.Slot)
				sb.WriteString(";")
			}
			return
		}
		sb.WriteString(v.Repr())
	}
	walk("", d.Root)
	return sb.String()
}

func TestPatchSeveralEdits(t *testing.T) {
	d, err := Parse([]byte(`[1, "aa", 3,  4.5 ]`))
	if err != nil {
		t.Fatal(err)
	}
	c := d.Root.Value.Children
	// Given out of order, and the first changes the length.
	out, err := d.Patch([]model.Edit{
		{Site: c[3].Slot.Site, Value: &model.Value{Kind: model.KindFloat, Float: 1e100}},
		{Site: c[1].Slot.Site, Value: &model.Value{Kind: model.KindString, Str: "b"}},
		{Site: c[0].Slot.Site, Value: &model.Value{Kind: model.KindInt, Int: big.NewInt(123456)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `[123456, "b", 3,  1e+100 ]`; string(out) != want {
		t.Errorf("got %s want %s", out, want)
	}
	if string(d.Data()) != `[1, "aa", 3,  4.5 ]` {
		t.Error("Patch changed the document")
	}
}

func TestPatchRefused(t *testing.T) {
	d, err := Parse([]byte(`{"i":1,"f":1.5,"s":"x","b":true,"n":null}`))
	if err != nil {
		t.Fatal(err)
	}
	other, err := Parse([]byte(`{"i":1}`))
	if err != nil {
		t.Fatal(err)
	}
	site := func(k string) *model.Site { return find(t, d, k).Site }
	limited, err := ParseOptions([]byte(`[5]`), Options{IntMin: big.NewInt(0), IntMax: big.NewInt(9)})
	if err != nil {
		t.Fatal(err)
	}
	i := func(n *big.Int) *model.Value { return &model.Value{Kind: model.KindInt, Int: n} }
	str := &model.Value{Kind: model.KindString, Str: "y"}
	tests := []struct {
		name  string
		doc   *Doc
		edits []model.Edit
		tag   string
		route string
	}{
		{"nil site", d, []model.Edit{{Value: i(big.NewInt(1))}}, tagEditSite, "Patch.JsonEditSite"},
		{"site of another document", d, []model.Edit{{Site: other.Root.Value.Children[0].Slot.Site, Value: i(big.NewInt(1))}}, tagEditSite, "Patch.JsonEditSite"},
		{"made up site", d, []model.Edit{{Site: &model.Site{ID: 0, Ref: &Span{}}, Value: i(big.NewInt(1))}}, tagEditSite, "Patch.JsonEditSite"},
		{"no value", d, []model.Edit{{Site: site("i")}}, tagEditKind, "Patch.JsonEditKind"},
		{"string over int", d, []model.Edit{{Site: site("i"), Value: str}}, tagEditKind, "Patch.JsonEditKind"},
		{"int over float", d, []model.Edit{{Site: site("f"), Value: i(big.NewInt(1))}}, tagEditKind, "Patch.JsonEditKind"},
		{"int over string", d, []model.Edit{{Site: site("s"), Value: i(big.NewInt(1))}}, tagEditKind, "Patch.JsonEditKind"},
		{"bool over null", d, []model.Edit{{Site: site("n"), Value: &model.Value{Kind: model.KindBool}}}, tagEditKind, "Patch.JsonEditKind"},
		{"int too big", d, []model.Edit{{Site: site("i"), Value: i(new(big.Int).Lsh(big.NewInt(1), 53))}}, tagEditRange, "Patch.JsonEditRange"},
		{"int too small", d, []model.Edit{{Site: site("i"), Value: i(new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 53)))}}, tagEditRange, "Patch.JsonEditRange"},
		{"int over the option", limited, []model.Edit{{Site: limited.Root.Value.Children[0].Slot.Site, Value: i(big.NewInt(10))}}, tagEditRange, "Patch.JsonEditRange"},
		{"int under the option", limited, []model.Edit{{Site: limited.Root.Value.Children[0].Slot.Site, Value: i(big.NewInt(-1))}}, tagEditRange, "Patch.JsonEditRange"},
		{"nan", d, []model.Edit{{Site: site("f"), Value: &model.Value{Kind: model.KindFloat, Float: math.NaN()}}}, tagEditFloat, "Patch.JsonEditFloat"},
		{"inf", d, []model.Edit{{Site: site("f"), Value: &model.Value{Kind: model.KindFloat, Float: math.Inf(-1)}}}, tagEditFloat, "Patch.JsonEditFloat"},
		{"invalid utf-8", d, []model.Edit{{Site: site("s"), Value: &model.Value{Kind: model.KindString, Str: "a\xffb"}}}, tagEditText, "Patch.JsonEditText"},
		{"twice", d, []model.Edit{{Site: site("s"), Value: str}, {Site: site("s"), Value: str}}, tagEditTwice, "Patch.JsonEditTwice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.doc.Patch(tt.edits)
			if err == nil {
				t.Fatalf("accepted: %s", out)
			}
			e, ok := errors.FromError(err)
			if !ok || e.Tag != tt.tag || !e.HasRoute(tt.route) {
				t.Errorf("got %v, route %s; want tag %s route %s", err, e.Route(), tt.tag, tt.route)
			}
		})
	}
}

func TestPatchCheckText(t *testing.T) {
	d, err := Parse([]byte(`["x"]`))
	if err != nil {
		t.Fatal(err)
	}
	s := d.Root.Value.Children[0].Slot.Site
	s.CheckText = func(s string) error {
		if strings.Contains(s, "!") {
			return errors.New("NoBangs", "no bangs")
		}
		return nil
	}
	if _, err := d.Patch([]model.Edit{{Site: s, Value: &model.Value{Kind: model.KindString, Str: "ok"}}}); err != nil {
		t.Fatal(err)
	}
	_, err = d.Patch([]model.Edit{{Site: s, Value: &model.Value{Kind: model.KindString, Str: "no!"}}})
	assertRoute(t, err, "Patch.JsonEditText")
	if e, _ := errors.FromError(err); e.Message != "no bangs" {
		t.Errorf("message %q", e.Message)
	}
}

func TestLossyStringIsNotEditable(t *testing.T) {
	d, err := Parse([]byte(`["\ud800","ok"]`))
	if err != nil {
		t.Fatal(err)
	}
	c := d.Root.Value.Children
	if c[0].Slot.Site != nil || c[1].Slot.Site == nil {
		t.Errorf("sites %v %v", c[0].Slot.Site, c[1].Slot.Site)
	}
}

func TestAppendFloat(t *testing.T) {
	tests := []struct {
		f    float64
		f32  bool
		want string
	}{
		{0, false, "0.0"}, {1, false, "1.0"}, {-2.5, false, "-2.5"}, {0.1, false, "0.1"},
		{1234567, false, "1234567.0"}, {1e20, false, "100000000000000000000.0"}, {1e21, false, "1e+21"},
		{1e-6, false, "0.000001"}, {1e-7, false, "1e-07"}, {math.MaxFloat64, false, "1.7976931348623157e+308"},
		{5e-324, false, "5e-324"}, {float64(float32(0.1)), true, "0.1"}, {float64(float32(16777217)), true, "16777216.0"},
	}
	for _, tt := range tests {
		got := string(AppendFloat(nil, tt.f, tt.f32))
		if got != tt.want {
			t.Errorf("AppendFloat(%v, %v) = %s, want %s", tt.f, tt.f32, got, tt.want)
		}
		bits := 64
		if tt.f32 {
			bits = 32
		}
		if back, err := strconv.ParseFloat(got, bits); err != nil || back != tt.f {
			t.Errorf("%s reads back as %v, %v", got, back, err)
		}
	}
}

func TestAppendString(t *testing.T) {
	tests := []struct{ in, want string }{
		{``, `""`}, {`abc`, `"abc"`}, {"\"\\", `"\"\\"`}, {"\b\f\n\r\t", `"\b\f\n\r\t"`},
		{"\x00\x1f\x7f", `"\u0000\u001f` + "\x7f" + `"`}, {"é😀 ", "\"é😀 \""}, {"/", `"/"`},
		{"a\xffb", "\"a�b\""},
	}
	for _, tt := range tests {
		got := string(AppendString(nil, tt.in))
		if got != tt.want {
			t.Errorf("AppendString(%q) = %s, want %s", tt.in, got, tt.want)
		}
		var back string
		if err := json.Unmarshal([]byte(got), &back); err != nil {
			t.Errorf("%s: %v", got, err)
		}
	}
}

// randomValue makes a random JSON value for the comparison with encoding/json.
func randomValue(r *rand.Rand, depth int) any {
	n := 8
	if depth > 4 {
		n = 5
	}
	switch r.Intn(n) {
	case 0:
		return nil
	case 1:
		return r.Intn(2) == 0
	case 2:
		return json.Number(strconv.FormatInt(r.Int63()-r.Int63(), 10))
	case 3:
		return json.Number(strconv.FormatFloat(r.NormFloat64()*math.Pow(10, float64(r.Intn(40)-20)), 'g', -1, 64))
	case 4:
		runes := []rune("aZ09 \"\\/\n\t\x00\x1fé€😀 <>&")
		var sb strings.Builder
		for range r.Intn(8) {
			sb.WriteRune(runes[r.Intn(len(runes))])
		}
		return sb.String()
	case 5, 6:
		out := make([]any, r.Intn(5))
		for i := range out {
			out[i] = randomValue(r, depth+1)
		}
		return out
	default:
		out := map[string]any{}
		for range r.Intn(5) {
			out[fmt.Sprint("k", r.Intn(10))] = randomValue(r, depth+1)
		}
		return out
	}
}

// toAny converts the tree to what encoding/json decodes (with UseNumber).
func toAny(v *model.Value) any {
	switch v.Kind {
	case model.KindNull:
		return nil
	case model.KindBool:
		return v.Bool
	case model.KindInt:
		return v.Int.String()
	case model.KindFloat:
		return v.Float
	case model.KindString:
		return v.Str
	case model.KindList:
		out := make([]any, len(v.Children))
		for i, c := range v.Children {
			out[i] = toAny(c.Slot.Value)
		}
		return out
	default:
		out := map[string]any{}
		for _, c := range v.Children {
			out[c.Label] = toAny(c.Slot.Value)
		}
		return out
	}
}

// normalize makes what encoding/json decoded comparable with toAny.
func normalize(v any) any {
	switch x := v.(type) {
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			return string(x)
		}
		f, _ := strconv.ParseFloat(string(x), 64)
		return f
	case []any:
		for i := range x {
			x[i] = normalize(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = normalize(x[k])
		}
	}
	return v
}

func TestAgainstEncodingJSON(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := range 300 {
		v := randomValue(r, 0)
		var data []byte
		var err error
		switch i % 3 {
		case 0:
			data, err = json.Marshal(v)
		case 1:
			data, err = json.MarshalIndent(v, "  ", "\t")
		default:
			data, err = json.Marshal(v)
			data = bytes.ReplaceAll(data, []byte(","), []byte(" ,\n "))
		}
		if err != nil {
			t.Fatal(err)
		}
		d, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", data, err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		var want any
		if err := dec.Decode(&want); err != nil {
			t.Fatal(err)
		}
		want = normalize(want)
		got := toAny(d.Root.Value)
		if fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", want) {
			t.Fatalf("%s\ngot  %#v\nwant %#v", data, got, want)
		}
		if out, err := d.Patch(nil); err != nil || !bytes.Equal(out, data) {
			t.Fatalf("round trip of %s: %s, %v", data, out, err)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{`{"a":[1,2.5,"xé",null,true]}`, `[`, `"\ud800"`, "\xEF\xBB\xBF1", `-0.0e-0`, `{"a":1,"a":2}`, "\"\xff\""} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := Parse(data)
		body := bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
		if err != nil {
			// Whatever encoding/json accepts and is valid UTF-8 is accepted, unless too deep.
			if json.Valid(body) && isUTF8(body) && !strings.Contains(err.Error(), "deeply") {
				t.Fatalf("rejected valid JSON %q: %v", data, err)
			}
			return
		}
		if !json.Valid(body) {
			t.Fatalf("accepted invalid JSON %q", data)
		}
		if out, err := d.Patch(nil); err != nil || !bytes.Equal(out, data) {
			t.Fatalf("round trip of %q: %q, %v", data, out, err)
		}
	})
}

func isUTF8(b []byte) bool { return strings.ToValidUTF8(string(b), "") == string(b) }

func bigDocument(n int) []byte {
	var sb strings.Builder
	sb.WriteString(`{"items":[`)
	for i := range n {
		if i > 0 {
			sb.WriteString(",\n")
		}
		fmt.Fprintf(&sb, `{"id":%d,"name":"item \"%d\" é","price":%d.25,"tags":["a","b"],"ok":true,"none":null}`, i, i, i)
	}
	sb.WriteString("]}")
	return []byte(sb.String())
}

func TestParseSpeed(t *testing.T) {
	data := bigDocument(40000)
	if len(data) < 3<<20 {
		t.Fatalf("only %d bytes", len(data))
	}
	start := time.Now()
	d, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("parsing %d bytes took %v", len(data), took)
	}
	if d.SiteCount() != 40000*7 {
		t.Errorf("sites: %d", d.SiteCount())
	}
}

func BenchmarkParse(b *testing.B) {
	data := bigDocument(40000)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Parse(data); err != nil {
			b.Fatal(err)
		}
	}
}

// TestDamagedTextNeverPanics reads every prefix of a text with every kind of token, and the
// text with each byte changed in turn: an error or a document, never a panic.
func TestDamagedTextNeverPanics(t *testing.T) {
	text := []byte(`{"a":[1,-2.5e3,"xé😀\n",true,false,null,{}],"b":{"c":[[]],"d":"\\"}}`)
	read := func(b []byte) {
		d, err := Parse(b)
		if err != nil {
			return
		}
		if _, err := d.Patch(nil); err != nil {
			t.Errorf("Patch(nil): %v", err)
		}
	}
	for n := range len(text) {
		read(text[:n])
	}
	for i := range text {
		for _, x := range []byte{0x01, 0x5A, 0xFF} {
			damaged := append([]byte(nil), text...)
			damaged[i] ^= x
			read(damaged)
		}
	}
}
