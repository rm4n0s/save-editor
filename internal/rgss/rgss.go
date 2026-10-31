// Package rgss opens the save files of RPG Maker XP (*.rxdata), VX (*.rvdata) and VX Ace
// (*.rvdata2): the games of RGSS 1, 2 and 3.
//
// A save is a sequence of Ruby Marshal dumps, which package rubymarshal reads. XP and VX
// write one dump per game object, in the order of the default Scene_Save (XP) and Scene_File
// (VX) scripts; VX Ace writes two hashes, the header and the contents, from the default
// DataManager. The dumps are shown as variables named after the global each one comes from
// ($game_system, $game_switches, ...). A dump that a custom script appended is shown as
// "extra 1", "extra 2", and so on. Editing replaces only the bytes of the edited values.
//
// The order of the dumps is that of the default scripts:
//
//	XP  Scene_Save#write_save_data: characters, Graphics.frame_count, $game_system,
//	    $game_switches, $game_variables, $game_self_switches, $game_screen, $game_actors,
//	    $game_party, $game_troop, $game_map, $game_player
//	VX  Scene_File#write_save_data: characters, Graphics.frame_count, the last BGM, the
//	    last BGS, $game_system, $game_message, $game_switches, $game_variables,
//	    $game_self_switches, $game_actors, $game_party, $game_troop, $game_map, $game_player
//	VX Ace  DataManager.save_game_without_rescue: make_save_header, then make_save_contents
//	    (:system, :timer, :message, :switches, :variables, :self_switches, :actors, :party,
//	    :troop, :map, :player)
//
// (The VX order is from the Scene_File of the default scripts as scripters quote them; the
// XP order from the Scene_Save quoted in the same way; the Ace order from the default
// scripts of RGSS3, in the gem rgss3_default_scripts.)
package rgss

import (
	"path"
	"strings"

	"github.com/rm4n0s/save-editor/internal/model"
	"github.com/rm4n0s/save-editor/internal/rubymarshal"
)

// engineID tells the three engines apart.
type engineID uint8

const (
	engineXP engineID = iota
	engineVX
	engineVXAce
)

// engine is what differs between the engines.
type engine struct {
	id   engineID
	name string
	ext  string
	// dumps are the variables of the dumps of the default scripts, in order (XP and VX).
	dumps []string
	// frameRate is Graphics.frame_rate of a game that does not change it.
	frameRate int
	// strings is the encoding of a String that has no flag: Ruby 1.8 has none, and the
	// RGSS 1 and 2 games keep their text in UTF-8.
	strings string
}

var (
	engines = []*engine{
		{
			id: engineXP, name: "RPG Maker XP", ext: ".rxdata", frameRate: 40, strings: "UTF-8",
			dumps: []string{"characters", "Graphics.frame_count", "$game_system", "$game_switches",
				"$game_variables", "$game_self_switches", "$game_screen", "$game_actors", "$game_party",
				"$game_troop", "$game_map", "$game_player"},
		},
		{
			id: engineVX, name: "RPG Maker VX", ext: ".rvdata", frameRate: 60, strings: "UTF-8",
			dumps: []string{"characters", "Graphics.frame_count", "last_bgm", "last_bgs", "$game_system",
				"$game_message", "$game_switches", "$game_variables", "$game_self_switches", "$game_actors",
				"$game_party", "$game_troop", "$game_map", "$game_player"},
		},
		{id: engineVXAce, name: "RPG Maker VX Ace", ext: ".rvdata2", frameRate: 60},
	}

	// FormatXP opens the saves of RPG Maker XP.
	FormatXP = engines[engineXP].format()
	// FormatVX opens the saves of RPG Maker VX.
	FormatVX = engines[engineVX].format()
	// FormatVXAce opens the saves of RPG Maker VX Ace.
	FormatVXAce = engines[engineVXAce].format()

	// Formats are the three formats, in the order to try them.
	Formats = []model.Format{FormatXP, FormatVX, FormatVXAce}
)

func (e *engine) format() model.Format {
	return model.Format{
		Name:     e.name,
		Patterns: []string{"*" + e.ext},
		Detect:   e.detect,
		Parse:    e.parse,
	}
}

// detect looks at the structure of the first dumps, and at the extension only when the
// structure of a save cannot be told (a game whose scripts changed the order of the dumps).
func (e *engine) detect(name string, data []byte) bool {
	if len(data) < 2 || data[0] != 4 || data[1] > 8 {
		return false
	}
	doc, err := rubymarshal.Parse(data, rubymarshal.Options{DefaultEncoding: e.strings, MaxDumps: 6})
	if err != nil {
		return false
	}
	roots := make([]*model.Value, len(doc.Dumps))
	for i, d := range doc.Dumps {
		roots[i] = d.Root.Value
	}
	if id, ok := shape(roots); ok {
		return id == e.id
	}
	// The structure does not tell. The file name does, if what the engine always writes
	// first is there: the party of a save of XP or VX; the header hash of Ace.
	if !strings.EqualFold(path.Ext(name), e.ext) || len(roots) < 2 {
		return false
	}
	if e.id == engineVXAce {
		return roots[0].Kind == model.KindMap && roots[1].Kind == model.KindMap
	}
	return roots[0].Kind == model.KindList && roots[1].Kind == model.KindInt && len(roots) >= 3
}

// shape is the engine whose default scripts write dumps of this shape.
func shape(r []*model.Value) (engineID, bool) {
	isObject := func(i int, types ...string) bool {
		if i >= len(r) || r[i].Kind != model.KindObject {
			return false
		}
		for _, t := range types {
			if r[i].Type == t {
				return true
			}
		}
		return false
	}
	isNilOr := func(i int, types ...string) bool {
		return i < len(r) && (r[i].Kind == model.KindNull || isObject(i, types...))
	}
	if len(r) >= 2 && r[0].Kind == model.KindMap && r[1].Kind == model.KindMap &&
		hasKey(r[0], ":characters") && hasKey(r[1], ":system") {
		return engineVXAce, true
	}
	if len(r) >= 3 && r[0].Kind == model.KindList && r[1].Kind == model.KindInt {
		switch {
		case isObject(2, "Game_System"):
			return engineXP, true
		case isNilOr(2, "RPG::BGM") && isNilOr(3, "RPG::BGS") && isObject(4, "Game_System"):
			return engineVX, true
		}
	}
	return 0, false
}

func hasKey(v *model.Value, label string) bool {
	for _, c := range v.Children {
		if c.Label == label {
			return true
		}
	}
	return false
}
