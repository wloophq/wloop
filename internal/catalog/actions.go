package catalog

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
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

// The origins of an action spec, in the order a shared gtd verb ranks them.
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

// Rank orders the specs whose gtd verb an entry runs on a unit of provider, the lowest first: the built-in
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
	spec api.ActionSpec
	// offer is the action's state on the row; false leaves it out.
	offer func(f Facts) (api.ActionState, bool)
}

func on(id string) (api.ActionState, bool) { return api.ActionState{ID: id, Enabled: true}, true }

func off(id, reason string) (api.ActionState, bool) {
	return api.ActionState{ID: id, Reason: reason}, true
}

func absent() (api.ActionState, bool) { return api.ActionState{}, false }

const (
	reasonGateOff  = "the daemon plans no automatic round; see doctor"
	reasonBriefOff = "the brief is off; set brief.enabled in the config"
	// ReasonYours disables take_over on a pull request that is the user's.
	ReasonYours = "It is yours already"
)

// BasisPending and BasisHeld prefix a run_now basis.
const (
	BasisPending = "pending:"
	BasisHeld    = "held:"
)

func questionOffer(id string) func(f Facts) (api.ActionState, bool) {
	return func(f Facts) (api.ActionState, bool) {
		if !f.Question {
			return off(id, fmt.Sprintf("%s on %s", turn.ErrNoQuestion, f.slug()))
		}
		return on(id)
	}
}

// blockSurfaces are where the actions on a start block show: the CLI has
// its own trust command.
var blockSurfaces = []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceMenubar, SurfaceNotification}

func everywhere(extra ...string) []string {
	return append([]string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceMenubar, SurfaceNotification, SurfaceCLI}, extra...)
}

// builtinActions is the catalog's Go table; the intents' actions go after
// the round group.
var builtinActions = []builtinAction{
	{api.ActionSpec{ID: "run_now", Label: "Run now", Group: "round", Key: "R", Verb: "approve", Surfaces: everywhere(), Intake: true}, func(f Facts) (api.ActionState, bool) {
		if p := f.Row.Pending; p != nil {
			return api.ActionState{ID: "run_now", Enabled: true, Primary: true, Basis: BasisPending + p.Since.UTC().Format(time.RFC3339Nano)}, true
		}
		if f.HeldByCap {
			return api.ActionState{ID: "run_now", Enabled: true, Primary: true, Basis: BasisHeld + f.Row.Summary.Reason,
				Confirm: fmt.Sprintf("Run the held round on %s now? It does not count against the cap.", f.slug())}, true
		}
		return absent()
	}},
	{api.ActionSpec{ID: "skip", Label: "Skip", Group: "round", Key: "S", Verb: "dismiss", Surfaces: everywhere()}, func(f Facts) (api.ActionState, bool) {
		if f.Row.Pending == nil {
			return absent()
		}
		return on("skip")
	}},
	{api.ActionSpec{ID: "guide", Label: "Guide", Group: "round", Key: "G", Surfaces: []string{SurfaceDetail, SurfaceTUI, SurfaceNotification},
		Input: &api.InputSpec{Kind: "text", Required: true, Label: "Guidance", Placeholder: "what the next round should look at"}}, func(f Facts) (api.ActionState, bool) {
		if !f.Tracked || !f.Open {
			return absent()
		}
		if !f.GateOn {
			return off("guide", reasonGateOff)
		}
		st, _ := on("guide")
		if f.Row.Pending == nil {
			st.Note = "Kept for the next automatic round"
		}
		return st, true
	}},
	{api.ActionSpec{ID: "trust_run", Label: "Trust folder and run", Group: "round", Key: "T", Surfaces: blockSurfaces}, func(f Facts) (api.ActionState, bool) {
		if !f.StartBlocked || f.TrustFolder == "" || len(f.TrustHomes) == 0 || !f.Open {
			return absent()
		}
		return api.ActionState{ID: "trust_run", Enabled: true, Primary: true,
			Note: fmt.Sprintf("Trust %s in %s, then run the round", f.TrustFolder, strings.Join(f.TrustHomes, ", "))}, true
	}},
	{api.ActionSpec{ID: "unblock", Label: "Dismiss block", Group: "round", Key: "D", Surfaces: blockSurfaces}, func(f Facts) (api.ActionState, bool) {
		if !f.StartBlocked {
			return absent()
		}
		st, _ := on("unblock")
		st.Note = "Lift the block without trusting; the round waits for a new trigger"
		return st, true
	}},
	{api.ActionSpec{ID: "answer", Label: "Answer", Group: "turn", Key: "W", Verb: "answer", Surfaces: []string{SurfaceDetail, SurfaceTUI, SurfaceNotification},
		Input: &api.InputSpec{Kind: "answer", Required: true, Label: "Answer"}}, questionOffer("answer")},
	{api.ActionSpec{ID: "continue", Label: "Continue", Group: "turn", Key: "C", Surfaces: []string{SurfaceDetail, SurfaceTUI, SurfaceNotification}}, questionOffer("continue")},
	{api.ActionSpec{ID: "dismiss", Label: "Dismiss", Group: "turn", Key: "X", Verb: "dismiss", Surfaces: []string{SurfaceDetail, SurfaceTUI, SurfaceNotification}}, func(f Facts) (api.ActionState, bool) {
		st, ok := questionOffer("dismiss")(f)
		if st.Enabled {
			st.Note = "Close the question; the pull request goes back to whoever it waits on"
		}
		return st, ok
	}},
	{api.ActionSpec{ID: "take_over", Label: "Take over", Group: "control", Key: "A", Surfaces: []string{SurfaceDetail, SurfaceTUI, SurfaceCLI, SurfaceExplore}}, func(f Facts) (api.ActionState, bool) {
		if !f.Open || f.Override.Adopted {
			return absent()
		}
		if f.Mine {
			return off("take_over", ReasonYours)
		}
		return api.ActionState{ID: "take_over", Enabled: true,
			Confirm: fmt.Sprintf("Take over %s? It becomes yours to develop: "+brand.Name+" creates or renames its author workspace.", f.slug())}, true
	}},
	{api.ActionSpec{ID: "pause", Label: "Pause", Group: "control", Key: "H", Toggle: "resume", Surfaces: everywhere()}, func(f Facts) (api.ActionState, bool) {
		if !f.Tracked || !f.Open || f.Override.Hold {
			return absent()
		}
		return on("pause")
	}},
	{api.ActionSpec{ID: "resume", Label: "Resume", Group: "control", Key: "H", Toggle: "pause", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceMenubar, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if !f.Tracked || !f.Open || !f.Override.Hold {
			return absent()
		}
		return on("resume")
	}},
	{api.ActionSpec{ID: "mute", Label: "Mute", Group: "control", Key: "M", Toggle: "unmute", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceCLI},
		Input: &api.InputSpec{Kind: "duration", Required: true, Label: "Mute for", Placeholder: "2d, 36h", Default: "2d", UntilState: true}}, func(f Facts) (api.ActionState, bool) {
		if !f.Tracked || !f.Open || f.Muted {
			return absent()
		}
		return on("mute")
	}},
	{api.ActionSpec{ID: "unmute", Label: "Unmute", Group: "control", Key: "M", Toggle: "mute", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if !f.Tracked || (f.Open && !f.Muted) || (!f.Open && !f.MutedStored) {
			return absent()
		}
		return on("unmute")
	}},
	{api.ActionSpec{ID: "track", Label: "Track", Group: "control", Toggle: "untrack", Surfaces: []string{SurfaceDetail, SurfaceCLI, SurfaceExplore}}, func(f Facts) (api.ActionState, bool) {
		if f.Override.Tracked || !f.Open {
			return absent()
		}
		return on("track")
	}},
	{api.ActionSpec{ID: "untrack", Label: "Untrack", Group: "control", Toggle: "track", Surfaces: []string{SurfaceDetail, SurfaceCLI, SurfaceExplore}}, func(f Facts) (api.ActionState, bool) {
		if !f.Override.Tracked {
			return absent()
		}
		return on("untrack")
	}},
	{api.ActionSpec{ID: "open", Label: "Open", Group: "workspace", Key: "O", Surfaces: everywhere()}, func(Facts) (api.ActionState, bool) {
		return on("open")
	}},
	{api.ActionSpec{ID: "remove", Label: "Remove", Group: "workspace", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if !removable(f) {
			return absent()
		}
		confirm := fmt.Sprintf("Remove the workspace of %s? Its checkout is deleted and its terminals are closed. It is refused while its agent works or waits, or when it holds uncommitted files, unpushed commits or ignored files outside build and dependency folders.", f.slug())
		if f.Open {
			confirm += " The pull request is still open: no automatic first round brings the workspace back; Review or Address does."
		}
		return api.ActionState{ID: "remove", Enabled: true, Confirm: confirm}, true
	}},
	{api.ActionSpec{ID: "remove_force", Label: "Force remove", Group: "workspace", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if !removable(f) || f.Forceable == "" {
			return absent()
		}
		return api.ActionState{ID: "remove_force", Enabled: true, Note: "Refused: " + f.Forceable,
			Confirm: fmt.Sprintf("Force remove the workspace of %s?\n\nIt was refused:\n%s\n\nForcing deletes its checkout with what is named above, for good.", f.slug(), f.Forceable)}, true
	}},
	{api.ActionSpec{ID: "brief_refresh", Label: "Refresh brief", Group: "brief", Surfaces: []string{SurfaceDetail, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if !f.Tracked {
			return absent()
		}
		if !f.BriefOn {
			return off("brief_refresh", reasonBriefOff)
		}
		st, _ := on("brief_refresh")
		st.Note = "Make this pull request's brief again now"
		return st, true
	}},
	{api.ActionSpec{ID: "intake_without", Label: "Start without brief", Group: "intake", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceMenubar, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		i := f.intake()
		if i == nil || !i.Waiting() {
			return absent()
		}
		if i.FromSession {
			return off("intake_without", SessionWithout(i.Seq))
		}
		return on("intake_without")
	}},
	{api.ActionSpec{ID: "intake_cancel", Label: "Cancel", Group: "intake", Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceMenubar, SurfaceCLI}}, func(f Facts) (api.ActionState, bool) {
		if i := f.intake(); i == nil || !i.Waiting() {
			return absent()
		}
		return on("intake_cancel")
	}},
	draftAction("draft.approve", "Approve", "approve", nil),
	draftAction("draft.edit", "Edit", "edit", &api.InputSpec{Kind: "text", Required: true, Label: "Text"}),
	draftAction("draft.discard", "Discard", "dismiss", nil),
	draftAction("draft.retry", "Retry", "retry", nil),
}

// draftAction is an Outbox action: no row offers it, the draft's entry does (DraftStates).
func draftAction(id, label, verb string, input *api.InputSpec) builtinAction {
	spec := api.ActionSpec{ID: id, Label: label, Group: "draft", Scope: ScopeDraft, Verb: verb, Input: input,
		Surfaces: []string{SurfaceRow, SurfaceDetail, SurfaceTUI, SurfaceCLI}}
	return builtinAction{spec, func(Facts) (api.ActionState, bool) { return absent() }}
}

// draftFrom are the draft states each draft action moves a draft from.
var draftFrom = map[string][]string{
	"draft.approve": {"proposed"},
	"draft.edit":    {"proposed", "failed"},
	"draft.discard": {"proposed", "approved", "failed"},
	"draft.retry":   {"failed"},
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
var postingDraftActions = []string{"draft.approve", "draft.retry"}

// DraftStates are the draft actions in catalog order as they stand on a draft: all disabled naming the plugin
// when it is off.
func DraftStates(f DraftFacts) []api.ActionState {
	out := []api.ActionState{}
	for _, b := range builtinActions {
		if b.spec.Scope != ScopeDraft {
			continue
		}
		st := api.ActionState{ID: b.spec.ID}
		switch {
		case f.Disabled && b.spec.ID != "draft.discard":
			st.Reason = string(f.Provider) + " is disabled"
		case !slices.Contains(draftFrom[b.spec.ID], f.State):
			st.Reason = "the draft is " + f.State
		case f.NoPublisher && slices.Contains(postingDraftActions, b.spec.ID):
			st.Reason = string(f.Provider) + " posts nothing"
		default:
			st.Enabled = true
		}
		if st.Enabled && b.spec.ID == "draft.retry" && f.MayHaveBeenPosted {
			st.Confirm = "This draft may have been posted already. Check its conversation first: retrying may post it twice."
		}
		out = append(out, st)
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

// recommend marks an intent that runs headless with the mode recommended for the row's items: primary when
// headless is, and the recommendation in its hint either way.
func recommend(st *api.ActionState, d dintent.Def, mode string) {
	if mode == "" || !d.Runs("headless") {
		return
	}
	st.Primary = st.Primary || mode == "headless"
	st.Note = strings.TrimSpace(st.Note + " (recommended: " + mode + ")")
}
