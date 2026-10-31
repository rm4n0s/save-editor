package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/glutter/desktop"
	"github.com/rm4n0s/save-editor/internal/gvas"
	"github.com/rm4n0s/save-editor/internal/lcf"
	"github.com/rm4n0s/save-editor/internal/model"
	"github.com/rm4n0s/save-editor/internal/nrbf"
	"github.com/rm4n0s/save-editor/internal/renpysave"
	"github.com/rm4n0s/save-editor/internal/rgss"
	"github.com/rm4n0s/save-editor/internal/rpgmv"
	"github.com/rm4n0s/save-editor/internal/unityjson"
	"github.com/rm4n0s/save-editor/internal/unityprefs"
)

// supportedFormats are the formats of save that the editor opens, in the order detection
// tries them.
func supportedFormats() []model.Format {
	formats := []model.Format{
		renpysave.Format,
		gvas.Format,
		lcf.Format,
	}
	formats = append(formats, rgss.Formats...)
	formats = append(formats, rpgmv.FormatMV, rpgmv.FormatMZ)
	formats = append(formats, unityprefs.Format)
	formats = append(formats, nrbf.Format)
	// Plain JSON detects last: the formats above are more specific than any JSON text.
	return append(formats, unityjson.FormatES3, unityjson.FormatJSON)
}

// detectFormat is the first of formats that says the file called name with the contents data
// is of its kind.
func detectFormat(formats []model.Format, name string, data []byte) (model.Format, error) {
	for _, f := range formats {
		if f.Detect(name, data) {
			return f, nil
		}
	}
	return model.Format{}, errors.New(tagFormatUnknown, "This file is not a save of a supported engine", "name", name)
}

// openDocument reads the file at path and parses it with the format that detects it.
func openDocument(ctx context.Context, formats []model.Format, path string) (model.Document, model.Format, error) {
	if err := ctx.Err(); err != nil {
		return nil, model.Format{}, errors.NewErr(tagFileUnreadable, err).SetMetadata("path", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, model.Format{}, errors.NewErr(tagFileUnreadable, err).SetMetadata("path", path)
	}
	name := filepath.Base(path)
	format, err := detectFormat(formats, name, data)
	if err != nil {
		return nil, model.Format{}, err
	}
	doc, err := format.Parse(name, data)
	if err != nil {
		return nil, model.Format{}, err
	}
	return doc, format, nil
}

// formatNames are the names of the formats, such as "Ren'Py, RPG Maker MV".
func formatNames(formats []model.Format) string {
	names := make([]string, len(formats))
	for i, f := range formats {
		names[i] = f.Name
	}
	return strings.Join(names, ", ")
}

// dialogPatterns are the patterns of a format for a file dialog, which matches the case of
// the letters: each pattern comes with its upper case twin.
func dialogPatterns(patterns []string) []string {
	var out []string
	add := func(p string) {
		for _, have := range out {
			if have == p {
				return
			}
		}
		out = append(out, p)
	}
	for _, p := range patterns {
		add(p)
		add(strings.ToUpper(p))
	}
	return out
}

// openFilters are the filters of the dialog that opens a save: all the supported saves, one
// for each format, and all files.
func openFilters(formats []model.Format) []desktop.FileFilter {
	var all []string
	filters := make([]desktop.FileFilter, 0, len(formats)+2)
	for _, f := range formats {
		patterns := dialogPatterns(f.Patterns)
		all = append(all, patterns...)
		filters = append(filters, desktop.FileFilter{Name: f.Name + " saves", Patterns: patterns})
	}
	filters = append([]desktop.FileFilter{{Name: "All supported saves", Patterns: dialogPatterns(all)}}, filters...)
	return append(filters, desktop.FileFilter{Name: "All files", Patterns: []string{"*"}})
}

// saveFilters are the filters of the dialog that saves a file of the format.
func saveFilters(f model.Format) []desktop.FileFilter {
	return []desktop.FileFilter{{Name: f.Name + " saves", Patterns: dialogPatterns(f.Patterns)}}
}

// The tags of the errors that this file makes.
const (
	tagFileUnreadable = "FileUnreadable"
	tagFormatUnknown  = "FormatUnknown"
)
