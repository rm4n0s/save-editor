// Package rmtest is the test support of the packages that read Ruby Marshal data: a tree of
// values in a canonical form, and the Python package rubymarshal as a second reader whose tree
// in the same form is compared with it. It is only imported by tests.
package rmtest

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/save-editor/internal/model"
)

// Tree is the canonical form of the values of dumps, as JSON would give it back (numbers are
// float64), so it can be compared with DeepEqual to what check.py writes.
func Tree(t *testing.T, roots []*model.Value) []any {
	t.Helper()
	dumps := make([]any, 0, len(roots))
	for _, v := range roots {
		dumps = append(dumps, canon(v))
	}
	raw, err := json.Marshal(dumps)
	if err != nil {
		t.Fatal(err)
	}
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func canon(v *model.Value) any {
	switch v.Kind {
	case model.KindNull:
		return map[string]any{"k": "nil"}
	case model.KindBool:
		return map[string]any{"k": "bool", "v": v.Bool}
	case model.KindInt:
		return map[string]any{"k": "int", "v": v.Int.String()}
	case model.KindFloat:
		return map[string]any{"k": "float", "v": canonFloat(v.Float)}
	case model.KindString:
		if v.Type == "Symbol" {
			return map[string]any{"k": "sym", "v": v.Str}
		}
		return map[string]any{"k": "str", "v": v.Str}
	case model.KindBytes:
		if v.Type == "String" {
			return map[string]any{"k": "bytes", "hex": hex(v.Bytes)}
		}
		return map[string]any{"k": "u", "c": v.Type, "hex": hex(v.Bytes)}
	case model.KindList:
		items := []any{}
		for _, c := range v.Children {
			items = append(items, canon(c.Slot.Value))
		}
		return map[string]any{"k": "arr", "v": items}
	case model.KindMap:
		items := []any{}
		for _, c := range v.Children {
			items = append(items, []any{c.Label, canon(c.Slot.Value)})
		}
		return map[string]any{"k": "hash", "v": items}
	case model.KindObject:
		return canonObject(v)
	}
	return map[string]any{"k": "opaque", "v": v.Str}
}

func hex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

func canonFloat(f float64) any {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	return f
}

func canonObject(v *model.Value) any {
	field := func(i int) *model.Value { return v.Children[i].Slot.Value }
	switch v.Type {
	case "Table":
		dims := []any{field(0).Int.Int64(), field(1).Int.Int64(), field(2).Int.Int64()}
		cells := []any{}
		for i := 0; i+1 < len(field(3).Bytes); i += 2 {
			cells = append(cells, int64(int16(binary.LittleEndian.Uint16(field(3).Bytes[i:]))))
		}
		return map[string]any{"k": "u", "c": "Table", "dims": dims, "cells": cells}
	case "Color", "Tone":
		return map[string]any{"k": "u", "c": v.Type, "f": []any{canonFloat(field(0).Float),
			canonFloat(field(1).Float), canonFloat(field(2).Float), canonFloat(field(3).Float)}}
	case "Rect":
		return map[string]any{"k": "u", "c": "Rect", "i": []any{field(0).Int.Int64(), field(1).Int.Int64(),
			field(2).Int.Int64(), field(3).Int.Int64()}}
	}
	fields := []any{}
	for _, c := range v.Children {
		fields = append(fields, []any{c.Label, canon(c.Slot.Value)})
	}
	return map[string]any{"k": "obj", "c": v.Type, "v": fields}
}

// Python is a Python that has the package rubymarshal, or the test is skipped. The
// environment variable RUBYMARSHAL_PYTHON names a Python (such as the one of a venv where
// `pip install rubymarshal` was run); without it python3 is tried.
func Python(t *testing.T) string {
	t.Helper()
	py := os.Getenv("RUBYMARSHAL_PYTHON")
	if py == "" {
		py = "python3"
	}
	path, err := exec.LookPath(py)
	if err != nil {
		t.Skipf("no %s: the reference reader is not available", py)
	}
	if out, err := exec.Command(path, "-I", "-c", "import rubymarshal").CombinedOutput(); err != nil {
		t.Skipf("the Python package rubymarshal is not installed (set RUBYMARSHAL_PYTHON to a Python that has it): %s", out)
	}
	return path
}

// PythonTrees reads the files with the Python package rubymarshal and gives their dumps in
// the form of Tree, by file name as it was given.
func PythonTrees(t *testing.T, py string, files ...string) map[string][]any {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find check.py")
	}
	script := filepath.Join(filepath.Dir(here), "..", "testdata", "check.py")
	out, err := exec.Command(py, append([]string{"-I", script}, files...)...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("check.py: %v\n%s", err, ee.Stderr)
		}
		t.Fatal(err)
	}
	var trees map[string][]any
	if err := json.Unmarshal(out, &trees); err != nil {
		t.Fatal(err)
	}
	return trees
}
