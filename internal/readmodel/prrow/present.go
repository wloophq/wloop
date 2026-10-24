package prrow

import (
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/situation"
)

var _ readmodel.Presenter = (*Provider)(nil)

// View ids the GitHub plugin serves besides api's core ids.
const (
	ViewPlan    = "plan"
	ViewDash    = "dash"
	ViewExplore = "explore"
	ViewRecent  = "recent"
)

const (
	surfaceWeb     = "web"
	surfaceTUI     = "tui"
	surfaceMenubar = "menubar"
)

var views = []api.ViewSpec{
	{ID: api.ViewNeedsMe, Label: "Needs me", Rows: true, Surfaces: []string{surfaceWeb, surfaceTUI}},
	{ID: api.ViewStalled, Label: "Stalled", Rows: true, Surfaces: []string{surfaceWeb}},
	{ID: api.ViewMine, Label: "My PRs", Rows: true, Surfaces: []string{surfaceWeb, surfaceTUI}},
	{ID: api.ViewLoop, Label: "In loop", Rows: true, Surfaces: []string{surfaceWeb, surfaceTUI, surfaceMenubar}},
	{ID: ViewPlan, Label: "Plan", Surfaces: []string{surfaceWeb, surfaceTUI}},
	{ID: api.ViewApproved, Label: "Approved", Rows: true, Surfaces: []string{surfaceWeb}},
	{ID: ViewDash, Label: "Backlog", Surfaces: []string{surfaceWeb}},
	{ID: api.ViewDone, Label: "Done", Rows: true, Surfaces: []string{surfaceWeb}},
	{ID: api.ViewAll, Label: "All", Rows: true, Surfaces: []string{surfaceWeb}},
	{ID: ViewExplore, Label: "Explore", Surfaces: []string{surfaceWeb}},
	{ID: ViewRecent, Label: "Recent", Surfaces: []string{surfaceWeb}},
}

// Views are today's cockpit views, in its order.
func (p *Provider) Views() []api.ViewSpec { return ViewSpecs() }

// ViewSpecs are the served views of Views.
func ViewSpecs() []api.ViewSpec {
	out := make([]api.ViewSpec, len(views))
	for i, v := range views {
		v.Provider, v.Order = string(unit.GitHub), i
		if v.Rows && v.ID != api.ViewAll {
			v.Unit, v.UnitSort = "Pull request", FacetRepo
		}
		out[i] = v
	}
	return out
}

// Situations are the GitHub kinds with their labels, ranked as the cockpit
// orders them.
func (p *Provider) Situations() []api.SituationSpec { return SituationSpecs() }

// SituationSpecs are the served situations of Situations.
func SituationSpecs() []api.SituationSpec {
	out := make([]api.SituationSpec, len(situation.Order))
	for i, k := range situation.Order {
		out[i] = api.SituationSpec{Provider: string(unit.GitHub), Kind: api.Kind(k), Label: k.Label(), Rank: i}
	}
	return out
}

// Facet ids of the GitHub rows.
const (
	FacetRepo    = "repo"
	FacetAuthor  = "author"
	FacetWait    = "wait"
	FacetWaitsOn = "waits-on"
	FacetKind    = "kind"
	FacetType    = "type"
	FacetRisk    = "risk"
	FacetSize    = "size"
	FacetStatus  = "status"
	FacetTag     = "tag"
	FacetIs      = "is"
	FacetCI      = "ci"
	FacetOpened  = "opened"
	FacetScope   = "scope"
	FacetAge     = "age"
	FacetLane    = "lane"
	FacetDue     = "due"
)

var (
	sizes     = []string{"xs", "s", "m", "l", "xl"}
	risks     = []string{"low", "medium", "high"}
	waitOrder = []string{"me", "loop", "reviewer", "author", "nobody"}
	ciOrder   = []string{"failure", "pending", "success"}
	lanes     = []string{"me", "reviewer", "author", "merge", "loop"}
	// flags are the clock-free flags of facet is, in the cockpit's order.
	flags = []string{"stalled", "needs-me", "in-loop", "approved", "paused", "muted", "adopted", "tracked"}
)

var openedBuckets = []api.Bucket{
	{ID: "1d", Label: "0-1 days", Within: "24h"},
	{ID: "3d", Label: "1-3 days", Within: "72h"},
	{ID: "7d", Label: "3-7 days", Within: "168h"},
	{ID: "2w", Label: "1-2 weeks", Within: "336h"},
	{ID: "4w", Label: "2-4 weeks", Within: "672h"},
	{ID: "older", Label: "over 4 weeks"},
}

// dueBuckets read an upcoming instant: "due" within the next 24 hours.
var dueBuckets = []api.Bucket{
	{ID: "later", Label: "later", Within: "-24h"},
	{ID: "due", Label: "due", Within: "1ms"},
	{ID: "past", Label: "past"},
}

// Facets are today's cockpit fields, in its order, plus the time facet due
// that the is flag "due" reads.
func (p *Provider) Facets() []api.FacetSpec { return FacetSpecs() }

// FacetSpecs are the served facets of Facets.
func FacetSpecs() []api.FacetSpec {
	kindLabels := map[string]string{}
	kindRank := make([]string, len(situation.Order))
	for i, k := range situation.Order {
		kindLabels[string(k)], kindRank[i] = k.Label(), string(k)
	}
	sizeLabels := map[string]string{}
	for _, s := range sizes {
		sizeLabels[s] = map[string]string{"xs": "XS", "s": "S", "m": "M", "l": "L", "xl": "XL"}[s]
	}
	openedRank := make([]string, len(openedBuckets))
	openedLabels := map[string]string{}
	for i, b := range openedBuckets {
		openedRank[i], openedLabels[b.ID] = b.ID, b.Label
	}
	out := []api.FacetSpec{
		{ID: FacetRepo, Label: "Repo", Group: true, Sort: "text", Match: "suffix", Menu: true},
		{ID: FacetAuthor, Label: "Author", Group: true, Sort: "rank", Rank: []string{"me"}, Menu: true},
		{ID: FacetWait, Label: "Waits on (role)", Group: true, Sort: "rank", Rank: waitOrder, Menu: true,
			Labels: map[string]string{"me": "Waiting on me", "loop": "In loop", "reviewer": "Waiting on a reviewer", "author": "Waiting on the author", "nobody": "Waiting on nobody"}},
		{ID: FacetWaitsOn, Label: "Waits on (person)", Match: "team", Menu: true},
		{ID: FacetKind, Label: "Situation", Group: true, Sort: "rank", Rank: kindRank, Labels: kindLabels, Menu: true},
		{ID: FacetType, Label: "Type", Group: true, Sort: "text", Empty: "no brief", Menu: true},
		{ID: FacetRisk, Label: "Risk", Group: true, Sort: "rank", Rank: []string{"high", "medium", "low"}, Empty: "no brief", Menu: true},
		{ID: FacetSize, Label: "Size", Group: true, Sort: "rank", Rank: sizes, Labels: sizeLabels, Empty: "no brief", Menu: true},
		{ID: FacetStatus, Label: "Workspace", Group: true, Sort: "text", Empty: "no workspace", Menu: true},
		{ID: FacetTag, Label: "Tag", Match: "prefix", Menu: true},
		{ID: FacetIs, Label: "Flags", Menu: true},
		{ID: FacetCI, Label: "Checks", Group: true, Rank: ciOrder, Empty: "no checks data", Menu: true},
		{ID: FacetOpened, Label: "Opened", Group: true, Sort: "time", Rank: openedRank, Labels: openedLabels, Empty: "no opening date", Menu: true, Buckets: openedBuckets},
		{ID: FacetScope, Label: "In scope as", Menu: true},
		{ID: FacetAge, Label: "Waiting for", Sort: "time", Menu: true, Buckets: openedBuckets},
		{ID: FacetLane, Label: "Board lane", Group: true, Rank: lanes,
			Labels: map[string]string{"me": "Needs me", "reviewer": "Waiting on reviewer", "author": "Waiting on author", "merge": "Can merge", "loop": "In loop"}},
		{ID: FacetDue, Label: "Due", Sort: "time", Buckets: dueBuckets},
	}
	for i := range out {
		out[i].Provider = string(unit.GitHub)
	}
	return out
}
