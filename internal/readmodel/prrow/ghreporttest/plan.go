package ghreporttest

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/sweep"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	ghplugin "github.com/wloophq/wloop/plugins/github"
	"github.com/wloophq/wloop/plugins/github/plan"
	ghpr "github.com/wloophq/wloop/plugins/github/pr"
	ghstore "github.com/wloophq/wloop/plugins/github/store"
)

// PlanEnv is what the plugin's planner reads of the host besides the pull
// requests: its round state, books, workspaces and checkouts.
type PlanEnv struct {
	Rounds *round.Store
	Pause  round.Pause
	Books  map[unit.Unit]turn.Book
	// Snapshot nil is a listing that could not be read.
	Snapshot *wsdomain.Snapshot
	Git      plan.Git
	Config   config.Config
}

// Planned is the planner's report of every pull request of ins under env
// and pe: its snapshot with the planner's facts and data, by unit.
func Planned(env Env, pe PlanEnv, ins []Input) map[unit.Unit]src.Snapshot {
	pl := plan.New(context.Background(), planInputs(env, pe, ins))
	out := map[unit.Unit]src.Snapshot{}
	for _, x := range ins {
		s := Snapshot(env, x)
		ref := PluginPR(x.PR).Ref
		res, err := pl.Derive(ref)
		if err != nil {
			panic(fmt.Sprintf("ghreporttest: %v", err))
		}
		var d ghplugin.Data
		if err := json.Unmarshal(s.Data, &d); err != nil {
			panic(fmt.Sprintf("ghreporttest: %v", err))
		}
		d.Plan = &ghplugin.PlanData{Rank: pl.RankOf(ref, res), Board: pl.BoardOf(ref, res)}
		if f := cmp.Or(x.PlanFacts, x.Facts); f != nil {
			det := DetailOf(*f)
			d.Detail, d.DetailVersion, d.DetailReadAt = &det, det.Version, det.ReadAt
		}
		raw, err := json.Marshal(d)
		if err != nil {
			panic(fmt.Sprintf("ghreporttest: %v", err))
		}
		s.Data = raw
		s.Facts = append(s.Facts, pl.Facts(ref, res)...)
		out[x.PR.Ref.Unit()] = s
	}
	return out
}

// PlanOf is the plan the plugin's planner makes of ref among ins over st,
// the host's round state of ref.
func PlanOf(env Env, pe PlanEnv, ins []Input, ref pr.Ref, st src.RoundState) src.Plan {
	in := planInputs(env, pe, ins)
	r := PluginPR(dinv.PR{Ref: ref}).Ref
	in.Rounds[r] = st
	pl := plan.New(context.Background(), in)
	res, err := pl.Derive(r)
	if err != nil {
		panic(fmt.Sprintf("ghreporttest: %v", err))
	}
	return pl.Plan(r, res)
}

func planInputs(env Env, pe PlanEnv, ins []Input) plan.Inputs {
	in := plan.Inputs{Now: env.Now, Viewer: env.Viewer, Details: map[ghpr.Ref]ghstore.Detail{}, Adopted: map[ghpr.Ref]bool{},
		Books: map[ghpr.Ref]src.Book{}, Rounds: map[ghpr.Ref]src.RoundState{}, Events: map[ghpr.Ref][]ghpr.Event{}, Git: pe.Git,
		Cfg: PlanConfig(pe.Config)}
	if pe.Snapshot == nil {
		in.Unlisted = true
	} else {
		in.Listing = ListingOf(pe.Snapshot)
	}
	rounds := pe.Rounds
	if rounds == nil {
		rounds = round.New()
	}
	for _, x := range ins {
		p := PluginPR(x.PR)
		in.Rows = append(in.Rows, p)
		if f := cmp.Or(x.PlanFacts, x.Facts); f != nil {
			in.Details[p.Ref] = DetailOf(*f)
		}
		in.Adopted[p.Ref] = x.Adopted
		for _, e := range x.Events {
			in.Events[p.Ref] = append(in.Events[p.Ref], ghpr.Event{PR: p.Ref, Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At, Data: e.Data})
		}
		if b, ok := pe.Books[x.PR.Ref.Unit()]; ok {
			in.Books[p.Ref] = BookOf(b)
		}
		in.Rounds[p.Ref] = RoundStateOf(rounds, x.PR.Ref.String(), env.Now, x.Held || pe.Pause.Holds(x.PR.Ref.Owner, x.PR.Ref.Repo))
	}
	for u, b := range pe.Books {
		if ref, err := ghpr.ParseKey(u.Key); err == nil {
			if _, ok := in.Books[ref]; !ok {
				in.Books[ref] = BookOf(b)
			}
		}
	}
	return in
}

// PlanConfig is cfg as the plugin's planner reads it.
func PlanConfig(cfg config.Config) plan.Config {
	gh := cfg.GitHub
	out := plan.Config{ReviewPrefix: gh.ReviewPrefix, Owners: gh.Owners, TrustedAuthors: gh.TrustedAuthors,
		FollowupsWithoutWorkspace: gh.FollowupsWithoutWorkspace, AddressWithoutWorkspace: gh.AddressWithoutWorkspace,
		AddressReviews: gh.AddressReviews, AddressSkipReviewers: gh.AddressSkipReviewers, AddressChecks: gh.AddressChecks,
		IgnoreChecks: gh.IgnoreChecks, AddressConflicts: gh.AddressConflicts, Fast: time.Duration(cfg.Sweep.Fast), Slow: time.Duration(cfg.Sweep.Slow)}
	for _, v := range cfg.Views {
		if c := v.Columns[config.ColumnCompleted]; c != "" {
			out.Completed = append(out.Completed, c)
		}
	}
	for _, p := range cfg.Projects {
		out.Projects = append(out.Projects, plan.Project{Match: p.Match, TrustedAuthors: p.GitHub.TrustedAuthors,
			FollowupsWithoutWorkspace: p.GitHub.FollowupsWithoutWorkspace, AddressWithoutWorkspace: p.GitHub.AddressWithoutWorkspace})
	}
	return out
}

// ListingOf is snap as the host's broker lists it to a plugin.
func ListingOf(snap *wsdomain.Snapshot) src.Listing {
	out := src.Listing{Partial: partial(snap)}
	byID := map[string]wsdomain.Repo{}
	for _, r := range snap.Repos {
		byID[r.ID] = r
		out.Repos = append(out.Repos, src.Repo{ID: r.ID, Remote: r.RemoteKey, Path: r.Path, DefaultBranch: r.DefaultBranch})
	}
	for _, w := range snap.Workspaces {
		r := byID[w.RepoID]
		name := ""
		owner, n, ok := r.OwnerName()
		if ok {
			name = owner + "/" + n
		}
		lw := src.Workspace{Manager: w.Manager, ID: w.ID, Repo: name, RepoID: w.RepoID, Path: w.Path, DisplayName: w.DisplayName,
			Comment: w.Comment, Branch: w.Branch, PushBranch: w.PushBranch, Status: w.Status, IsMain: w.IsMain, Local: w.HostID == wsdomain.LocalHost}
		if ok && w.LinkedPR != 0 {
			lw.Units = append(lw.Units, ghpr.Ref{Owner: owner, Repo: n, Number: w.LinkedPR}.Unit())
		}
		out.Workspaces = append(out.Workspaces, lw)
	}
	return out
}

// partial says whether the listing may miss a workspace, as the sweep read
// a merged snapshot: each manager answers for its own.
func partial(snap *wsdomain.Snapshot) bool {
	split := func(ws, ts wsdomain.Scope) bool {
		return !ws.Complete || !ts.Complete || ws.RuntimeID == "" || ws.RuntimeID != ts.RuntimeID
	}
	if len(snap.Scopes) == 0 {
		return split(snap.WorkspaceScope, snap.TerminalScope)
	}
	for _, sc := range snap.Scopes {
		if !sc.Workspaces.Complete || sc.Terminals.Complete && split(sc.Workspaces, sc.Terminals) {
			return true
		}
	}
	return false
}

// RoundStateOf is st's round state of key at now, as the host's broker
// serves it.
func RoundStateOf(st *round.Store, key string, now time.Time, paused bool) src.RoundState {
	p := st.PRs[key]
	day := st.Days[round.DayKey(now)]
	marks := map[string]string{}
	for k, v := range p.Marks {
		marks[k] = v
	}
	return src.RoundState{Marks: marks, SentAt: p.SentAt, AgentHome: p.AgentHome, Today: day.PerPR[key], DayTotal: day.Total, Paused: paused}
}

// BookOf is b as the host's broker serves it.
func BookOf(b turn.Book) src.Book {
	out := src.Book{Unit: ghpr.Ref{}.Unit()}
	if ref, err := ghpr.ParseKey(b.Unit.Key); err == nil {
		out.Unit = ref.Unit()
	}
	for _, t := range b.Turns {
		out.Turns = append(out.Turns, src.Turn{Seq: t.Seq, ID: t.ID, Intent: string(t.Intent), Role: t.Role, Workflow: t.Workflow, State: string(t.State),
			Head: t.Head, Session: t.Session, SentAt: t.SentAt, StartedAt: t.StartedAt, EndedAt: t.EndedAt, At: t.SentAt})
	}
	for _, rec := range b.Records {
		raw, err := json.Marshal(rec.Record)
		if err != nil {
			panic(fmt.Sprintf("ghreporttest: %v", err))
		}
		out.Records = append(out.Records, src.TurnRecord{ID: rec.ID, TurnSeq: rec.TurnSeq, Head: rec.Head, At: rec.At, Record: raw})
	}
	return out
}

// DetailOf is f's full read as the plugin's store keeps it.
func DetailOf(f dinv.PlanFacts) ghstore.Detail {
	var d ghstore.Detail
	raw, err := json.Marshal(struct {
		AuthorAssociation, Visibility, NewestChangesRequestOid string
		Labels                                                 []string
		Commits                                                []pr.Commit
		Reviews, LatestReviews                                 []pr.Review
		Comments                                               []pr.Comment
		Checks                                                 []pr.Check
		ThreadsTotal                                           int
		OpenThreads                                            []pr.Thread
	}{f.AuthorAssociation, f.Visibility, f.NewestChangesRequestOid, f.Labels, f.Commits, f.Reviews, f.LatestReviews, f.Comments, f.Checks,
		f.ThreadsTotal, f.OpenThreads})
	if err == nil {
		err = json.Unmarshal(raw, &d)
	}
	if err != nil {
		panic(fmt.Sprintf("ghreporttest: %v", err))
	}
	d.ThreadsRead = true
	if !f.ReadAt.IsZero() {
		d.Version, d.ReadAt = ghstore.DetailVersion, f.ReadAt
	}
	return d
}

// Branches are what a first round's review workspace would find of the
// pull request's head branch in the local checkout.
type Branches interface {
	LocalBranchTip(ctx context.Context, dir, branch string) (string, error)
	CheckedOutAt(ctx context.Context, dir, branch string) (string, error)
	HasCommit(ctx context.Context, dir, oid string) (bool, error)
	IsAncestor(ctx context.Context, dir, ancestor, commit string) (bool, error)
}

// GitOf is the planner's git over a host test's checkouts.
func GitOf(local sweep.LocalGit, branches Branches) plan.Git { return testGit{local, branches} }

type testGit struct {
	local    sweep.LocalGit
	branches Branches
}

func (g testGit) Upstream(ctx context.Context, dir string) (string, error) {
	if g.local == nil {
		return "", nil
	}
	return g.local.Upstream(ctx, dir)
}

func (g testGit) LocalBranchTip(ctx context.Context, dir, branch string) (string, error) {
	if g.branches == nil {
		return "", nil
	}
	return g.branches.LocalBranchTip(ctx, dir, branch)
}

func (g testGit) CheckedOutAt(ctx context.Context, dir, branch string) (string, error) {
	if g.branches == nil {
		return "", nil
	}
	return g.branches.CheckedOutAt(ctx, dir, branch)
}

func (g testGit) HasCommit(ctx context.Context, dir, oid string) (bool, error) {
	if g.branches == nil {
		return false, nil
	}
	return g.branches.HasCommit(ctx, dir, oid)
}

func (g testGit) IsAncestor(ctx context.Context, dir, ancestor, commit string) (bool, error) {
	if g.branches == nil {
		return false, nil
	}
	return g.branches.IsAncestor(ctx, dir, ancestor, commit)
}

// Settled is what the plugin's planner leaves behind on ins under env and
// pe: the marks it sets, "" removing one, by pull request, and the events
// it records.
func Settled(env Env, pe PlanEnv, ins []Input) (map[pr.Ref]map[string]string, []dinv.Event) {
	pl := plan.New(context.Background(), planInputs(env, pe, ins))
	marks := map[pr.Ref]map[string]string{}
	var evs []dinv.Event
	for _, x := range ins {
		ref := PluginPR(x.PR).Ref
		res, err := pl.Derive(ref)
		if err != nil {
			panic(fmt.Sprintf("ghreporttest: %v", err))
		}
		s := pl.Settle(ref, res)
		if len(s.Marks) > 0 {
			marks[x.PR.Ref] = s.Marks
		}
		for _, e := range s.Events {
			evs = append(evs, dinv.Event{PR: x.PR.Ref, Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At, Data: e.Data})
		}
	}
	return marks, evs
}
