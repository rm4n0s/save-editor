package rgss

import (
	"strconv"
	"strings"

	"github.com/rm4n0s/save-editor/internal/model"
	"github.com/rm4n0s/save-editor/internal/rubymarshal"
)

// Document is a parsed RGSS save.
type Document struct {
	engine  *engine
	doc     *rubymarshal.Doc
	vars    []model.Variable
	details []model.Detail
}

// internalDumps are the dumps that are the engine's rather than the game's: the party shown
// in the slot of the save list, and the state of the message window and of the audio.
var internalDumps = map[string]bool{
	"characters": true, "last_bgm": true, "last_bgs": true, "$game_message": true, "header": true,
}

// contentsKeys are the keys of the contents hash of VX Ace, by the global each comes from.
var contentsKeys = map[string]string{
	":system": "$game_system", ":timer": "$game_timer", ":message": "$game_message",
	":switches": "$game_switches", ":variables": "$game_variables", ":self_switches": "$game_self_switches",
	":actors": "$game_actors", ":party": "$game_party", ":troop": "$game_troop", ":map": "$game_map",
	":player": "$game_player",
}

func (e *engine) parse(_ string, data []byte) (model.Document, error) {
	opts := rubymarshal.Options{DefaultEncoding: e.strings}
	doc, err := rubymarshal.Parse(data, opts)
	if err != nil {
		return nil, err
	}
	d := &Document{engine: e, doc: doc}
	d.nameDumps()
	d.collectDetails()
	return d, nil
}

// nameDumps makes the variables.
func (d *Document) nameDumps() {
	used := map[string]bool{}
	add := func(name string, slot model.Slot) {
		for base, n := name, 2; used[name]; n++ {
			name = base + " #" + strconv.Itoa(n)
		}
		used[name] = true
		d.vars = append(d.vars, model.Variable{Name: name, Internal: internalDumps[name], Slot: slot})
	}
	known := len(d.engine.dumps)
	if d.engine.id == engineVXAce {
		known = 2
	}
	for i, dump := range d.doc.Dumps {
		switch {
		case i >= known:
			add("extra "+strconv.Itoa(i-known+1), dump.Root)
		case d.engine.id != engineVXAce:
			add(d.engine.dumps[i], dump.Root)
		case i == 0:
			add("header", dump.Root)
		case dump.Root.Value.Kind != model.KindMap:
			add("contents", dump.Root)
		default:
			for _, c := range dump.Root.Value.Children {
				name, ok := contentsKeys[c.Label]
				if !ok {
					name = "contents[" + c.Label + "]"
				}
				add(name, c.Slot)
			}
		}
	}
}

// Variables are the dumps, in the order of the file.
func (d *Document) Variables() []model.Variable { return d.vars }

// Details are the facts that the save header gives.
func (d *Document) Details() []model.Detail { return d.details }

// Encode is the file with the edits made; only the edited values change.
func (d *Document) Encode(edits []model.Edit) ([]byte, error) { return d.doc.Encode(edits) }

// variable is the value of the variable called name, or nil.
func (d *Document) variable(name string) *model.Value {
	for _, v := range d.vars {
		if v.Name == name {
			return v.Slot.Value
		}
	}
	return nil
}

// child is the part of v with the label, or nil.
func child(v *model.Value, label string) *model.Value {
	if v == nil {
		return nil
	}
	for _, c := range v.Children {
		if c.Label == label {
			return c.Slot.Value
		}
	}
	return nil
}

// collectDetails reads the header of the save: who is in the party, how long the game has
// been played, how many times it was saved.
func (d *Document) collectDetails() {
	d.details = append(d.details, model.Detail{Label: "Engine", Text: d.engine.name})
	var party, play *model.Value
	if d.engine.id == engineVXAce {
		header := d.variable("header")
		party, play = child(header, ":characters"), child(header, ":playtime_s")
		if play != nil && play.Kind == model.KindString {
			d.details = append(d.details, model.Detail{Label: "Play time", Text: play.Str,
				Tooltip: "playtime_s of the save header"})
		}
	} else {
		party, play = d.variable("characters"), d.variable("Graphics.frame_count")
		if play != nil && play.Kind == model.KindInt && play.Int.IsInt64() && play.Int.Int64() >= 0 {
			frames := play.Int.Int64()
			d.details = append(d.details, model.Detail{
				Label: "Play time",
				Text:  clock(frames / int64(d.engine.frameRate)),
				Tooltip: strconv.FormatInt(frames, 10) + " frames at " + strconv.Itoa(d.engine.frameRate) +
					" frames per second (the default of " + d.engine.name + "; a game can change it)",
			})
		}
	}
	if party != nil && party.Kind == model.KindList {
		var names, tips []string
		for _, c := range party.Children {
			member := c.Slot.Value
			if member.Kind != model.KindList || len(member.Children) < 2 {
				continue
			}
			graphic := member.Children[0].Slot.Value
			names = append(names, graphic.Str)
			tips = append(tips, graphic.Str+" ("+member.Children[1].Slot.Value.Repr()+")")
		}
		text := "none"
		if len(names) > 0 {
			text = strconv.Itoa(len(names)) + ": " + strings.Join(names, ", ")
		}
		d.details = append(d.details, model.Detail{Label: "Party", Text: text,
			Tooltip: "the character graphics of the save slot: " + strings.Join(tips, ", ")})
	}
	if n := child(d.variable("$game_system"), "@save_count"); n != nil && n.Kind == model.KindInt {
		d.details = append(d.details, model.Detail{Label: "Saved", Text: n.Int.String() + " times",
			Tooltip: "@save_count of $game_system"})
	}
	extra := len(d.vars)
	for i, v := range d.vars {
		if strings.HasPrefix(v.Name, "extra ") {
			extra = i
			break
		}
	}
	if n := len(d.vars) - extra; n > 0 {
		d.details = append(d.details, model.Detail{Label: "Extra data", Text: strconv.Itoa(n) +
			" more dump(s) written by the game's scripts"})
	}
}

// clock is a number of seconds as h:mm:ss.
func clock(secs int64) string {
	two := func(n int64) string {
		if n < 10 {
			return "0" + strconv.FormatInt(n, 10)
		}
		return strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(secs/3600, 10) + ":" + two(secs/60%60) + ":" + two(secs%60)
}
