package main

import (
	"context"
	"encoding/json"
	"math/big"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/pickle"
	"github.com/rm4n0s/save-editor/internal/renpysave"
)

func sampleVars(t *testing.T) []renpysave.Variable {
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
	list := &pickle.Value{Kind: pickle.KindList}
	list.Items = []pickle.Slot{{Value: list}}
	vars := []renpysave.Variable{{Name: "loop", Slot: pickle.Slot{Value: list}}}
	rows := buildRows(vars, "", false, map[string]bool{"loop": true, "loop\x00[0]": true})
	if len(rows) != 2 || !rows[1].recursive || rows[1].expandable {
		t.Errorf("rows %q; the list inside itself is not marked", labels(rows))
	}
}

func TestParseValue(t *testing.T) {
	tests := []struct {
		kind pickle.Kind
		text string
		want string
	}{
		{pickle.KindInt, " 42 ", "42"},
		{pickle.KindInt, "1_000_000", "1000000"},
		{pickle.KindInt, "-99999999999999999999999", "-99999999999999999999999"},
		{pickle.KindFloat, "2.5", "2.5"},
		{pickle.KindFloat, "1e3", "1000.0"},
		{pickle.KindString, " padded ", `" padded "`},
		{pickle.KindByteString, "py2", `"py2"`},
	}
	for _, tt := range tests {
		v, err := parseValue(tt.kind, tt.text)
		if err != nil {
			t.Errorf("parseValue(%v, %q): %v", tt.kind, tt.text, err)
			continue
		}
		if v.Kind != tt.kind || v.Repr() != tt.want {
			t.Errorf("parseValue(%v, %q) = %v %s, want %s", tt.kind, tt.text, v.Kind, v.Repr(), tt.want)
		}
	}
}

func TestParseValueFailures(t *testing.T) {
	tests := []struct {
		kind  pickle.Kind
		text  string
		route string
	}{
		{pickle.KindInt, "1.5", "parseValue.ValueNotInt"},
		{pickle.KindInt, "", "parseValue.ValueNotInt"},
		{pickle.KindFloat, "half", "parseValue.ValueNotFloat"},
		{pickle.KindNone, "x", "parseValue.ValueNotEditable"},
	}
	for _, tt := range tests {
		_, err := parseValue(tt.kind, tt.text)
		appErr, ok := errors.FromError(err)
		if !ok {
			t.Fatalf("parseValue(%v, %q): expected *errors.Error, got %T", tt.kind, tt.text, err)
		}
		if !appErr.HasRoute(tt.route) {
			t.Errorf("unexpected failure path: %s", appErr.Route())
		}
	}
}

func TestSameValue(t *testing.T) {
	i := func(n int64) *pickle.Value { return &pickle.Value{Kind: pickle.KindInt, Int: big.NewInt(n)} }
	tests := []struct {
		a, b *pickle.Value
		want bool
	}{
		{i(5), i(5), true},
		{i(5), i(6), false},
		{i(1), &pickle.Value{Kind: pickle.KindBool, Bool: true}, false},
		{&pickle.Value{Kind: pickle.KindFloat, Float: 0.5}, &pickle.Value{Kind: pickle.KindFloat, Float: 0.5}, true},
		{&pickle.Value{Kind: pickle.KindString, Str: "a"}, &pickle.Value{Kind: pickle.KindString, Str: "b"}, false},
		{&pickle.Value{Kind: pickle.KindNone}, &pickle.Value{Kind: pickle.KindNone}, true},
	}
	for _, tt := range tests {
		if got := sameValue(tt.a, tt.b); got != tt.want {
			t.Errorf("sameValue(%s, %s) = %v", tt.a.Repr(), tt.b.Repr(), got)
		}
	}
}

func TestMetadataText(t *testing.T) {
	tests := []struct {
		got, want string
	}{
		{metadataDuration(jsonNumber("3723.25")), "1:02:03"},
		{metadataDuration(jsonNumber("59")), "0:00:59"},
		{metadataDuration("not a number"), ""},
		{metadataTime(jsonNumber("0")), ""},
		{metadataString("1.0"), "1.0"},
		{metadataString(nil), ""},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%q, want %q", tt.got, tt.want)
		}
	}
}

func jsonNumber(s string) any { return json.Number(s) }
