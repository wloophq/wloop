package catalog

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// The surfaces an action spec names.
const (
	SurfaceRow          = "row"
	SurfaceDetail       = "detail"
	SurfaceTUI          = "tui"
	SurfaceMenubar      = "menubar"
	SurfaceNotification = "notification"
	SurfaceCLI          = "cli"
	SurfaceExplore      = "explore"
)

// SourceBuiltin is the Source of an action spec Go declares.
const SourceBuiltin = "builtin"

// The origins of an action spec, in the order a shared verb ranks them.
const (
	OriginBuiltin = iota
	OriginBundled
	OriginUser
)

// Origin is where s comes from: Go (Source "builtin"), a bundled manifest (a relative path) or a user's (an
// absolute path).
func Origin(s api.ActionSpec) int {
	switch {
	case s.Source == SourceBuiltin:
		return OriginBuiltin
	case filepath.IsAbs(s.Source):
		return OriginUser
	}
	return OriginBundled
}

// Rank orders the specs whose verb a task runs on a unit of provider, the lowest first: the built-in
// actions, then the intents of the unit's own provider, then those of any provider, a user's intent before
// a bundled one within each.
func Rank(s api.ActionSpec, provider unit.ProviderID) int {
	other := 0
	if s.Plugin != string(provider) {
		other = 1
	}
	switch Origin(s) {
	case OriginBuiltin:
		return other
	case OriginUser:
		return 2 + 2*other
	}
	return 3 + 2*other
}

type builtinAction struct {
	// spec is the action without its words, which actions.toml holds by
	// its id.
	spec api.ActionSpec
	// offer is the action's state on the row, without its texts; false
	// leaves it out.
	offer func(f Facts) (api.ActionState, bool)
}

func on(id string) (api.ActionState, bool) { return api.ActionState{ID: id, Enabled: true}, true }

func off(id string) (api.ActionState, bool) { return api.ActionState{ID: id}, true }

func absent() (api.ActionState, bool) { return api.ActionState{}, false }

// BasisPending and BasisHeld prefix the basis of the plan gate's first
// approving intent.
const (
	BasisPending = "pending:"
	BasisHeld    = "held:"
)

// blockSurfaces are where the actions on a start block show: the CLI has
// its own trust command.
var blockSurfaces = []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceMenubar, SurfaceNotification}

func everywhere(extra ...string) []string {
	return append([]string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceMenubar, SurfaceNotification, SurfaceCLI}, extra...)
}

// builtinActions is the catalog's Go table; the intents' actions go after
// the start group.
var builtinActions = []builtinAction{
	{api.ActionSpec{ID: "take_over", Group: "control", Key: "A", Surfaces: []string{SurfaceDetail, SurfaceTUI, SurfaceCLI, SurfaceExplore}}, func(f Facts) (api.ActionState, bool) {
		if !f.Open || f.Override.Adopted {
			return absent()
		}
		if f.Mine {
			return off("take_over")
		}
		return on("take_over")
	}},
	{api.ActionSpec{ID: "pause", Group: "control", Key: "H", Toggle: "resume", Surfaces: everywhere()}, func(f Facts) (api.ActionState, bool) {
		if !f.Tracked || !f.Open || f.Override.Hold {
			return absent()
		}
		return on("pause")
	}},
	{api.ActionSpec{ID: "resume", Group: "control", Key: "H", Toggle: "pause", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceMenubar, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if !f.Tracked || !f.Open || !f.Override.Hold {
			return absent()
		}
		return on("resume")
	}},
	{api.ActionSpec{ID: "mute", Group: "control", Key: "M", Toggle: "unmute", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceCLI},
		Input: &api.InputSpec{Kind: "duration", Required: true, Default: "2d", UntilState: true}}, func(f Facts) (api.ActionState, bool) {
		if !f.Tracked || !f.Open || f.Muted {
			return absent()
		}
		return on("mute")
	}},
	{api.ActionSpec{ID: "unmute", Group: "control", Key: "M", Toggle: "mute", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if !f.Tracked || (f.Open && !f.Muted) || (!f.Open && !f.MutedStored) {
			return absent()
		}
		return on("unmute")
	}},
	{api.ActionSpec{ID: "track", Group: "control", Toggle: "untrack", Surfaces: []string{SurfaceDetail, SurfaceCLI, SurfaceExplore}}, func(f Facts) (api.ActionState, bool) {
		if f.Override.Tracked || !f.Open {
			return absent()
		}
		return on("track")
	}},
	{api.ActionSpec{ID: "untrack", Group: "control", Toggle: "track", Surfaces: []string{SurfaceDetail, SurfaceCLI, SurfaceExplore}}, func(f Facts) (api.ActionState, bool) {
		if !f.Override.Tracked {
			return absent()
		}
		return on("untrack")
	}},
	{api.ActionSpec{ID: "open", Group: "workspace", Key: "O", Surfaces: everywhere()}, func(Facts) (api.ActionState, bool) {
		return on("open")
	}},
	{api.ActionSpec{ID: "remove", Group: "workspace", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if !removable(f) {
			return absent()
		}
		return on("remove")
	}},
	{api.ActionSpec{ID: "remove_force", Group: "workspace", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if !removable(f) || f.Forceable == "" {
			return absent()
		}
		return on("remove_force")
	}},
	{api.ActionSpec{ID: "brief_refresh", Group: "brief", Surfaces: []string{SurfaceDetail, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if !f.Tracked {
			return absent()
		}
		if !f.BriefOn {
			return off("brief_refresh")
		}
		return on("brief_refresh")
	}},
	{api.ActionSpec{ID: "intake_without", Group: "intake", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceMenubar, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		i := f.intake()
		if i == nil || !i.Waiting() {
			return absent()
		}
		if i.FromSession {
			return off("intake_without")
		}
		return on("intake_without")
	}},
	{api.ActionSpec{ID: "intake_cancel", Group: "intake", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceMenubar, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if i := f.intake(); i == nil || !i.Waiting() {
			return absent()
		}
		return on("intake_cancel")
	}},
	draftAction("draft.edit", "edit", &api.InputSpec{Kind: "text", Required: true}),
	draftAction("draft.retry", "retry", nil),
	runAction(RunOpen),
	runAction(RunEnter),
	runAction(RunTranscript),
}

// The run actions, as a RunView's actions name them; actions.toml words each under runPrefix and its name.
const (
	RunOpen       = "open"
	RunEnter      = "enter"
	RunTranscript = "transcript"
)

const runPrefix = "run."

// runAction is a run's action: no row offers it, a task's run does (RunActions).
func runAction(name string) builtinAction {
	spec := api.ActionSpec{ID: runPrefix + name, Group: "run", Scope: ScopeRun, Surfaces: []string{SurfaceDetail, SurfaceCLI}}
	return builtinAction{spec, func(Facts) (api.ActionState, bool) { return absent() }}
}

// RunFacts are what a run's actions depend on: its workspace in a manager, a session not closed, and an agent
// transcript.
type RunFacts struct {
	Workspace, Session, Transcript bool
}

// RunActions are the names of the run actions a run offers, in catalog order.
func RunActions(f RunFacts) []string {
	offered := map[string]bool{RunOpen: f.Workspace, RunEnter: f.Workspace && f.Session, RunTranscript: f.Transcript}
	var out []string
	for _, b := range builtinActions {
		if name := strings.TrimPrefix(b.spec.ID, runPrefix); b.spec.Scope == ScopeRun && offered[name] {
			out = append(out, name)
		}
	}
	return out
}

// draftAction is an action on a draft's delivery: no row offers it, the draft's task does (DraftStates). Approving
// and discarding a draft are its gate's intents.
func draftAction(id, verb string, input *api.InputSpec) builtinAction {
	spec := api.ActionSpec{ID: id, Group: "draft", Scope: ScopeDraft, Verb: verb, Input: input,
		Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceCLI}}
	return builtinAction{spec, func(Facts) (api.ActionState, bool) { return absent() }}
}

// worded is spec with the words of its actions.toml table key.
func worded(l *intentload.Loaded, key string, spec api.ActionSpec) api.ActionSpec {
	a := actionWords(l, key)
	spec.Label, spec.Summary = a.Label, a.Summary
	if spec.Input != nil {
		in := *spec.Input
		in.Label, in.Placeholder = a.Input.Label, a.Input.Help
		spec.Input = &in
	}
	return spec
}

func actionWords(l *intentload.Loaded, key string) intentload.BuiltinAction {
	if l == nil {
		return intentload.BuiltinAction{}
	}
	return l.Actions[key]
}

// texted is st with the texts of its actions.toml table key over facts:
// note, confirm and warning when it is enabled, why when it is not. A
// text that fails to render says so, so a refusal never reads empty.
func texted(l *intentload.Loaded, key string, st api.ActionState, facts map[string]any) api.ActionState {
	a := actionWords(l, key)
	render := func(name string) string {
		out, err := a.Render(name, facts)
		if err != nil {
			return fmt.Sprintf("the profile has errors: %s: %s.%s: %v", intentload.ActionsFile, key, name, err)
		}
		return out
	}
	if !st.Enabled {
		st.Reason, st.Refusal = render("why"), api.BuiltinWhy(key)
		return st
	}
	st.Note, st.Confirm, st.Warning = render("note"), render("confirm"), render("warning")
	return st
}

// gateTexted is st with the note and confirm of gate intent in over facts,
// when it is enabled.
func gateTexted(in intentload.GateIntent, st api.ActionState, facts map[string]any) api.ActionState {
	if !st.Enabled {
		return st
	}
	render := func(name string) string {
		out, err := in.Render(name, facts)
		if err != nil {
			return fmt.Sprintf("the profile has errors: %s: gates.*.intents.%s.%s: %v", intentload.GatesFile, in.Name, name, err)
		}
		return out
	}
	st.Note, st.Confirm = render("note"), render("confirm")
	return st
}

// draftFrom are the draft states each draft action moves a draft from.
var draftFrom = map[string][]string{
	"draft.edit":  {"held", "failed"},
	"draft.retry": {"failed"},
}

// DraftFacts are what a draft's actions depend on.
type DraftFacts struct {
	State    string
	Provider unit.ProviderID
	// Disabled is a draft whose plugin is off; NoPublisher one whose plugin posts nothing.
	Disabled, NoPublisher bool
	// MayHaveBeenPosted is a failed draft whose post may have reached its provider.
	MayHaveBeenPosted bool
}

// postingDraftActions are the draft actions that lead to a post.
var postingDraftActions = []string{"draft.retry"}

// DraftStates are the draft actions in catalog order as they stand on a draft, worded by l: all disabled
// when its plugin is off.
func DraftStates(l *intentload.Loaded, f DraftFacts) []api.ActionState {
	out := []api.ActionState{}
	for _, b := range builtinActions {
		if b.spec.Scope != ScopeDraft {
			continue
		}
		refusal := ""
		switch {
		case f.Disabled:
			refusal = "plugin_off"
		case !slices.Contains(draftFrom[b.spec.ID], f.State):
			refusal = "state"
		case f.NoPublisher && slices.Contains(postingDraftActions, b.spec.ID):
			refusal = "posts_nothing"
		}
		facts := intentload.DraftActionFactMap(func(name string) any {
			switch name {
			case "draft.state":
				return f.State
			case "draft.provider":
				return string(f.Provider)
			case "draft.refusal":
				return refusal
			case "draft.maybe_posted":
				return f.MayHaveBeenPosted
			}
			return nil
		})
		out = append(out, texted(l, b.spec.ID, api.ActionState{ID: b.spec.ID, Enabled: refusal == ""}, facts))
	}
	return out
}

func removable(f Facts) bool {
	return f.Workspace.ID != "" && !f.Workspace.IsMain
}

// SessionWithout is why a turn by hand's intake does not start without its brief.
func SessionWithout(seq int64) string {
	return fmt.Sprintf("intake %d is a turn by hand, which only its session starts; cancel it with "+brand.Command+" intake cancel %d", seq, seq)
}

// intentScope is item for an intent whose turns may run headless, which act on the items selected on
// their unit, and unit for every other intent.
func intentScope(d dintent.Def) string {
	if d.Runs("headless") {
		return ScopeItem
	}
	return ScopeUnit
}

// recommend makes an intent that runs headless primary on a row whose items are recommended to run headless.
func recommend(st *api.ActionState, d dintent.Def, mode string) {
	st.Primary = st.Primary || mode == "headless" && d.Runs("headless")
}
