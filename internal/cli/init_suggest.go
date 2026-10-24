package cli

import (
	"cmp"
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
)

// reviewedWindow is how far back the scope suggestion reads your reviews.
const reviewedWindow = 30 * 24 * time.Hour

const maxSuggestions = 10

// suggestProjectsDir is the directory that holds most of the repositories
// the workspace manager knows, "" when it knows none.
func (e *Env) suggestProjectsDir(ctx context.Context) string {
	repos, err := e.WS.Repos(ctx)
	if err != nil {
		return ""
	}
	return commonParent(repos)
}

func commonParent(repos []wsdomain.Repo) string {
	count := map[string]int{}
	for _, r := range repos {
		if r.Path != "" {
			count[filepath.Dir(filepath.Clean(r.Path))]++
		}
	}
	if ranked := mostFrequent(count); len(ranked) > 0 {
		return ranked[0]
	}
	return ""
}

// mostFrequent lists count's keys, most frequent first, then by name.
func mostFrequent(count map[string]int) []string {
	keys := slices.Collect(maps.Keys(count))
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(count[b]-count[a], strings.Compare(a, b)) })
	return keys
}

// reviewedRepos are the repositories of the pull requests you reviewed in
// the last reviewedWindow, most reviewed first.
func (e *Env) reviewedRepos(ctx context.Context) ([]string, error) {
	since := e.Now().Add(-reviewedWindow).UTC().Format("2006-01-02")
	out, err := e.GH.Run(ctx, "search", "prs", "--reviewed-by=@me", "--updated=>="+since, "--limit", "100",
		"--json", "repository", "--jq", ".[].repository.nameWithOwner")
	if err != nil {
		return nil, err
	}
	count := map[string]int{}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.ToLower(strings.TrimSpace(l)); strings.Count(l, "/") == 1 {
			count[l]++
		}
	}
	ranked := mostFrequent(count)
	return ranked[:min(maxSuggestions, len(ranked))], nil
}

// userTeams are the teams of your organizations you belong to, as org/slug.
func (e *Env) userTeams(ctx context.Context) ([]string, error) {
	out, err := e.GH.Run(ctx, "api", "user/teams", "--paginate", "--jq", `.[] | .organization.login + "/" + .slug`)
	if err != nil {
		return nil, err
	}
	var teams []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.ToLower(strings.TrimSpace(l)); strings.Count(l, "/") == 1 && !slices.Contains(teams, l) {
			teams = append(teams, l)
		}
	}
	slices.Sort(teams)
	return teams, nil
}
