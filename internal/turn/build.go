package turn

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"text/template"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dpr "github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/github"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// PRReader reads a pull request and your reviews of it, in one read.
type PRReader interface {
	PR(ctx context.Context, ref github.Ref) (PR, error)
}

// Brief is the stored preview of a pull request a turn reads.
type Brief struct {
	Goal, Context, Kind, Size, Risk, RiskWhy string
	// Tags are key:value; Files are the changed paths the brief read.
	OpenQuestions, Tags, Files []string
	// HeadMoved says the pull request got new commits since the brief was
	// written; OlderFormat, that the loop's brief format changed since.
	HeadMoved, OlderFormat bool
	Run                    int64
	// Extra are the brief intent's extra fields, by name.
	Extra map[string]any
}

// Stored is what the loop keeps about a unit beyond its provider's read.
type Stored struct {
	// Brief is nil when none is stored.
	Brief *Brief
	// Teams are the teams a review was requested through, as <org>-<slug>.
	Teams []string
}

// FactsReader reads what the loop keeps about a unit; a unit with no
// brief and no teams reads the zero Stored.
type FactsReader interface {
	Facts(ctx context.Context, u unit.Unit) (Stored, error)
}

// GuideReader composes the guide a turn of kind reads for a pull request
// with facts f.
type GuideReader interface {
	Guide(kind profile.Kind, f profile.Facts) (profile.Guide, error)
}

// ProbeTitle is the title of a probe's pull request, which exists nowhere.
var ProbeTitle = brand.Name + " probe"

const noBrief = "none is stored for this pull request yet"

type Builder struct {
	Guides GuideReader
	// Facts reads the stored facts; nil reads nothing.
	Facts FactsReader
	// Intents is the registry the turn's intent comes from.
	Intents func() *intentload.Loaded
	// Viewer is the GitHub login of the user; nil leaves pr.mine false.
	Viewer func(context.Context) (string, error)
	// Config is the configuration for the turn's pull request.
	Config  config.Config
	Command string
	// ManagerBinary is the workspace manager's executable, which a probe never runs; "" for none.
	ManagerBinary string
	// Payloads read each provider's payload; github's is GitHub over a PRReader.
	Payloads map[unit.ProviderID]PayloadReader
	// Items nil renders no items.
	Items ItemsReader
	// Dossiers nil renders no dossier; a dry run reads none.
	Dossiers DossierReader
}

// DossierReader opens a unit's dossier for a session turn.
type DossierReader interface {
	Dossier(ctx context.Context, u unit.Unit) (Dossier, bool, error)
}

func (b Builder) Build(ctx context.Context, r Request) (Context, error) {
	l := b.Intents()
	d, err := definition(l, r.Intent)
	if err != nil {
		return Context{}, err
	}
	if !d.Serves(r.Unit.Provider) {
		return Context{}, fmt.Errorf("%s is a %s intent: it has no turn on a %s unit", d.Name, d.ProviderOrDefault(), r.Unit.Provider)
	}
	c := Context{
		Intent: r.Intent, Trigger: r.Trigger, Planned: r.Planned, Plain: r.Plain, Owned: r.Owned, Inputs: r.Inputs,
		Agent: r.Agent, Command: b.Command, ManagerBinary: b.ManagerBinary, History: r.History, Resume: r.Resume, Why: whyOf(r.Why),
	}
	if d.Context == "synthetic" {
		ref, err := dpr.ParseKey(r.Unit.Key)
		if err != nil {
			return Context{}, err
		}
		pr := &PR{URL: ref.URL(), Owner: ref.Owner, Repo: ref.Repo, Number: ref.Number}
		if d.Launch == builtin.LaunchProbe {
			pr.Title = ProbeTitle
		}
		c.Payload = pr
		return Complete(c, l)
	}
	if c.Payload, err = b.payload(ctx, r); err != nil {
		return Context{}, err
	}
	c.Unit, c.Wording = c.Payload.Unit(), c.Payload.Wording()
	c.WithoutProjectSettings = !b.Config.ProjectSettingsFor(string(r.Intent), PayloadFacts(c.Payload))
	c.Queued, c.Guidance, c.Local = r.Queued, r.Guidance, r.Local
	c.AutomatedReviewers = b.Config.GitHub.AutomatedReviewers
	c.Facts = TurnFacts{AddressReviews: b.Config.GitHub.AddressReviews, AddressChecks: b.Config.GitHub.AddressChecks,
		FollowupsWithoutWorkspace: b.Config.GitHub.FollowupsWithoutWorkspace, AddressWithoutWorkspace: b.Config.GitHub.AddressWithoutWorkspace}
	var st Stored
	if pr := c.PR(); pr != nil {
		c.Facts.CrossRepository, c.Facts.LastReviewVerdict = pr.CrossRepository, pr.LastReviewVerdict
		if b.Viewer != nil && readsMine(d, l.Templates[d.Name]) {
			viewer, err := b.Viewer(ctx)
			if err != nil {
				return Context{}, err
			}
			c.Facts.Mine = viewer != "" && strings.EqualFold(viewer, pr.Author)
		}
	}
	if b.Facts != nil {
		if st, err = b.Facts.Facts(ctx, c.Unit); err != nil {
			st, r.BriefMissing = Stored{}, "reading it failed ("+err.Error()+")"
		}
	}
	if b.Items != nil {
		if c.Items, err = b.Items.OpenItems(ctx, c.Unit); err != nil {
			return Context{}, err
		}
	}
	if b.Dossiers != nil && !r.Plain {
		d, ok, err := b.Dossiers.Dossier(ctx, c.Unit)
		if err != nil {
			return Context{}, err
		}
		if ok {
			c.Dossier = &d
		}
	}
	c.Brief = st.Brief
	if c.Brief == nil {
		c.BriefMissing = r.BriefMissing
		if c.BriefMissing == "" {
			c.BriefMissing = "none is stored for " + c.Wording.This + " yet"
		}
	}
	if c, err = Complete(c, l); err != nil {
		return Context{}, err
	}
	c.Facts.RequestedTeams = st.Teams
	return b.compose(c, l)
}

// payload is the request's payload, else what the unit provider's reader
// reads; a pull request's prior review is anchored to the loop's last
// verdict.
func (b Builder) payload(ctx context.Context, r Request) (Payload, error) {
	p := r.Payload
	if p == nil {
		read, ok := b.Payloads[r.Unit.Provider]
		if !ok {
			return nil, fmt.Errorf("no turn payload reader for the %s provider", r.Unit.Provider)
		}
		var err error
		if p, err = read.Payload(ctx, r); err != nil {
			return nil, err
		}
	}
	if pr, ok := p.(*PR); ok {
		ref, err := dpr.ParseKey(r.Unit.Key)
		if err != nil {
			return nil, err
		}
		p = anchored(*pr, ref, r.LastVerdict)
	}
	return p, nil
}

// compose reads the turn's guides: the intent's profile at the turn's
// stage, then each also_profile entry whose when holds, with the
// dimensions it pins.
func (b Builder) compose(c Context, l *intentload.Loaded) (Context, error) {
	d := c.Def
	if d.Profile == "" {
		return c, nil
	}
	dims := profile.Vocabulary(l.Registry)
	var err error
	c.Guide, err = b.Guides.Guide(profile.Kind(d.Profile), c.dimFacts(dims, map[string]string{dintent.DimStage: c.Round}))
	if err != nil {
		return Context{}, err
	}
	c.Guides = map[string]profile.Guide{d.Profile: c.Guide}
	also := l.Programs[d.Name].Also
	for i, a := range d.AlsoProfiles {
		if i < len(also) && also[i] != nil && !also[i].Eval(turnFacts{&c}) {
			continue
		}
		pins := map[string]string{dintent.DimStage: ""}
		maps.Copy(pins, a.Dims)
		g, err := b.Guides.Guide(profile.Kind(a.Profile), c.dimFacts(dims, pins))
		if err != nil {
			return Context{}, err
		}
		c.Guides[a.Profile] = g
	}
	return c, nil
}

// dimFacts are the values of the dimensions a guide is matched against,
// with the dimensions pins names set to their pinned values.
func (c Context) dimFacts(dims []profile.DimDef, pins map[string]string) profile.Facts {
	f := profile.Facts{Values: map[string]any{}}
	get := turnFacts{&c}
	for _, d := range dims {
		var v any
		pin, pinned := pins[string(d.Name)]
		switch {
		case pinned:
			v = pin
			if d.List {
				v = []string{pin}
			}
		case d.Extra != "":
			if c.Brief != nil {
				v = c.Brief.Extra[d.Extra]
			}
		case d.Brief:
			v = c.Brief.fact(d.Fact)
		default:
			v = get.Get(d.Fact)
		}
		if v != nil {
			f.Values[d.Fact] = v
		}
	}
	if br := c.Brief; br != nil {
		f.Tags, f.Files, f.HasBrief = br.Tags, br.Files, true
	}
	return f
}

// fact is the brief fact name of b; nil without a brief.
func (b *Brief) fact(name string) any {
	if b == nil {
		return nil
	}
	switch name {
	case "brief.goal":
		return b.Goal
	case "brief.kind":
		return b.Kind
	case "brief.size":
		return b.Size
	case "brief.risk":
		return b.Risk
	case "brief.tags":
		return b.Tags
	}
	return nil
}

var mineRef = regexp.MustCompile(`\bpr\.mine\b|\.Facts\.Mine\b`)

// readsMine says whether the intent's turn predicates or templates read
// pr.mine, so a turn that never does reads no viewer from GitHub.
func readsMine(d dintent.Def, t *template.Template) bool {
	srcs := []string{d.Records.Src}
	for _, c := range d.Headline.New {
		srcs = append(srcs, c.When.Src)
	}
	for _, a := range d.AlsoProfiles {
		srcs = append(srcs, a.When.Src)
	}
	if t != nil {
		for _, tt := range t.Templates() {
			if tt.Tree != nil && tt.Tree.Root != nil {
				srcs = append(srcs, tt.Tree.Root.String())
			}
		}
	}
	return slices.ContainsFunc(srcs, mineRef.MatchString)
}
