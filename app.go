package main

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rm4n0s/errors"
	"github.com/rm4n0s/glutter/desktop"
	m "github.com/rm4n0s/glutter/material"
	"github.com/rm4n0s/glutter/services"
	"github.com/rm4n0s/save-editor/internal/model"
	"github.com/rm4n0s/save-editor/internal/renpysave"
)

// EditorApp is the Material app around the editor.
type EditorApp struct {
	m.StatelessWidget
	path     string
	keysPath string
	formats  []model.Format
}

// NewEditorApp creates the app. path is a save to open at the start (none when empty), and
// keysPath the Ren'Py security_keys.txt to sign Ren'Py saves with when it exists.
func NewEditorApp(path, keysPath string) *EditorApp {
	return newEditorApp(path, keysPath, supportedFormats())
}

// newEditorApp is NewEditorApp with the formats it opens.
func newEditorApp(path, keysPath string, formats []model.Format) *EditorApp {
	return &EditorApp{path: path, keysPath: keysPath, formats: formats}
}

// Build builds the app.
func (a *EditorApp) Build(m.BuildContext) m.Widget {
	seed := m.Some(m.Colors.DeepPurple.Primary)
	return m.NewMaterialApp(m.MaterialAppOpts{
		Title:                      "Save Editor",
		Theme:                      m.NewThemeData(m.ThemeDataOpts{ColorSchemeSeed: seed}),
		DarkTheme:                  m.NewThemeData(m.ThemeDataOpts{ColorSchemeSeed: seed, Brightness: m.BrightnessDark}),
		DebugShowCheckedModeBanner: m.Some(false),
		Home:                       newEditorPage(a.path, a.keysPath, a.formats),
	})
}

// EditorPage shows a save: its screenshot and details on the left, its variables on the
// right.
type EditorPage struct {
	m.StatefulWidget
	path     string
	keysPath string
	formats  []model.Format
}

// NewEditorPage creates the page; see NewEditorApp for the paths.
func NewEditorPage(path, keysPath string, opts ...m.WidgetOpts) *EditorPage {
	return newEditorPage(path, keysPath, supportedFormats(), opts...)
}

// newEditorPage is NewEditorPage with the formats it opens.
func newEditorPage(path, keysPath string, formats []model.Format, opts ...m.WidgetOpts) *EditorPage {
	return &EditorPage{StatefulWidget: m.NewStatefulWidgetBase(opts...), path: path, keysPath: keysPath, formats: formats}
}

// CreateState creates the state.
func (p *EditorPage) CreateState() m.State { return &editorState{} }

// edit is a new value typed or switched for a slot. value is nil while the text is not a
// value of the slot's type, and err says why.
type edit struct {
	slot  model.Slot
	text  string
	value *model.Value
	err   string
}

type editorState struct {
	m.StateOf[*EditorPage]

	path   string
	doc    model.Document
	format model.Format
	vars   []model.Variable
	// renpy is doc when it is a Ren'Py save, which has a name and signatures that other
	// formats do not; nil otherwise.
	renpy        *renpysave.Document
	hasInternals bool

	// edits are by the ID of the slot's site, so a slot shown twice has one edit.
	edits       map[int]*edit
	fields      map[int]*m.TextEditingController
	expanded    map[string]bool
	filter      *m.TextEditingController
	saveName    *m.TextEditingController
	showHidden  bool
	keys        *renpysave.KeyRing
	keysPath    string
	sign        bool
	busy        bool
	status      string
	statusError bool
}

func (s *editorState) InitState() {
	s.edits = map[int]*edit{}
	s.fields = map[int]*m.TextEditingController{}
	s.expanded = map[string]bool{}
	s.filter = m.NewTextEditingController()
	s.saveName = m.NewTextEditingController()
	s.status = "Open a save file to edit it."
	if path := s.Widget().keysPath; path != "" {
		s.loadKeys(path)
	}
	if path := s.Widget().path; path != "" {
		s.open(path)
	}
}

func (s *editorState) Dispose() {
	s.filter.Dispose()
	s.saveName.Dispose()
	for _, c := range s.fields {
		c.Dispose()
	}
}

func (s *editorState) setStatus(text string, isError bool) {
	s.SetState(func() { s.status, s.statusError, s.busy = text, isError, false })
}

// fail shows an error in the status bar.
func (s *editorState) fail(what string, err error) {
	msg := err.Error()
	if appErr, ok := errors.FromError(err); ok && appErr.OriginalErr != nil {
		msg = appErr.OriginalErr.Error()
	}
	s.setStatus(what+": "+msg, true)
}

// changes is how many values and names will change on saving, and whether every new value
// is valid.
func (s *editorState) changes() (int, bool) {
	n, valid := 0, true
	for _, e := range s.edits {
		if e.value == nil {
			valid = false
			continue
		}
		n++
	}
	if s.renpy != nil && s.saveName.Text() != s.renpy.SaveName() {
		n++
	}
	return n, valid
}

// open reads the save at path on a goroutine and shows it.
func (s *editorState) open(path string) {
	s.Go(func(ctx context.Context) { s.openIn(ctx, path) })
}

// openIn reads the save at path and shows it. Call it on a goroutine of the state.
func (s *editorState) openIn(ctx context.Context, path string) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	s.SetState(func() { s.busy = true })
	doc, format, err := openDocument(ctx, s.Widget().formats, path)
	if err != nil {
		s.fail("Cannot open "+filepath.Base(path), err)
		return
	}
	s.SetState(func() {
		s.show(path, doc, format)
		s.status, s.statusError = fmt.Sprintf("Opened %s: %d variables.", filepath.Base(path), len(s.vars)), false
	})
}

// show replaces the save on the page. Call it inside SetState.
func (s *editorState) show(path string, doc model.Document, format model.Format) {
	s.path, s.doc, s.format, s.vars, s.busy = path, doc, format, doc.Variables(), false
	s.renpy, _ = doc.(*renpysave.Document)
	s.hasInternals = false
	for _, v := range s.vars {
		s.hasInternals = s.hasInternals || v.Internal
	}
	s.showHidden = s.showHidden && s.hasInternals
	s.edits = map[int]*edit{}
	s.expanded = map[string]bool{}
	if s.renpy != nil {
		s.saveName.SetText(s.renpy.SaveName())
	}
	// The fields of the old save are still in the tree until this frame is built.
	old := s.fields
	s.fields = map[int]*m.TextEditingController{}
	m.WidgetsBindingOf(s.Context()).Scheduler().AddPostFrameCallback(func(time.Duration) {
		for _, c := range old {
			c.Dispose()
		}
	})
}

func (s *editorState) chooseSave() {
	bc := s.Context()
	start := renpyFolder()
	if s.path != "" {
		start = filepath.Dir(s.path)
	}
	s.Go(func(ctx context.Context) {
		res, err := desktop.OpenFiles(ctx, bc, desktop.OpenFilesOpts{
			Title:         "Open a save",
			Filters:       openFilters(s.Widget().formats),
			CurrentFolder: existingFolder(start),
		})
		switch {
		case err != nil:
			s.fail("The file dialog failed", err)
		case res.Cancelled || len(res.Paths) == 0:
		default:
			s.openIn(ctx, res.Paths[0])
		}
	})
}

// loadKeys reads a security_keys.txt on a goroutine.
func (s *editorState) loadKeys(path string) {
	s.Go(func(ctx context.Context) { s.loadKeysIn(ctx, path, false) })
}

// loadKeysIn reads a security_keys.txt and signs with it from now on. A missing file is
// quiet unless the user chose it. Call it on a goroutine of the state.
func (s *editorState) loadKeysIn(ctx context.Context, path string, chosen bool) {
	keys, err := renpysave.LoadKeyRing(ctx, path)
	if err != nil {
		if chosen || !errors.Is(err, fs.ErrNotExist) {
			s.fail("Cannot use the keys of "+path, err)
		}
		return
	}
	s.SetState(func() {
		s.keys, s.keysPath, s.sign = keys, path, keys.CanSign()
		if chosen {
			s.status, s.statusError = "Using the keys of "+path+".", false
		}
	})
}

func (s *editorState) chooseKeys() {
	bc := s.Context()
	s.Go(func(ctx context.Context) {
		res, err := desktop.OpenFiles(ctx, bc, desktop.OpenFilesOpts{
			Title:         "Choose your Ren'Py " + renpysave.KeysFileName,
			Filters:       []desktop.FileFilter{{Name: "Ren'Py keys", Patterns: []string{renpysave.KeysFileName}}, {Name: "All files", Patterns: []string{"*"}}},
			CurrentFolder: existingFolder(filepath.Join(renpyFolder(), "tokens")),
		})
		switch {
		case err != nil:
			s.fail("The file dialog failed", err)
		case res.Cancelled || len(res.Paths) == 0:
		default:
			s.loadKeysIn(ctx, res.Paths[0], true)
		}
	})
}

// changeSet is the changes of the page, to give to Encode.
type changeSet struct {
	edits []model.Edit
	// saveName and keys are Ren'Py's: the new name of the save, and the key that signs it.
	saveName *string
	keys     *renpysave.KeyRing
}

// pending is the changes of the page, or false with the status saying why there are none to
// save.
func (s *editorState) pending() (changeSet, bool) {
	if s.doc == nil {
		return changeSet{}, false
	}
	if _, valid := s.changes(); !valid {
		s.setStatus("Fix the values marked in red before saving.", true)
		return changeSet{}, false
	}
	var cs changeSet
	for _, e := range s.edits {
		cs.edits = append(cs.edits, model.Edit{Site: e.slot.Site, Value: e.value})
	}
	slices.SortFunc(cs.edits, func(a, b model.Edit) int { return a.Site.ID - b.Site.ID })
	if s.renpy != nil {
		if name := s.saveName.Text(); name != s.renpy.SaveName() {
			cs.saveName = &name
		}
		if s.sign && s.keys != nil {
			cs.keys = s.keys
		}
	}
	return cs, true
}

// encode is the file with the changes.
func (s *editorState) encode(doc model.Document, cs changeSet) ([]byte, error) {
	if rd, ok := doc.(*renpysave.Document); ok {
		return rd.EncodeWith(renpysave.EncodeOpts{Edits: cs.edits, SaveName: cs.saveName, Keys: cs.keys})
	}
	return doc.Encode(cs.edits)
}

// partsWord is "part" for one part of an object and "parts" for any other number, the wording
// of model.Value.Repr.
func partsWord(n int) string {
	if n == 1 {
		return "part"
	}
	return "parts"
}

// write saves the page to path (asking where first when saveAs is set) on a goroutine,
// then shows the save as written.
func (s *editorState) write(saveAs bool) {
	cs, ok := s.pending()
	if !ok {
		return
	}
	doc, format, path, bc := s.doc, s.format, s.path, s.Context()
	s.SetState(func() { s.busy = true })
	s.Go(func(ctx context.Context) {
		backup := true
		if saveAs {
			res, err := desktop.SaveFile(ctx, bc, desktop.SaveFileOpts{
				Title:         "Save as",
				Filters:       saveFilters(format),
				CurrentFolder: filepath.Dir(path),
				CurrentName:   filepath.Base(path),
			})
			if err != nil {
				s.fail("The file dialog failed", err)
				return
			}
			if res.Cancelled || len(res.Paths) == 0 {
				s.setStatus("Not saved.", false)
				return
			}
			path, backup = res.Paths[0], false
		}
		data, err := s.encode(doc, cs)
		if err != nil {
			s.fail("Cannot save", err)
			return
		}
		saved, err := format.Parse(filepath.Base(path), data)
		if err != nil {
			s.fail("The new save does not read back; nothing was written", err)
			return
		}
		if err := model.WriteFile(ctx, path, data, backup); err != nil {
			s.fail("Cannot write "+filepath.Base(path), err)
			return
		}
		msg := "Saved " + filepath.Base(path)
		before, _ := doc.(*renpysave.Document)
		after, _ := saved.(*renpysave.Document)
		switch {
		case cs.keys != nil:
			msg += ", signed with your key."
		case before != nil && after != nil && after.Signatures == "" && before.Signatures != "":
			msg += " without signatures: Ren'Py will ask once before loading it."
		default:
			msg += "."
		}
		if backup {
			msg += " The first version is kept in " + filepath.Base(path) + ".bak."
		}
		s.SetState(func() {
			s.show(path, saved, format)
			s.status, s.statusError = msg, false
		})
	})
}

// setText records the text of a field as the new value of its slot.
func (s *editorState) setText(slot model.Slot, text string) {
	s.SetState(func() {
		v, err := parseValue(slot, text)
		switch {
		case err != nil:
			msg := err.Error()
			if appErr, ok := errors.FromError(err); ok {
				msg = appErr.Message
			}
			s.edits[slot.Site.ID] = &edit{slot: slot, text: text, err: msg}
		case sameValue(v, slot.Value):
			delete(s.edits, slot.Site.ID)
		default:
			s.edits[slot.Site.ID] = &edit{slot: slot, text: text, value: v}
		}
	})
}

func (s *editorState) setBool(slot model.Slot, b bool) {
	s.SetState(func() {
		if b == slot.Value.Bool {
			delete(s.edits, slot.Site.ID)
			return
		}
		s.edits[slot.Site.ID] = &edit{slot: slot, value: &model.Value{Kind: model.KindBool, Type: slot.Value.Type, Bool: b}}
	})
}

// reset gives the slot its value from the file again.
func (s *editorState) reset(slot model.Slot) {
	s.SetState(func() {
		delete(s.edits, slot.Site.ID)
		if c, ok := s.fields[slot.Site.ID]; ok {
			c.SetText(editText(slot.Value))
		}
	})
}

func (s *editorState) discard() {
	s.SetState(func() {
		for _, e := range s.edits {
			if c, ok := s.fields[e.slot.Site.ID]; ok {
				c.SetText(editText(e.slot.Value))
			}
		}
		s.edits = map[int]*edit{}
		if s.renpy != nil {
			s.saveName.SetText(s.renpy.SaveName())
		}
		s.status, s.statusError = "Changes discarded.", false
	})
}

func (s *editorState) toggle(id string) {
	s.SetState(func() { s.expanded[id] = !s.expanded[id] })
}

// field is the controller of the text field of a slot, made the first time it is shown.
func (s *editorState) field(slot model.Slot) *m.TextEditingController {
	c, ok := s.fields[slot.Site.ID]
	if !ok {
		text := editText(slot.Value)
		if e, ok := s.edits[slot.Site.ID]; ok {
			text = e.text
		}
		c = m.NewTextEditingController(text)
		s.fields[slot.Site.ID] = c
	}
	return c
}

func (s *editorState) Build(bc m.BuildContext) m.Widget {
	hasSave := s.doc != nil
	var whenSaved func()
	var whenSavedAs func()
	if hasSave && !s.busy {
		whenSaved = func() { s.write(false) }
		whenSavedAs = func() { s.write(true) }
	}
	var body m.Widget
	if hasSave {
		body = m.NewRow([]m.Widget{
			m.NewSizedBox(s.buildDetails(bc), m.SizedBoxOpts{Width: m.Some(320.0)}),
			m.NewVerticalDivider(m.VerticalDividerOpts{Width: m.Some(1.0)}),
			m.NewExpanded(s.buildVariables(bc)),
		}, m.RowOpts{CrossAxisAlignment: m.CrossAxisAlignmentStretch})
	} else {
		body = s.buildEmpty(bc)
	}
	children := []m.Widget{}
	if s.busy {
		children = append(children, m.NewLinearProgressIndicator())
	}
	children = append(children, m.NewExpanded(body), s.buildStatus(bc))

	page := m.NewScaffold(m.ScaffoldOpts{
		AppBar: m.NewAppBar(m.AppBarOpts{
			Title: m.NewText(s.title()),
			Actions: []m.Widget{
				m.NewIconButton(s.chooseSave, m.NewIcon(m.Icons.FolderOpen()), m.IconButtonOpts{Tooltip: "Open a save (Ctrl+O)"}),
				m.NewIconButton(whenSaved, m.NewIcon(m.Icons.Save()), m.IconButtonOpts{Tooltip: "Save (Ctrl+S)"}),
				m.NewIconButton(whenSavedAs, m.NewIcon(m.Icons.SaveAs()), m.IconButtonOpts{Tooltip: "Save as (Ctrl+Shift+S)"}),
				m.NewSizedBox(nil, m.SizedBoxOpts{Width: m.Some(8.0)}),
			},
		}),
		Body: m.NewColumn(children, m.ColumnOpts{CrossAxisAlignment: m.CrossAxisAlignmentStretch}),
	})
	return m.NewCallbackShortcuts([]m.CallbackShortcutEntry{
		{Activator: m.NewSingleActivator(services.LogicalKeyboardKeyKeyO, m.SingleActivatorOpts{Control: true}), Callback: s.chooseSave},
		{Activator: m.NewSingleActivator(services.LogicalKeyboardKeyKeyS, m.SingleActivatorOpts{Control: true}), Callback: func() {
			if whenSaved != nil {
				whenSaved()
			}
		}},
		{Activator: m.NewSingleActivator(services.LogicalKeyboardKeyKeyS, m.SingleActivatorOpts{Control: true, Shift: true}), Callback: func() {
			if whenSavedAs != nil {
				whenSavedAs()
			}
		}},
	}, m.NewFocus(page, m.FocusOpts{Autofocus: true}))
}

func (s *editorState) title() string {
	if s.doc == nil {
		return "Save Editor"
	}
	title := filepath.Base(s.path)
	if n, _ := s.changes(); n > 0 {
		title = "• " + title
	}
	return title
}

func (s *editorState) buildEmpty(bc m.BuildContext) m.Widget {
	theme := m.ThemeOf(bc)
	return m.NewCenter(m.NewConstrainedBox(m.NewBoxConstraints(m.BoxConstraintsOpts{MaxWidth: m.Some(520.0)}),
		m.NewPadding(m.NewEdgeInsetsAll(24), m.NewColumn([]m.Widget{
			m.NewIcon(m.Icons.FolderOpen(), m.IconOpts{Size: m.Some(64.0), Color: m.Some(theme.ColorScheme.Primary())}),
			m.NewSizedBox(nil, m.SizedBoxOpts{Height: m.Some(16.0)}),
			m.NewText("Open a save file", m.TextOpts{Style: theme.TextTheme.HeadlineSmall}),
			m.NewSizedBox(nil, m.SizedBoxOpts{Height: m.Some(8.0)}),
			m.NewText("A save file is what a game writes to remember your progress. Close the game before you edit its saves. "+
				"Saves of these games can be opened: "+formatNames(s.Widget().formats)+".",
				m.TextOpts{TextAlign: m.TextAlignCenter, Style: theme.TextTheme.BodyMedium}),
			m.NewSizedBox(nil, m.SizedBoxOpts{Height: m.Some(24.0)}),
			m.NewFilledButtonIcon(s.chooseSave, m.NewText("Open save…"), m.FilledButtonIconOpts{Icon: m.NewIcon(m.Icons.FolderOpen())}),
		}, m.ColumnOpts{MainAxisSize: m.MainAxisSizeMin}))))
}

func (s *editorState) buildStatus(bc m.BuildContext) m.Widget {
	scheme := m.ThemeOf(bc).ColorScheme
	color := scheme.OnSurfaceVariant()
	if s.statusError {
		color = scheme.Error()
	}
	return m.NewContainer(
		m.NewText(s.status, m.TextOpts{MaxLines: m.Some(2), Overflow: m.TextOverflowEllipsis,
			Style: m.ThemeOf(bc).TextTheme.BodySmall.CopyWith(m.TextStyleCopy{Color: m.Some(color)})}),
		m.ContainerOpts{
			Color:   m.Some(scheme.SurfaceContainer()),
			Padding: m.NewEdgeInsetsSymmetric(6, 16),
		})
}

// buildDetails is the left side: the screenshot, the details, and for a Ren'Py save its name and
// signature.
func (s *editorState) buildDetails(bc m.BuildContext) m.Widget {
	theme := m.ThemeOf(bc)
	small := theme.TextTheme.BodySmall.CopyWith(m.TextStyleCopy{Color: m.Some(theme.ColorScheme.OnSurfaceVariant())})
	var shot m.Widget
	if sh, ok := s.doc.(model.Screenshotter); ok && len(sh.Screenshot()) > 0 {
		shot = m.NewClipRRect(m.NewImageMemory(sh.Screenshot(), m.ImageMemoryOpts{ImageOpts: m.ImageOpts{
			Fit: m.BoxFitContain, SemanticLabel: m.Some("Screenshot of the save"),
		}}), m.ClipRRectOpts{BorderRadius: m.NewBorderRadiusCircular(12)})
	}
	info := []m.Widget{}
	add := func(label, value, tooltip string) {
		if value == "" {
			return
		}
		var text m.Widget = m.NewText(value, m.TextOpts{Style: theme.TextTheme.BodyMedium})
		if tooltip != "" {
			text = m.NewTooltip(text, m.TooltipOpts{Message: tooltip})
		}
		info = append(info, m.NewPadding(m.NewEdgeInsetsSymmetric(3, 0), m.NewRow([]m.Widget{
			m.NewSizedBox(m.NewText(label, m.TextOpts{Style: small}), m.SizedBoxOpts{Width: m.Some(104.0)}),
			m.NewExpanded(text),
		}, m.RowOpts{CrossAxisAlignment: m.CrossAxisAlignmentStart})))
	}
	add("File", filepath.Base(s.path), s.path)
	add("Format", s.format.Name, "")
	for _, d := range s.doc.Details() {
		add(d.Label, d.Text, d.Tooltip)
	}

	var parts []m.Widget
	if shot != nil {
		parts = append(parts, shot, m.NewSizedBox(nil, m.SizedBoxOpts{Height: m.Some(16.0)}))
	}
	if s.renpy != nil {
		parts = append(parts, m.NewTextField(m.TextFieldOpts{
			Key:        m.NewValueKey("save name"),
			Controller: s.saveName,
			Decoration: &m.InputDecoration{LabelText: m.Some("Save name"), Border: m.NewOutlineInputBorder(), IsDense: m.Some(true)},
			OnChanged:  func(string) { s.SetState(func() {}) },
		}), m.NewSizedBox(nil, m.SizedBoxOpts{Height: m.Some(12.0)}))
	}
	parts = append(parts, m.NewColumn(info))
	if s.renpy != nil {
		parts = append(parts, m.NewSizedBox(nil, m.SizedBoxOpts{Height: m.Some(12.0)}), s.buildSignature(bc))
	}
	return m.NewSingleChildScrollView(m.NewPadding(m.NewEdgeInsetsAll(16), m.NewColumn(parts,
		m.ColumnOpts{CrossAxisAlignment: m.CrossAxisAlignmentStretch})))
}

// buildSignature is the card of a Ren'Py save: whether the game trusts its signatures, and the
// key to sign with.
func (s *editorState) buildSignature(bc m.BuildContext) m.Widget {
	theme := m.ThemeOf(bc)
	status := s.renpy.SignatureStatus(s.keys)
	icon, color := m.Icons.VerifiedUser(), theme.ColorScheme.Primary()
	explain := "Ren'Py loads this save without asking."
	switch status {
	case renpysave.Unsigned:
		icon, color = m.Icons.Info(), theme.ColorScheme.OnSurfaceVariant()
		explain = "Ren'Py asks once before loading it."
	case renpysave.SignedUntrusted:
		icon, color = m.Icons.Info(), theme.ColorScheme.Tertiary()
		explain = "Ren'Py asks whether to trust the computer that made it."
	case renpysave.SignedInvalid:
		icon, color = m.Icons.Warning(), theme.ColorScheme.Error()
		explain = "Ren'Py refuses to load it. Saving here fixes that."
	}
	canSign := s.keys != nil && s.keys.CanSign()
	var onSign func(bool)
	if canSign {
		onSign = func(v bool) { s.SetState(func() { s.sign = v }) }
	}
	keysText := "No Ren'Py key found. Edited saves are written without signatures."
	if canSign {
		keysText = "Key: " + s.keysPath
	} else if s.keys != nil {
		keysText = s.keysPath + " has no signing key."
	}
	small := theme.TextTheme.BodySmall.CopyWith(m.TextStyleCopy{Color: m.Some(theme.ColorScheme.OnSurfaceVariant())})
	return m.NewCardOutlined(m.NewPadding(m.NewEdgeInsetsAll(12), m.NewColumn([]m.Widget{
		m.NewRow([]m.Widget{
			m.NewIcon(icon, m.IconOpts{Color: m.Some(color), Size: m.Some(20.0)}),
			m.NewSizedBox(nil, m.SizedBoxOpts{Width: m.Some(8.0)}),
			m.NewExpanded(m.NewText(status.String(), m.TextOpts{Style: theme.TextTheme.TitleSmall})),
		}),
		m.NewSizedBox(nil, m.SizedBoxOpts{Height: m.Some(4.0)}),
		m.NewText(explain, m.TextOpts{Style: small}),
		m.NewSizedBox(nil, m.SizedBoxOpts{Height: m.Some(8.0)}),
		m.NewRow([]m.Widget{
			m.NewExpanded(m.NewText("Sign with my Ren'Py key", m.TextOpts{Style: theme.TextTheme.BodyMedium})),
			m.NewSwitch(s.sign && canSign, onSign),
		}),
		m.NewText(keysText, m.TextOpts{Style: small, MaxLines: m.Some(3), Overflow: m.TextOverflowEllipsis}),
		m.NewSizedBox(nil, m.SizedBoxOpts{Height: m.Some(8.0)}),
		m.NewAlign(m.NewOutlinedButtonIcon(s.chooseKeys, m.NewText("Choose key file…"),
			m.OutlinedButtonIconOpts{Icon: m.NewIcon(m.Icons.Key())}), m.AlignOpts{Alignment: m.AlignmentCenterLeft}),
	}, m.ColumnOpts{CrossAxisAlignment: m.CrossAxisAlignmentStretch})), m.CardOpts{Margin: m.NewEdgeInsetsAll(0)})
}

// buildVariables is the right side: the search bar and the list of variables.
func (s *editorState) buildVariables(bc m.BuildContext) m.Widget {
	theme := m.ThemeOf(bc)
	rows := buildRows(s.vars, s.filter.Text(), s.showHidden, s.expanded)
	n, _ := s.changes()
	changes := "No changes"
	if n == 1 {
		changes = "1 change"
	} else if n > 1 {
		changes = strconv.Itoa(n) + " changes"
	}
	var onDiscard func()
	if n > 0 || len(s.edits) > 0 {
		onDiscard = s.discard
	}
	var clear m.Widget
	if s.filter.Text() != "" {
		clear = m.NewIconButton(func() {
			s.SetState(func() { s.filter.Clear() })
		}, m.NewIcon(m.Icons.Clear()), m.IconButtonOpts{Tooltip: "Clear the search"})
	}
	barItems := []m.Widget{
		m.NewExpanded(m.NewTextField(m.TextFieldOpts{
			Key:        m.NewValueKey("search"),
			Controller: s.filter,
			Decoration: &m.InputDecoration{
				HintText:   m.Some("Search variables and values"),
				PrefixIcon: m.NewIcon(m.Icons.Search()),
				SuffixIcon: clear,
				Border:     m.NewOutlineInputBorder(),
				IsDense:    m.Some(true),
			},
			OnChanged: func(string) { s.SetState(func() {}) },
		})),
		m.NewSizedBox(nil, m.SizedBoxOpts{Width: m.Some(12.0)}),
	}
	if s.hasInternals {
		barItems = append(barItems, m.NewFilterChip(m.NewText("Internals"), m.FilterChipOpts{
			Selected:   s.showHidden,
			OnSelected: func(v bool) { s.SetState(func() { s.showHidden = v }) },
			Tooltip:    m.Some("Show the variables that belong to the engine, not to the game"),
		}), m.NewSizedBox(nil, m.SizedBoxOpts{Width: m.Some(12.0)}))
	}
	barItems = append(barItems, m.NewText(changes, m.TextOpts{Style: theme.TextTheme.LabelLarge}),
		m.NewTextButton(onDiscard, m.NewText("Discard")))
	bar := m.NewPadding(m.NewEdgeInsetsFromLTRB(16, 12, 16, 8), m.NewRow(barItems))
	var list m.Widget
	if len(rows) == 0 {
		list = m.NewCenter(m.NewText("No variable matches.", m.TextOpts{Style: theme.TextTheme.BodyMedium}))
	} else {
		// A row keeps its element (and the focus of its field) when rows above it come
		// and go, as a part is expanded or the search changes.
		index := make(map[m.Key]int, len(rows))
		for i, r := range rows {
			index[m.NewValueKey(r.id)] = i
		}
		list = m.NewListViewBuilder(func(bc m.BuildContext, i int) m.Widget {
			return s.buildRow(bc, rows[i])
		}, m.ListViewOpts{
			ItemCount: m.Some(len(rows)),
			Padding:   m.NewEdgeInsetsFromLTRB(0, 0, 0, 16),
			FindChildIndexCallback: func(key m.Key) m.Opt[int] {
				if i, ok := index[key]; ok {
					return m.Some(i)
				}
				return m.None[int]()
			},
		})
	}
	return m.NewColumn([]m.Widget{
		bar,
		m.NewDivider(m.DividerOpts{Height: m.Some(1.0)}),
		m.NewExpanded(list),
	}, m.ColumnOpts{CrossAxisAlignment: m.CrossAxisAlignmentStretch})
}

func (s *editorState) buildRow(bc m.BuildContext, r row) m.Widget {
	theme := m.ThemeOf(bc)
	scheme := theme.ColorScheme
	canEdit := editable(r.slot)
	var e *edit
	var changed bool
	if canEdit {
		e, changed = s.edits[r.slot.Site.ID]
	}

	var expander m.Widget = m.NewSizedBox(nil, m.SizedBoxOpts{Width: m.Some(40.0)})
	if r.expandable {
		icon := m.Icons.ChevronRight()
		if r.expanded {
			icon = m.Icons.ExpandMore()
		}
		expander = m.NewIconButton(func() { s.toggle(r.id) }, m.NewIcon(icon), m.IconButtonOpts{
			Tooltip: "Show the parts", VisualDensity: m.Some(m.VisualDensityCompact()),
		})
	}
	labelStyle := theme.TextTheme.BodyMedium
	if r.depth == 0 {
		labelStyle = labelStyle.CopyWith(m.TextStyleCopy{FontWeight: m.FontWeightW600})
	}
	typeName := r.slot.Value.TypeName()
	typeStyle := theme.TextTheme.LabelSmall.CopyWith(m.TextStyleCopy{Color: m.Some(scheme.OnSurfaceVariant())})

	var value m.Widget
	switch {
	case r.recursive:
		value = m.NewText("(contains itself)", m.TextOpts{Style: typeStyle})
	case canEdit && r.slot.Value.Kind == model.KindBool:
		on := r.slot.Value.Bool
		if changed {
			on = e.value.Bool
		}
		label := "false"
		if on {
			label = "true"
		}
		value = m.NewRow([]m.Widget{
			m.NewSwitch(on, func(v bool) { s.setBool(r.slot, v) }),
			m.NewSizedBox(nil, m.SizedBoxOpts{Width: m.Some(8.0)}),
			m.NewText(label, m.TextOpts{Style: theme.TextTheme.BodyMedium}),
		})
	case canEdit:
		dec := &m.InputDecoration{Border: m.NewOutlineInputBorder(), IsDense: m.Some(true)}
		if changed && e.err != "" {
			dec.ErrorText = m.Some(e.err)
		}
		value = m.NewTextField(m.TextFieldOpts{
			Key:        m.NewValueKey(r.slot.Site.ID),
			Controller: s.field(r.slot),
			Decoration: dec,
			OnChanged:  func(text string) { s.setText(r.slot, text) },
		})
	default:
		text := r.slot.Value.Repr()
		if r.slot.Value.Kind == model.KindObject {
			text = r.slot.Value.QualifiedTypeName()
			if n := len(r.slot.Value.Children); n > 0 {
				text += " (" + strconv.Itoa(n) + " " + partsWord(n) + ")"
			}
		}
		value = m.NewText(text, m.TextOpts{Style: theme.TextTheme.BodyMedium.CopyWith(m.TextStyleCopy{
			Color: m.Some(scheme.OnSurfaceVariant())}), MaxLines: m.Some(1), Overflow: m.TextOverflowEllipsis})
	}

	var trailing m.Widget = m.NewSizedBox(nil, m.SizedBoxOpts{Width: m.Some(40.0)})
	if changed {
		trailing = m.NewIconButton(func() { s.reset(r.slot) }, m.NewIcon(m.Icons.Undo()), m.IconButtonOpts{
			Tooltip: "Undo this change", VisualDensity: m.Some(m.VisualDensityCompact()),
		})
	}
	content := m.NewPadding(m.NewEdgeInsetsFromLTRB(8+20*float64(r.depth), 4, 8, 4), m.NewRow([]m.Widget{
		expander,
		m.NewSizedBox(m.NewTooltip(m.NewText(r.label, m.TextOpts{Style: labelStyle, MaxLines: m.Some(1), Overflow: m.TextOverflowEllipsis}),
			m.TooltipOpts{Message: rowTooltip(r)}), m.SizedBoxOpts{Width: m.Some(math.Max(120, 240-20*float64(r.depth)))}),
		m.NewSizedBox(m.NewText(typeName, m.TextOpts{Style: typeStyle, MaxLines: m.Some(1), Overflow: m.TextOverflowEllipsis}),
			m.SizedBoxOpts{Width: m.Some(110.0)}),
		m.NewExpanded(value),
		trailing,
	}))
	// The row always has a color, transparent until it changes: a Container that gains a
	// color puts a box around its child, which would make a new field and lose the focus.
	color := m.Colors.Transparent
	if changed {
		color = scheme.SecondaryContainer().WithOpacity(0.5)
	}
	return m.NewContainer(content, m.ContainerOpts{Key: m.NewValueKey(r.id), Color: m.Some(color)})
}

// rowTooltip is the full name and type of a row.
func rowTooltip(r row) string {
	name := strings.ReplaceAll(r.id, "\x00", " › ")
	return name + "\n" + r.slot.Value.QualifiedTypeName()
}

// existingFolder is dir when it is a folder, or empty so the dialog chooses.
func existingFolder(dir string) string {
	if dir == "" {
		return ""
	}
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return ""
}
