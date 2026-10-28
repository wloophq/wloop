// Package scope turns the configured sources into the people, teams and
// repositories the inventory follows, and the searches that find them.
package scope

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

const MaxQueryLen = 250

type TeamReader interface {
	TeamMembers(ctx context.Context, org, slug string) ([]string, error)
}

type Resolved struct {
	People []string // sorted, lower-case, unique: scope people plus team members
	Teams  []string
	// MyTeams are the teams of Teams the viewer belongs to.
	MyTeams []string
	Repos   []string // sorted, unique; the slow sweep appends discovered ones
	Watch   []string
	Viewer  string
}

type Query struct {
	Reason string // author, review-requested, team-review-requested, watch, repo, discover
	Q      string
}

func Resolve(ctx context.Context, cfg Config, viewer string, teams TeamReader) (Resolved, []error) {
	r := Resolved{Viewer: strings.ToLower(viewer)}
	var errs []error
	if r.Viewer == "" {
		errs = append(errs, fmt.Errorf("viewer login unknown: queries on my pull requests and review requests are skipped"))
	}
	for _, p := range cfg.People {
		r.People = append(r.People, strings.ToLower(p))
	}
	for _, t := range cfg.Teams {
		org, slug, ok := strings.Cut(t, "/")
		if !ok || org == "" || slug == "" || strings.Contains(slug, "/") {
			errs = append(errs, fmt.Errorf("team %q: want org/slug", t))
			continue
		}
		members, err := teams.TeamMembers(ctx, org, slug)
		if err != nil {
			errs = append(errs, fmt.Errorf("team %s: %w", t, err))
			continue
		}
		r.Teams = append(r.Teams, strings.ToLower(t))
		for _, m := range members {
			r.People = append(r.People, strings.ToLower(m))
			if r.Viewer != "" && strings.EqualFold(m, r.Viewer) {
				r.MyTeams = append(r.MyTeams, strings.ToLower(t))
			}
		}
	}
	for _, x := range cfg.Repos {
		r.Repos = append(r.Repos, strings.ToLower(x))
	}
	for _, w := range cfg.Watch {
		r.Watch = append(r.Watch, strings.ToLower(w))
	}
	r.Normalize()
	return r, errs
}

// Normalize sorts and dedupes every list.
func (r *Resolved) Normalize() {
	for _, s := range []*[]string{&r.People, &r.Teams, &r.MyTeams, &r.Repos, &r.Watch} {
		slices.Sort(*s)
		*s = slices.Compact(*s)
	}
}

func base(state string, since time.Time) string {
	parts := []string{"is:pr"}
	if state != "" {
		parts = append(parts, state)
	}
	parts = append(parts, "archived:false")
	if !since.IsZero() {
		parts = append(parts, "updated:>="+since.UTC().Format("2006-01-02T15:04:05Z"))
	}
	return strings.Join(parts, " ")
}

// chunks renders prefix+value terms after base, splitting into as many
// queries as MaxQueryLen requires.
func chunks(reason, base, prefix string, vals []string) []Query {
	var out []Query
	cur := base
	for _, v := range vals {
		term := " " + prefix + v
		if cur != base && len(cur)+len(term) > MaxQueryLen {
			out = append(out, Query{Reason: reason, Q: cur})
			cur = base
		}
		cur += term
	}
	if cur != base {
		out = append(out, Query{Reason: reason, Q: cur})
	}
	return out
}

// Queries builds the searches of a fast sweep. With a zero since they list
// the open pull requests; otherwise they return the pull requests updated at
// or after since in any state, so a close or merge in the window is seen.
// Every query stays under MaxQueryLen.
func (r Resolved) Queries(since time.Time) []Query {
	state := ""
	if since.IsZero() {
		state = "is:open"
	}
	b := base(state, since)
	var qs []Query
	qs = append(qs, chunks("author", b, "author:", r.authors())...)
	if r.Viewer != "" {
		qs = append(qs, Query{Reason: "review-requested", Q: b + " user-review-requested:" + r.Viewer})
	}
	for _, t := range r.Teams {
		qs = append(qs, Query{Reason: "team-review-requested", Q: b + " team-review-requested:" + t})
	}
	qs = append(qs, chunks("watch", b, "author:", r.Watch)...)
	qs = append(qs, chunks("watch", b, "user-review-requested:", r.Watch)...)
	qs = append(qs, chunks("repo", b, "repo:", r.Repos)...)
	return qs
}

// DiscoveryQueries find where the area opened pull requests since the given
// instant, any state.
func (r Resolved) DiscoveryQueries(since time.Time) []Query {
	return chunks("discover", base("", since), "author:", r.People)
}

// ClosedQueries find the pull requests of the area and of the scope
// repositories closed or merged since the given instant.
func (r Resolved) ClosedQueries(since time.Time) []Query {
	b := base("is:closed", since)
	var qs []Query
	qs = append(qs, chunks("author", b, "author:", r.authors())...)
	qs = append(qs, chunks("repo", b, "repo:", r.Repos)...)
	return qs
}

func (r Resolved) authors() []string {
	authors := slices.Clone(r.People)
	if r.Viewer != "" {
		authors = append(authors, r.Viewer)
	}
	slices.Sort(authors)
	return slices.Compact(authors)
}

func (r Resolved) Reasons(author, ownerRepo string, requested []string) []string {
	var out []string
	author, ownerRepo = strings.ToLower(author), strings.ToLower(ownerRepo)
	if author != "" && (slices.Contains(r.People, author) || author == r.Viewer) {
		out = append(out, "author")
	}
	if slices.Contains(r.Repos, ownerRepo) {
		out = append(out, "repo")
	}
	for _, q := range requested {
		q = strings.ToLower(q)
		switch {
		case q != "" && q == r.Viewer:
			out = append(out, "review-requested")
		case strings.HasPrefix(q, "team:") && slices.Contains(r.Teams, strings.TrimPrefix(q, "team:")):
			out = append(out, "team-review-requested")
		case slices.Contains(r.Watch, q):
			out = append(out, "watch")
		}
	}
	if slices.Contains(r.Watch, author) {
		out = append(out, "watch")
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Config is the scope [plugins.github.scope] configures.
type Config struct {
	People []string `json:"people"`
	Teams  []string `json:"teams"`
	Repos  []string `json:"repos"`
	Watch  []string `json:"watch"`
}
