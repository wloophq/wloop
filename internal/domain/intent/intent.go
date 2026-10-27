// Package intent holds the definition of an intent, as its manifest declares
// it, and the registry of every loaded intent.
package intent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Name is an intent's name: the directory of its manifest.
type Name string

// DimStage is the profile dimension that selects by the pass over a unit:
// first or follow-up.
const DimStage = "stage"

// DimReviewer is the profile dimension that selects an address round by a reviewer it answers.
const DimReviewer = "reviewer"

// The intents Go binds by name; every other use of an intent goes through the registry.
const (
	Review  Name = "review"
	Address Name = "address"
	Nudge   Name = "nudge"
	Probe   Name = "probe"
	Act     Name = "act"
)

type Def struct {
	Name       Name
	Label      string
	Summary    string
	Key        string
	Verb       string // the gtd verb its action fulfills; "" for none
	UserFacing bool
	Workspace  string // review | author | linked_else_main | given
	Session    Session
	Context    string // pr | synthetic, from the role
	Launch     string // "" | probe, from the role
	Policy     string // round | probe, from the role
	Intake     bool
	StartLine  string
	OwnerOnly  bool
	Answerable bool
	Board      string // review | author | none, from the workspace
	DrivesRow  bool   // from the role, moved to a user intent that takes it
	Reports    bool
	Reconcile  string // "" | my_review, from the role
	Brief      Brief
	Profile    string // "" means none
	Dims       []string
	// Records is [record] when.
	Records Pred
	Prepare string // from the role
	// Effects are what sending the turn applies, turn effects of
	// builtin.Effects.
	Effects      []string
	Headline     Headline
	Guidance     Guidance
	Inputs       []Input // sorted by name
	AlsoProfiles []AlsoProfile
	Action       *Action // nil: not offered
	Join         Join
	Record       RecordRules
	Resume       Resume
	Source       Source
	Bundled      bool
	// Provider is the manifest's provider; default "github".
	Provider unit.ProviderID
	// DraftApproval is the manifest's [drafts.approval] block; nil when absent.
	DraftApproval *draft.Policy
	// Runners are turn.Runner values; default ["session"].
	Runners []string
	// Providers are the providers of a manifest whose provider is a list,
	// Provider the first; nil for a single provider.
	Providers []unit.ProviderID
	// Role is what Go resolves the intent by (Registry.ByRole), one of
	// builtin.Roles; "" none.
	Role string
	// Builds is what a builder intent builds, such as "brief": it is never
	// started as a turn. Prompt and Schema are its prompt, resolved through
	// the profile layers, and its schema, in its directory.
	Builds, Prompt, Schema string
	// Extra are a brief builder's extra fields, the top-level properties of
	// its Schema as the loader compiled it; nil for any other intent.
	Extra []jsonschema.Property
	// WorkspaceRepo is a manifest's workspace table: the repository the
	// turn runs in, named outright or by an input; nil for a workspace
	// builtin.
	WorkspaceRepo *WorkspaceRepo
	// AllowedEffects are the effects the intent's agents may declare on the
	// drafts they propose, names of builtin.Effects.
	AllowedEffects []string
	// RoundKind is the sweep round the intent's turns are, from its role:
	// "review" (first or follow-up, by the step's stage) or "address"; ""
	// none.
	RoundKind string
	// Why is the template, over the unit's workflow facts, that says why
	// the intent's turn is due; empty for none.
	Why Text
	// Refuse are the refusals of turn.start, in order: the first whose when
	// holds refuses with its why.
	Refuse []Refuse
	// Skill says whether install writes the intent a turn skill.
	Skill bool
	Help  Help
}

// Help is how the intent's command and turn skill present it.
type Help struct {
	// Target says what the command's target is, such as "a pull request
	// URL"; the turn skill and the command's help read it.
	Target string
	// Summary is the main help's line for the command, Summary when empty.
	Summary string
	// Group is the title of the main help's group the command joins, in
	// any case; "" or a title the help has not joins the user's intents.
	Group string
	// Examples are command lines without the command's name.
	Examples []string
	// Docs is a page path of the documentation site.
	Docs string
}

// WorkspaceRepo is where a turn runs when its manifest names a repository:
// Repo (owner/name) outright, or the input RepoInput holds.
type WorkspaceRepo struct {
	Repo, RepoInput string
}

// Runs says whether the intent's turns may run under runner, a turn.Runner
// value; nil Runners run as session only.
func (d Def) Runs(runner string) bool {
	if d.Runners == nil {
		return runner == "session"
	}
	return slices.Contains(d.Runners, runner)
}

type Session struct {
	Strategy string // reuse_live | new
	Title    string
	// Name is what the manager and the agent show the session as, over the
	// name facts; empty renders as Title.
	Name          Text
	CloseWhenDone bool
}

type Brief struct {
	Render, Wait bool
	// Need are the brief intent's extra fields the turn needs: a brief
	// missing one is completed before the turn starts.
	Need []string
}

type Pos struct {
	File      string
	Line, Col int
}

// Pred is a predicate's source and where its value starts; compiled programs live in the loader.
type Pred struct {
	Src string
	At  Pos
}

// Text is a text/template source.
type Text struct {
	Src string
	At  Pos
}

type Headline struct {
	Resumed Text
	New     []Case // the last case has an empty When
}

type Case struct {
	When Pred
	Text Text
}

type Guidance struct {
	Label, Help string
	Max         int
}

type Input struct {
	Name       string
	Type       string // flag | text | list
	Flag       string
	Help       string
	From       string // user | prepare
	Persist    bool
	Hidden     bool
	ByHandOnly bool
	Before     string
	Max        int
	Surfaces   []string
	At         Pos
}

type AlsoProfile struct {
	Profile, Define, Prefix string
	// Dims pins dimensions of the other judgment, such as stage = first.
	Dims map[string]string
	When Pred
}

// Refuse is a refusal of turn.start: when holds over the unit's workflow
// facts, why says it.
type Refuse struct {
	When Pred
	Why  Text
}

type Join struct{ DiffersOn []string }

type RecordRules struct {
	States          []string // done settled asked failed
	Artifacts       []string
	Outcome         []string // verdict url
	OutcomeDoc      string
	HistoryArtifact string
	Require         []Require
}

type Require struct {
	State, Artifact string
	Fields          []string
}

// Resume holds Changed, the text a resumed turn reads when its unit changed
// since the question.
type Resume struct{ Changed string }

type Source struct {
	Layer, Dir string // Dir "" for bundled
	// Overrides are the files, relative to Dir, that replace a bundled
	// intent's; the bundled intent's other files are inherited.
	Overrides []string
}

// Registry is the set of loaded intents, read-only once built.
type Registry struct {
	defs  []Def
	index map[Name]int
}

func NewRegistry(defs []Def) *Registry {
	r := &Registry{defs: slices.Clone(defs), index: make(map[Name]int, len(defs))}
	slices.SortStableFunc(r.defs, func(a, b Def) int { return strings.Compare(string(a.Name), string(b.Name)) })
	for i, d := range r.defs {
		r.index[d.Name] = i
	}
	return r
}

func (r *Registry) Get(n Name) (Def, bool) {
	i, ok := r.index[n]
	if !ok {
		return Def{}, false
	}
	return r.defs[i], true
}

func (r *Registry) All() []Def { return slices.Clone(r.defs) }

func (r *Registry) UserFacing() []Def {
	var out []Def
	for _, d := range r.defs {
		if d.UserFacing {
			out = append(out, d)
		}
	}
	return out
}

func (r *Registry) Answerable() []string {
	var out []string
	for _, d := range r.defs {
		if d.Answerable {
			out = append(out, string(d.Name))
		}
	}
	return out
}

// Names is "address, ask-author, nudge or review": user-facing, for messages.
func (r *Registry) Names() string {
	var names []string
	for _, d := range r.UserFacing() {
		names = append(names, string(d.Name))
	}
	return OrList(names)
}

// AllNames is Names over every intent, user-facing or not.
func (r *Registry) AllNames() string {
	var names []string
	for _, d := range r.All() {
		names = append(names, string(d.Name))
	}
	return OrList(names)
}

// OrList joins names as a message lists alternatives: "a, b or c".
func OrList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

func (r *Registry) DeclaringFlag(flag string) []Name {
	var out []Name
	for _, d := range r.defs {
		if slices.ContainsFunc(d.Inputs, func(in Input) bool { return in.Flag == flag }) {
			out = append(out, d.Name)
		}
	}
	return out
}

// Rules, Answerable, ClosesWhenDone, Reconcile and SessionTitles satisfy turns.Intents.
func (r *Registry) Rules(intent string) (RecordRules, error) {
	d, ok := r.Get(Name(intent))
	if !ok {
		return RecordRules{}, fmt.Errorf("the intent %q is not one "+brand.Name+" knows", intent)
	}
	return d.Record, nil
}

func (r *Registry) ClosesWhenDone(intent string) bool {
	d, _ := r.Get(Name(intent))
	return d.Session.CloseWhenDone
}

func (r *Registry) Reconcile(intent string) string {
	d, _ := r.Get(Name(intent))
	return d.Reconcile
}

// SessionTitles is session.title of the user-facing intents, each once.
func (r *Registry) SessionTitles() []string {
	var out []string
	for _, d := range r.UserFacing() {
		if !slices.Contains(out, d.Session.Title) {
			out = append(out, d.Session.Title)
		}
	}
	return out
}

// AnyProvider is the provider of an intent whose turns run on units of every provider.
const AnyProvider unit.ProviderID = "any"

// Serves says whether the intent's turns run on units of p.
func (d Def) Serves(p unit.ProviderID) bool {
	if d.Providers != nil {
		return slices.Contains(d.Providers, p) || slices.Contains(d.Providers, AnyProvider)
	}
	own := d.ProviderOrDefault()
	return own == AnyProvider || own == p
}

// ByRole is the intent holding role for units of p: a user's intent over
// the bundled one; the loader refuses two holders in one layer.
func (r *Registry) ByRole(role string, p unit.ProviderID) (Def, bool) {
	var found Def
	ok := false
	for _, d := range r.defs {
		if role == "" || d.Role != role || !d.Serves(p) {
			continue
		}
		if !d.Bundled {
			return d, true
		}
		if !ok {
			found, ok = d, true
		}
	}
	return found, ok
}

// Builder is the intent that builds what for units of p, such as the brief:
// the user's holder over the bundled one.
func (r *Registry) Builder(what string, p unit.ProviderID) (Def, bool) {
	if what == "" {
		return Def{}, false
	}
	var found Def
	ok := false
	for _, d := range r.defs {
		if d.Builds != what || !d.Serves(p) {
			continue
		}
		if !d.Bundled {
			return d, true
		}
		if !ok {
			found, ok = d, true
		}
	}
	return found, ok
}

// ProviderOrDefault is the provider whose units the intent's turns run on;
// the loader fills it from the profile's defaults when the manifest names
// none.
func (d Def) ProviderOrDefault() unit.ProviderID { return d.Provider }

// ForProvider is the intents whose turns run on units of p.
func (r *Registry) ForProvider(p unit.ProviderID) []Def {
	var out []Def
	for _, d := range r.defs {
		if d.Serves(p) {
			out = append(out, d)
		}
	}
	return out
}
