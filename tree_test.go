package main

import (
	"context"
	stderrors "errors"
	"math/big"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
	"github.com/rm4n0s/save-editor/internal/renpysave"
)

func sampleVars(t *testing.T) []model.Variable {
	t.Helper()
	s, err := renpysave.Open(context.Background(), filepath.Join(fixtures, "renpy8.save"))
	if err != nil {
		t.Fatal(err)
	}
	return s.Variables()
}

func labels(rows []row) string {
	var out []string
	for _, r := range rows {
		out = append(out, strings.Repeat(" ", r.depth)+r.label)
	}
	return strings.Join(out, ",")
}

func TestBuildRows(t *testing.T) {
	vars := sampleVars(t)
	tests := []struct {
		name     string
		filter   string
		internal bool
		expanded map[string]bool
		want     string
	}{
		{"a name", "MON", false, nil, "money"},
		{"a value", "eileen", false, nil, ""},
		{"a primitive value", "annann", false, nil, "nickname,player_name"},
		{"internal hidden", "_int", false, nil, ""},
		{"internal shown", "_int", true, nil, "_internal"},
		{"expanded", "inv", false, map[string]bool{"inventory": true}, "inventory, [0], [1], [2]"},
		{"an empty dict does not expand", "", false, map[string]bool{"e": true, "e\x00.what_args": true},
			"big,e, .name, .what_args,flag_off,greeting,has_key,hero,inventory,money,neg,nickname,nothing,pair,player_name,ratio,seen,stats"},
		{"an object", "hero", false, map[string]bool{"hero": true}, "hero, .name, .hp"},
		{"a set", "seen", false, map[string]bool{"seen": true}, "seen, [0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRows(vars, tt.filter, tt.internal, tt.expanded)
			if labels(got) != tt.want {
				t.Errorf("rows %q, want %q", labels(got), tt.want)
			}
		})
	}
}

func TestBuildRowsStopsAtRecursion(t *testing.T) {
	list := &model.Value{Kind: model.KindList}
	list.Children = []model.Child{{Label: "[0]", Slot: model.Slot{Value: list}}}
	vars := []model.Variable{{Name: "loop", Slot: model.Slot{Value: list}}}
	rows := buildRows(vars, "", false, map[string]bool{"loop": true, "loop\x00[0]": true})
	if len(rows) != 2 || !rows[1].recursive || rows[1].expandable {
		t.Errorf("rows %q; the list inside itself is not marked", labels(rows))
	}
}

func intValue(n int64) *model.Value { return &model.Value{Kind: model.KindInt, Int: big.NewInt(n)} }

func bounded(min, max int64) *model.Site {
	return &model.Site{ID: 1, IntMin: big.NewInt(min), IntMax: big.NewInt(max)}
}

func TestParseValue(t *testing.T) {
	noCaps := func(text string) error {
		if strings.ContainsAny(text, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			return errors.New("TestCaps", "Use only lower case letters")
		}
		return nil
	}
	tests := []struct {
		name string
		slot model.Slot
		text string
		want string
	}{
		{"an int", model.Slot{Value: intValue(1), Site: &model.Site{}}, " 42 ", "42"},
		{"an int with underscores", model.Slot{Value: intValue(1), Site: &model.Site{}}, "1_000_000", "1000000"},
		{"an int beyond 64 bits", model.Slot{Value: intValue(1), Site: &model.Site{}}, "-99999999999999999999999", "-99999999999999999999999"},
		{"an int at the lowest bound", model.Slot{Value: intValue(1), Site: bounded(-128, 127)}, "-128", "-128"},
		{"an int at the highest bound", model.Slot{Value: intValue(1), Site: bounded(-128, 127)}, "127", "127"},
		{"a float", model.Slot{Value: &model.Value{Kind: model.KindFloat}, Site: &model.Site{}}, "2.5", "2.5"},
		{"a float in exponent form", model.Slot{Value: &model.Value{Kind: model.KindFloat}, Site: &model.Site{}}, "1e3", "1000.0"},
		{"a float32 is rounded to single precision", model.Slot{Value: &model.Value{Kind: model.KindFloat, Float32: true}, Site: &model.Site{}},
			"0.1", "0.1"},
		{"a float32 that float64 cannot hold exactly", model.Slot{Value: &model.Value{Kind: model.KindFloat, Float32: true}, Site: &model.Site{}},
			"3.4e38", "3.4e+38"},
		{"a string", model.Slot{Value: &model.Value{Kind: model.KindString}, Site: &model.Site{}}, " padded ", `" padded "`},
		{"a string the site accepts", model.Slot{Value: &model.Value{Kind: model.KindString}, Site: &model.Site{CheckText: noCaps}}, "fine", `"fine"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := parseValue(tt.slot, tt.text)
			if err != nil {
				t.Fatalf("parseValue(%q): %v", tt.text, err)
			}
			if v.Kind != tt.slot.Value.Kind || v.Float32 != tt.slot.Value.Float32 || v.Repr() != tt.want {
				t.Errorf("parseValue(%q) = %v %s, want %s", tt.text, v.Kind, v.Repr(), tt.want)
			}
		})
	}
	// A float32 holds exactly the float32 that was typed.
	v, err := parseValue(model.Slot{Value: &model.Value{Kind: model.KindFloat, Float32: true}, Site: &model.Site{}}, "0.1")
	if err != nil || v.Float != float64(float32(0.1)) {
		t.Errorf("0.1 as float32 = %v, %v", v.Float, err)
	}
}

func TestParseValueFailures(t *testing.T) {
	noCaps := func(text string) error {
		if strings.ContainsAny(text, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			return errors.New("TestCaps", "Use only lower case letters")
		}
		return nil
	}
	plainCheck := func(string) error { return stderrors.New("Use only Latin letters") }
	free := &model.Site{}
	tests := []struct {
		name    string
		slot    model.Slot
		text    string
		route   string
		message string
	}{
		{"text for an int", model.Slot{Value: intValue(1), Site: free}, "1.5", "parseValue.ValueNotInt", "Enter a whole number"},
		{"nothing for an int", model.Slot{Value: intValue(1), Site: free}, "", "parseValue.ValueNotInt", "Enter a whole number"},
		{"text for a bounded int", model.Slot{Value: intValue(1), Site: bounded(0, 255)}, "x", "parseValue.ValueNotInt", "Enter a whole number from 0 to 255"},
		{"an int below the bound", model.Slot{Value: intValue(1), Site: bounded(0, 255)}, "-1", "parseValue.ValueOutOfRange", "Enter a whole number from 0 to 255"},
		{"an int above the bound", model.Slot{Value: intValue(1), Site: bounded(0, 255)}, "256", "parseValue.ValueOutOfRange", "Enter a whole number from 0 to 255"},
		{"an int below a lower bound only", model.Slot{Value: intValue(1), Site: &model.Site{IntMin: big.NewInt(0)}}, "-5",
			"parseValue.ValueOutOfRange", "Enter a whole number of 0 or more"},
		{"an int above an upper bound only", model.Slot{Value: intValue(1), Site: &model.Site{IntMax: big.NewInt(9)}}, "10",
			"parseValue.ValueOutOfRange", "Enter a whole number of 9 or less"},
		{"text for a float", model.Slot{Value: &model.Value{Kind: model.KindFloat}, Site: free}, "half", "parseValue.ValueNotFloat", "Enter a number"},
		{"a float64 out of range", model.Slot{Value: &model.Value{Kind: model.KindFloat}, Site: free}, "1e999",
			"parseValue.ValueOutOfRange", "This number is too large or too small"},
		{"a float32 out of range", model.Slot{Value: &model.Value{Kind: model.KindFloat, Float32: true}, Site: free}, "1e39",
			"parseValue.ValueOutOfRange", "This number is too large or too small"},
		{"a text the site refuses", model.Slot{Value: &model.Value{Kind: model.KindString}, Site: &model.Site{CheckText: noCaps}}, "ABC",
			"TestCaps", "Use only lower case letters"},
		{"a text refused with a plain error", model.Slot{Value: &model.Value{Kind: model.KindString}, Site: &model.Site{CheckText: plainCheck}}, "ω",
			"parseValue.ValueTextRefused", "Use only Latin letters"},
		{"a null", model.Slot{Value: &model.Value{Kind: model.KindNull}, Site: free}, "x", "parseValue.ValueNotEditable", "This value cannot be edited"},
		{"a list", model.Slot{Value: &model.Value{Kind: model.KindList}, Site: free}, "x", "parseValue.ValueNotEditable", "This value cannot be edited"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseValue(tt.slot, tt.text)
			appErr, ok := errors.FromError(err)
			if !ok {
				t.Fatalf("expected *errors.Error, got %T: %v", err, err)
			}
			if !appErr.HasRoute(tt.route) {
				t.Errorf("unexpected failure path: %s", appErr.Route())
			}
			if appErr.Message != tt.message {
				t.Errorf("message %q, want %q", appErr.Message, tt.message)
			}
		})
	}
}

func TestSameValue(t *testing.T) {
	tests := []struct {
		a, b *model.Value
		want bool
	}{
		{intValue(5), intValue(5), true},
		{intValue(5), intValue(6), false},
		{intValue(0), &model.Value{Kind: model.KindInt}, true},
		{intValue(1), &model.Value{Kind: model.KindBool, Bool: true}, false},
		{&model.Value{Kind: model.KindFloat, Float: 0.5}, &model.Value{Kind: model.KindFloat, Float: 0.5}, true},
		{&model.Value{Kind: model.KindString, Str: "a"}, &model.Value{Kind: model.KindString, Str: "b"}, false},
		{&model.Value{Kind: model.KindString, Str: "a"}, &model.Value{Kind: model.KindString, Str: "a"}, true},
		{&model.Value{Kind: model.KindBool, Bool: true}, &model.Value{Kind: model.KindBool, Bool: true}, true},
		{&model.Value{Kind: model.KindNull}, &model.Value{Kind: model.KindNull}, true},
	}
	for _, tt := range tests {
		if got := sameValue(tt.a, tt.b); got != tt.want {
			t.Errorf("sameValue(%s, %s) = %v", tt.a.Repr(), tt.b.Repr(), got)
		}
	}
}

func TestEditable(t *testing.T) {
	site := &model.Site{}
	tests := []struct {
		name string
		slot model.Slot
		want bool
	}{
		{"an int with a site", model.Slot{Value: intValue(1), Site: site}, true},
		{"a bool with a site", model.Slot{Value: &model.Value{Kind: model.KindBool}, Site: site}, true},
		{"an int without a site", model.Slot{Value: intValue(1)}, false},
		{"a null with a site", model.Slot{Value: &model.Value{Kind: model.KindNull}, Site: site}, false},
		{"bytes with a site", model.Slot{Value: &model.Value{Kind: model.KindBytes}, Site: site}, false},
		{"a list with a site", model.Slot{Value: &model.Value{Kind: model.KindList}, Site: site}, false},
	}
	for _, tt := range tests {
		if got := editable(tt.slot); got != tt.want {
			t.Errorf("%s: editable = %v", tt.name, got)
		}
	}
}

func TestEditText(t *testing.T) {
	tests := []struct {
		v    *model.Value
		want string
	}{
		{intValue(-7), "-7"},
		{&model.Value{Kind: model.KindInt}, "0"},
		{&model.Value{Kind: model.KindFloat, Float: 2}, "2.0"},
		{&model.Value{Kind: model.KindFloat, Float: float64(float32(0.1)), Float32: true}, "0.1"},
		{&model.Value{Kind: model.KindString, Str: "a b"}, "a b"},
	}
	for _, tt := range tests {
		if got := editText(tt.v); got != tt.want {
			t.Errorf("editText = %q, want %q", got, tt.want)
		}
	}
}
