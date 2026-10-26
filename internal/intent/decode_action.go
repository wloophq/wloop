package intent

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/template"

	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

var defaultActionSurfaces = []string{"row", "detail", "tui", "cli", "explore"}

type mAction struct {
	Group    *string    `toml:"group"`
	Surfaces []string   `toml:"surfaces"`
	When     *string    `toml:"when"`
	Primary  *mWhen     `toml:"primary"`
	Refuse   []mRefusal `toml:"refuse"`
	Note     *string    `toml:"note"`
	Warning  *string    `toml:"warning"`
	Confirm  *string    `toml:"confirm"`
}

type mWhen struct {
	When *string `toml:"when"`
}

type mRefusal struct {
	When *string `toml:"when"`
	Why  *string `toml:"why"`
}

// decodeAction is the [action] table of m, with its defaults; nil when m
// has none.
func (s *sheet) decodeAction(a *mAction) *dintent.Action {
	if a == nil {
		return nil
	}
	var primary *string
	if a.Primary != nil {
		primary = a.Primary.When
	}
	out := &dintent.Action{
		Group:    str(a.Group, builtin.GroupStart),
		Surfaces: orEmpty(a.Surfaces, defaultActionSurfaces),
		When:     s.pred("action.when", a.When, "row.open"),
		Primary:  s.pred("action.primary.when", primary, "false"),
		Note:     s.text("action.note", a.Note, ""),
		Warning:  s.text("action.warning", a.Warning, ""),
		Confirm:  s.text("action.confirm", a.Confirm, ""),
	}
	for i, r := range a.Refuse {
		at := fmt.Sprintf("action.refuse[%d]", i)
		out.Refuse = append(out.Refuse, dintent.Refusal{When: s.pred(at+".when", r.When, ""), Why: s.text(at+".why", r.Why, "")})
	}
	return out
}

func (s *sheet) validateAction(d dintent.Def) {
	a := d.Action
	if a == nil {
		return
	}
	if !d.UserFacing {
		s.add("action", "applies to user-facing intents only")
	}
	s.oneOf("action.group", a.Group, builtin.ActionGroups)
	s.eachOneOf("action.surfaces", a.Surfaces, builtin.ActionSurfaces)
	for i, r := range a.Refuse {
		at := fmt.Sprintf("action.refuse[%d]", i)
		if r.When.Src == "" {
			s.add(at+".when", "is required")
		}
		if r.Why.Src == "" {
			s.add(at+".why", "is required: it is what the disabled action says")
		}
	}
}

// RefusalProgram is an [[action.refuse]] case's compiled when.
type RefusalProgram struct {
	When *expr.Program
}

// ActionPrograms are an intent's compiled [action] predicates and texts.
type ActionPrograms struct {
	When    *expr.Program
	Primary *expr.Program
	Refuse  []RefusalProgram
	// ActionTexts are note, warning, confirm and each refusal's why
	// ("refuse[<i>].why"), over the action fact map.
	ActionTexts map[string]*template.Template
}

// RefusalText is the ActionTexts key of the why of refusal i.
func RefusalText(i int) string { return fmt.Sprintf("refuse[%d].why", i) }

// compileAction compiles a's predicates with one, over the action slot.
func compileAction(a *dintent.Action, one func(path string, p dintent.Pred) *expr.Program) ActionPrograms {
	if a == nil {
		return ActionPrograms{}
	}
	ps := ActionPrograms{
		When:    one("action.when", a.When),
		Primary: one("action.primary.when", a.Primary),
	}
	for i, r := range a.Refuse {
		var p *expr.Program
		if r.When.Src != "" {
			p = one(fmt.Sprintf("action.refuse[%d].when", i), r.When)
		}
		ps.Refuse = append(ps.Refuse, RefusalProgram{When: p})
	}
	return ps
}

// ActionFuncs are the functions action texts may call.
var ActionFuncs = template.FuncMap{"join": strings.Join, "name": func() string { return brand.Name }, "command": func() string { return brand.Command }}

// ActionText parses an action text named path and executes it once over
// sample, so a text that cannot render fails at load.
func ActionText(path, src string, sample map[string]any) (*template.Template, error) {
	t, err := template.New(path).Funcs(ActionFuncs).Option("missingkey=error").Parse(src)
	if err == nil {
		err = t.Execute(io.Discard, sample)
	}
	return t, err
}

// parseActionTexts parses note, warning, confirm and every refusal's why,
// checking each over a fully populated action fact map.
func (s *sheet) parseActionTexts(a *dintent.Action) map[string]*template.Template {
	if a == nil {
		return nil
	}
	texts := map[string]dintent.Text{"note": a.Note, "warning": a.Warning, "confirm": a.Confirm}
	for i, r := range a.Refuse {
		texts[RefusalText(i)] = r.Why
	}
	sample := SampleActionFacts()
	out := map[string]*template.Template{}
	for _, k := range slices.Sorted(maps.Keys(texts)) {
		if texts[k].Src == "" {
			continue
		}
		path := "action." + k
		t, err := ActionText(path, texts[k].Src, sample)
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
// {"pr": {"slug": ...}, "row": {"question": ...}}.
func ActionFactMap(get func(name string) any) map[string]any {
	return factMap(builtin.ActionSlot, get)
}

// DraftActionFactMap is ActionFactMap for the texts of a draft's actions.
func DraftActionFactMap(get func(name string) any) map[string]any {
	return factMap(builtin.DraftActionSlot, get)
}

func factMap(slot builtin.FactSlot, get func(name string) any) map[string]any {
	root := map[string]any{}
	for _, f := range builtin.Facts {
		if !slices.Contains(f.Slots, slot) {
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
func SampleActionFacts() map[string]any { return sampleFacts(builtin.ActionSlot) }

// SampleDraftActionFacts is SampleActionFacts for a draft's actions.
func SampleDraftActionFacts() map[string]any { return sampleFacts(builtin.DraftActionSlot) }

func sampleFacts(slot builtin.FactSlot) map[string]any {
	types := map[string]expr.Type{}
	for _, f := range builtin.Facts {
		if slices.Contains(f.Slots, slot) {
			types[f.Name] = f.Type
		}
	}
	return factMap(slot, func(name string) any {
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
