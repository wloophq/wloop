// Package intent holds the definition of an intent, as its manifest declares
// it, and the registry of every loaded intent.
package intent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Name is an intent's name: the directory of its manifest.
type Name string

// DimRound is the profile dimension that selects by review round.
const DimRound = "round"

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
	Name         Name
	Label        string
	Summary      string
	Key          string
	Verb         string // the gtd verb its action fulfills; "" for none
	UserFacing   bool
	Workspace    string // review | author | pr_else_main | given
	Session      Session
	Context      string // pr | synthetic
	Launch       string // "" | probe
	Policy       string // round | probe
	Intake       bool
	StartLine    string
	OwnerOnly    bool
	Answerable   bool
	Board        string // review | author | none
	DrivesRow    bool
	Reports      bool
	Reconcile    string // "" | my_review
	Brief        Brief
	Profile      string // "" means none
	Dims         []string
	Records      Pred
	Prepare      string
	OnSent       []string
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
	// DraftApproval is the manifest's [draft_approval] block; nil when absent.
	DraftApproval *draft.Approval
	// Modes are turn.Mode values; default ["session"].
	Modes []string
}

// Runs says whether the intent's turns may run in mode, a turn.Mode value; nil Modes run as session only.
func (d Def) Runs(mode string) bool {
	if d.Modes == nil {
		return mode == "session"
	}
	return slices.Contains(d.Modes, mode)
}

type Session struct {
	Strategy      string // reuse_live | new
	Title         string
	CloseWhenDone bool
}

type Brief struct{ Render, Wait bool }

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
	Profile, Round, Define, SourcePrefix string
	When                                 Pred
}

type Action struct {
	Group            string
	Surfaces         []string
	Visible          Pred
	Enabled          Pred
	ReasonOff        Text
	Primary          Pred
	Confirm          Text
	Hint             Text
	NoteWhenQuestion string
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

type Resume struct{ HeadMoved string }

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
	own := d.ProviderOrDefault()
	return own == AnyProvider || own == p
}

// ProviderOrDefault is the provider whose units the intent's turns run on.
func (d Def) ProviderOrDefault() unit.ProviderID {
	if d.Provider == "" {
		return unit.GitHub
	}
	return d.Provider
}

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
