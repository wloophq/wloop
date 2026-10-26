package prrow

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/situation"
)

// facetValues are the row's values of every facet of Facets, the group
// key first; a time facet holds RFC 3339 instants the client buckets.
func (d Deriver) facetValues(r api.Row, p Payload) map[string][]string {
	brief := api.BriefSummary{}
	if p.Brief != nil {
		brief = *p.Brief
	}
	author := []string{p.Author}
	if mineAuthor(p) {
		author = []string{"me", p.Author}
	}
	out := map[string][]string{
		FacetRepo:    {p.Repo},
		FacetAuthor:  author,
		FacetWait:    waitCats(r, p),
		FacetWaitsOn: waitingWho(r, p),
		FacetKind:    {string(r.Kind)},
		FacetType:    {or(brief.Kind, "none")},
		FacetRisk:    {known(brief.Risk, risks)},
		FacetSize:    {known(brief.Size, sizes)},
		FacetStatus:  {or(p.Workspace.Status, "none")},
		FacetTag:     slices.Clone(brief.Tags),
		FacetIs:      rowFlags(r, p),
		FacetCI:      ciValues(p),
		FacetScope:   slices.Clone(p.Reasons),
		FacetAge:     {stamp(r.Summary.Since)},
		FacetLane:    {boardLane(r, p)},
		FacetDue:     dueTimes(r, p),
	}
	maps.Copy(out, extraValues(registryOf(d.Loaded), brief.Extra))
	if p.Facts != nil && !p.Facts.CreatedAt.IsZero() {
		out[FacetOpened] = []string{stamp(p.Facts.CreatedAt)}
	}
	for id, vs := range out {
		vs = slices.DeleteFunc(vs, func(v string) bool { return v == "" })
		if len(vs) == 0 {
			delete(out, id)
			continue
		}
		out[id] = vs
	}
	return out
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func known(v string, of []string) string {
	if slices.Contains(of, v) {
		return v
	}
	return "none"
}

// resolveWho names the author "me" when the viewer is the author.
func resolveWho(p Payload, w string) string {
	if w != "author" {
		return w
	}
	if mineAuthor(p) {
		return "me"
	}
	return p.Author
}

func waitingWho(r api.Row, p Payload) []string {
	var out []string
	for w := range strings.SplitSeq(r.Summary.WaitsOn, ", ") {
		if w != "" && w != "nobody" {
			out = append(out, resolveWho(p, w))
		}
	}
	return out
}

// waitCats are the classes of whom a row waits on: me, the loop, a
// reviewer, the author, or nobody.
func waitCats(r api.Row, p Payload) []string {
	if k := situation.Kind(r.Kind); k == situation.KindLoopWorking || k == situation.KindLoopWaiting {
		return []string{"loop"}
	}
	cats := map[string]bool{}
	for w := range strings.SplitSeq(r.Summary.WaitsOn, ", ") {
		switch {
		case w == "" || w == "nobody":
		case resolveWho(p, w) == "me":
			cats["me"] = true
		case w == "author":
			cats["author"] = true
		default:
			cats["reviewer"] = true
		}
	}
	if len(cats) == 0 {
		return []string{"nobody"}
	}
	var out []string
	for _, c := range waitOrder {
		if cats[c] {
			out = append(out, c)
		}
	}
	return out
}

// boardLane puts a row in one lane of the board: "In loop" holds the
// stalled rows that have a review workspace.
func boardLane(r api.Row, p Payload) string {
	switch {
	case slices.Contains(r.Views, api.ViewNeedsMe):
		return "me"
	case slices.Contains(r.Views, api.ViewLoop):
		return "loop"
	}
	switch situation.Kind(r.Kind) {
	case situation.KindMerge:
		return "merge"
	case situation.KindReview, situation.KindReviewerBack:
		return "reviewer"
	}
	if slices.Contains(waitingWho(r, p), "me") {
		return "me"
	}
	return "author"
}

func rowFlags(r api.Row, p Payload) []string {
	o := p.Override
	on := map[string]bool{
		"stalled": slices.Contains(r.Views, api.ViewStalled), "needs-me": slices.Contains(r.Views, api.ViewNeedsMe),
		"in-loop": slices.Contains(r.Views, api.ViewLoop), "approved": p.Approved, "paused": o.Hold, "muted": p.Mute != nil,
		"adopted": o.Adopted, "tracked": o.Tracked,
	}
	var out []string
	for _, f := range flags {
		if on[f] {
			out = append(out, f)
		}
	}
	return out
}

func ciValues(p Payload) []string {
	var c api.CheckFacts
	if p.Facts != nil {
		c = p.Facts.Checks
	}
	return append([]string{or(strings.ToLower(c.State), "none")}, c.Failing...)
}

// dueTimes are every instant the row waits for, past or not: the gate's
// deadline, the end of a mute, the brief's retry and the facts' next
// instants, the soonest first.
func dueTimes(r api.Row, p Payload) []string {
	var at []time.Time
	if r.Pending != nil {
		at = append(at, r.Pending.Deadline)
	}
	at = append(at, p.Override.MutedUntil, p.briefRetryAt)
	if p.Facts != nil {
		for _, n := range p.Facts.Next {
			at = append(at, n.At)
		}
	}
	at = slices.DeleteFunc(at, time.Time.IsZero)
	slices.SortFunc(at, time.Time.Compare)
	at = slices.CompactFunc(at, time.Time.Equal)
	out := make([]string, len(at))
	for i, t := range at {
		out[i] = stamp(t)
	}
	return out
}
