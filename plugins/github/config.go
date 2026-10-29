package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/collect"
	"github.com/wloophq/wloop/plugins/github/plan"
	"github.com/wloophq/wloop/plugins/github/scope"
	"github.com/wloophq/wloop/plugins/github/situation"
)

// Defaults the configuration falls back to when it says nothing.
const (
	defaultFast     = 2 * time.Minute
	defaultSlow     = time.Hour
	defaultFraction = 0.5
	// defaultMaxDiffBytes is brief.max_input_bytes' default.
	defaultMaxDiffBytes = 60000

	defaultStalledReview = 48 * time.Hour
	defaultStalledReply  = 24 * time.Hour
	defaultStalledMerge  = 24 * time.Hour
)

// needsMeColumn is the semantic status of the board column a workspace
// waiting on the user sits in.
const needsMeColumn = "needs-me"

// completedColumn is the semantic status of the board column a finished
// review workspace sits in.
const completedColumn = "completed"

// settings are what the plugin reads of the configuration: its
// [plugins.github] table and the host keys its manifest's config_reads
// names.
type settings struct {
	Collect collect.Config
	Fast    time.Duration
	// Ignore are who a row never waits on: the automated reviewers and the
	// reviewers an address round skips.
	Ignore  []string
	Stalled situation.Thresholds
	Plan    plan.Config
	// MaxDiffBytes bounds the diff a brief reads.
	MaxDiffBytes int
}

type pluginTable struct {
	ReviewPrefix         *string      `json:"review_prefix"`
	RateLimitFraction    float64      `json:"rate_limit_fraction"`
	Scope                scope.Config `json:"scope"`
	AutomatedReviewers   []string     `json:"automated_reviewers"`
	AddressSkipReviewers []string     `json:"address_skip_reviewers"`
	Owners               []string     `json:"owners"`
	TrustedAuthors       []string     `json:"trusted_authors"`
	FollowupsWithoutWS   bool         `json:"followups_without_workspace"`
	AddressWithoutWS     bool         `json:"address_without_workspace"`
	AddressReviews       *bool        `json:"address_reviews"`
	AddressChecks        *bool        `json:"address_checks"`
	IgnoreChecks         []string     `json:"ignore_checks"`
	AddressConflicts     *bool        `json:"address_conflicts"`
	Stalled              struct {
		Review string `json:"review"`
		Reply  string `json:"reply"`
		Merge  string `json:"merge"`
	} `json:"stalled"`
}

type hostKeys struct {
	Sweep struct {
		Fast string `json:"fast"`
		Slow string `json:"slow"`
	} `json:"sweep"`
	Views map[string]struct {
		Columns map[string]string `json:"columns"`
	} `json:"views"`
	Brief struct {
		MaxInputBytes int `json:"max_input_bytes"`
	} `json:"brief"`
	Projects []struct {
		Match  string `json:"match"`
		GitHub struct {
			TrustedAuthors            []string `json:"trusted_authors"`
			FollowupsWithoutWorkspace *bool    `json:"followups_without_workspace"`
			AddressWithoutWorkspace   *bool    `json:"address_without_workspace"`
		} `json:"github"`
	} `json:"projects"`
}

// settingsOf reads cfg; a value it does not hold is its default.
func settingsOf(cfg src.Config) (settings, error) {
	var t pluginTable
	if len(cfg.Plugin) > 0 && string(cfg.Plugin) != "null" {
		if err := json.Unmarshal(cfg.Plugin, &t); err != nil {
			return settings{}, fmt.Errorf("[plugins.github]: %w", err)
		}
	}
	var h hostKeys
	if len(cfg.Host) > 0 {
		if err := json.Unmarshal(cfg.Host, &h); err != nil {
			return settings{}, fmt.Errorf("the host's configuration: %w", err)
		}
	}
	out := settings{Collect: collect.Config{Scope: t.Scope, ReviewPrefix: "REVIEW ", RateLimitFraction: t.RateLimitFraction}}
	if t.ReviewPrefix != nil {
		out.Collect.ReviewPrefix = *t.ReviewPrefix
	}
	if out.Collect.RateLimitFraction <= 0 {
		out.Collect.RateLimitFraction = defaultFraction
	}
	var err error
	if out.Fast, err = durationOr(h.Sweep.Fast, defaultFast); err != nil {
		return settings{}, fmt.Errorf("sweep.fast: %w", err)
	}
	if out.Collect.Slow, err = durationOr(h.Sweep.Slow, defaultSlow); err != nil {
		return settings{}, fmt.Errorf("sweep.slow: %w", err)
	}
	for _, v := range h.Views {
		if c := v.Columns[needsMeColumn]; c != "" && !slices.Contains(out.Collect.NeedsMe, c) {
			out.Collect.NeedsMe = append(out.Collect.NeedsMe, c)
		}
	}
	slices.Sort(out.Collect.NeedsMe)
	out.Plan = plan.Config{ReviewPrefix: out.Collect.ReviewPrefix, Owners: t.Owners, TrustedAuthors: t.TrustedAuthors,
		FollowupsWithoutWorkspace: t.FollowupsWithoutWS, AddressWithoutWorkspace: t.AddressWithoutWS,
		AddressReviews: on(t.AddressReviews), AddressChecks: on(t.AddressChecks), AddressConflicts: on(t.AddressConflicts),
		AddressSkipReviewers: t.AddressSkipReviewers, IgnoreChecks: t.IgnoreChecks, Fast: out.Fast, Slow: out.Collect.Slow}
	for _, v := range h.Views {
		if c := v.Columns[completedColumn]; c != "" && !slices.Contains(out.Plan.Completed, c) {
			out.Plan.Completed = append(out.Plan.Completed, c)
		}
	}
	slices.Sort(out.Plan.Completed)
	out.MaxDiffBytes = h.Brief.MaxInputBytes
	if out.MaxDiffBytes <= 0 {
		out.MaxDiffBytes = defaultMaxDiffBytes
	}
	for _, p := range h.Projects {
		out.Plan.Projects = append(out.Plan.Projects, plan.Project{Match: p.Match, TrustedAuthors: p.GitHub.TrustedAuthors,
			FollowupsWithoutWorkspace: p.GitHub.FollowupsWithoutWorkspace, AddressWithoutWorkspace: p.GitHub.AddressWithoutWorkspace})
	}
	out.Ignore = slices.Concat(t.AutomatedReviewers, t.AddressSkipReviewers)
	for _, th := range []struct {
		key  string
		raw  string
		def  time.Duration
		into *time.Duration
	}{{"stalled.review", t.Stalled.Review, defaultStalledReview, &out.Stalled.Review}, {"stalled.reply", t.Stalled.Reply, defaultStalledReply, &out.Stalled.Reply},
		{"stalled.merge", t.Stalled.Merge, defaultStalledMerge, &out.Stalled.Merge}} {
		if *th.into, err = durationOr(th.raw, th.def); err != nil {
			return settings{}, fmt.Errorf("[plugins.github] %s: %w", th.key, err)
		}
	}
	return out, nil
}

// on is b, true when unset: the address rounds' switches default on.
func on(b *bool) bool { return b == nil || *b }

// durationOr reads s, a Go duration or a whole number of days such as 7d,
// the configuration's grammar; def when s is empty.
func durationOr(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	var d time.Duration
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || n == "" || n[0] < '0' || n[0] > '9' {
			return 0, fmt.Errorf("%q is not a duration", s)
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("%q is not a duration", s)
		}
	}
	if d <= 0 {
		return 0, errors.New("a duration must be longer than zero")
	}
	return d, nil
}
