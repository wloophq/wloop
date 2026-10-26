package intent

import (
	"io"
	"slices"
	"strings"
	"text/template"

	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

var (
	actionGroups          = []string{"round", "turn", "control", "workspace", "brief", "intake"}
	actionSurfaces        = []string{"row", "detail", "tui", "menubar", "notification", "cli", "explore"}
	defaultActionSurfaces = []string{"row", "detail", "tui", "cli", "explore"}
)

type mAction struct {
	Group            *string  `toml:"group"`
	Surfaces         []string `toml:"surfaces"`
	Visible          *string  `toml:"visible"`
	Enabled          *string  `toml:"enabled"`
	ReasonOff        *string  `toml:"reason_off"`
	Primary          *string  `toml:"primary"`
	Confirm          *string  `toml:"confirm"`
	Hint             *string  `toml:"hint"`
	NoteWhenQuestion string   `toml:"note_when_question"`
}

// decodeAction is the [action] table of m, with its defaults; nil when m
// has none.
func (s *sheet) decodeAction(a *mAction) *dintent.Action {
	if a == nil {
		return nil
	}
	return &dintent.Action{
		Group:            str(a.Group, "round"),
		Surfaces:         orEmpty(a.Surfaces, defaultActionSurfaces),
		Visible:          s.pred("action.visible", a.Visible, "row.open"),
		Enabled:          s.pred("action.enabled", a.Enabled, "true"),
		ReasonOff:        s.text("action.reason_off", a.ReasonOff, ""),
		Primary:          s.pred("action.primary", a.Primary, "false"),
		Confirm:          s.text("action.confirm", a.Confirm, ""),
		Hint:             s.text("action.hint", a.Hint, ""),
		NoteWhenQuestion: a.NoteWhenQuestion,
	}
}

func (s *sheet) validateAction(d dintent.Def) {
	a := d.Action
	if a == nil {
		return
	}
	if !d.UserFacing {
		s.add("action", "applies to user-facing intents only")
	}
	s.oneOf("action.group", a.Group, actionGroups)
	s.eachOneOf("action.surfaces", a.Surfaces, actionSurfaces)
	if a.Enabled.Src != "true" && a.ReasonOff.Src == "" {
		s.add("action.reason_off", "is required when enabled is not \"true\"")
	}
}

// ActionPrograms are an intent's compiled [action] predicates and texts.
type ActionPrograms struct {
	Visible     *expr.Program
	Enabled     *expr.Program
	Primary     *expr.Program
	ActionTexts map[string]*template.Template // reason_off, confirm, hint over the action fact map
}

// compileAction compiles a's predicates with one, over the action slot.
func compileAction(a *dintent.Action, one func(path string, p dintent.Pred) *expr.Program) ActionPrograms {
	if a == nil {
		return ActionPrograms{}
	}
	return ActionPrograms{
		Visible: one("action.visible", a.Visible),
		Enabled: one("action.enabled", a.Enabled),
		Primary: one("action.primary", a.Primary),
	}
}

// ActionFuncs are the functions action texts may call.
var ActionFuncs = template.FuncMap{"join": strings.Join, "name": func() string { return brand.Name }}

var actionTexts = []string{"reason_off", "confirm", "hint"}

// parseActionTexts parses reason_off, confirm and hint and renders each over a
// fully populated action fact map.
func (s *sheet) parseActionTexts(a *dintent.Action) map[string]*template.Template {
	if a == nil {
		return nil
	}
	sample := SampleActionFacts()
	out := map[string]*template.Template{}
	for _, k := range actionTexts {
		txt := map[string]dintent.Text{"reason_off": a.ReasonOff, "confirm": a.Confirm, "hint": a.Hint}[k]
		if txt.Src == "" {
			continue
		}
		path := "action." + k
		t, err := template.New(path).Funcs(ActionFuncs).Option("missingkey=error").Parse(txt.Src)
		if err == nil {
			err = t.Execute(io.Discard, sample)
		}
		if err != nil {
			_, msg := templateMsg(path, err)
			s.add(path, "%s", msg)
			continue
		}
		out[k] = t
	}
	return out
}

// ActionFactMap nests dotted action facts into the map action texts execute over:
// {"pr": {"slug": ...}, "row": {"nudge": {"who": ...}}}.
func ActionFactMap(get func(name string) any) map[string]any {
	root := map[string]any{}
	for _, f := range builtin.Facts {
		if !slices.Contains(f.Slots, builtin.ActionSlot) {
			continue
		}
		parts := strings.Split(f.Name, ".")
		m := root
		for _, p := range parts[:len(parts)-1] {
			next, ok := m[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[p] = next
			}
			m = next
		}
		m[parts[len(parts)-1]] = get(f.Name)
	}
	return root
}

// SampleActionFacts is the action fact map with every fact set, for checking texts.
func SampleActionFacts() map[string]any {
	types := map[string]expr.Type{}
	for _, f := range builtin.Facts {
		if slices.Contains(f.Slots, builtin.ActionSlot) {
			types[f.Name] = f.Type
		}
	}
	return ActionFactMap(func(name string) any {
		switch types[name] {
		case expr.Bool:
			return true
		case expr.Int:
			return 1
		case expr.List:
			return []string{"sample"}
		default:
			return "sample"
		}
	})
}
