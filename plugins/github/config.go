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
	"github.com/wloophq/wloop/plugins/github/scope"
)

// Defaults the configuration falls back to when it says nothing.
const (
	defaultFast     = 2 * time.Minute
	defaultSlow     = time.Hour
	defaultFraction = 0.5
)

// needsMeColumn is the semantic status of the board column a workspace
// waiting on the user sits in.
const needsMeColumn = "needs-me"

// settings are what the plugin reads of the configuration: its
// [plugins.github] table and the host keys its manifest's config_reads
// names.
type settings struct {
	Collect collect.Config
	Fast    time.Duration
}

type pluginTable struct {
	ReviewPrefix      *string      `json:"review_prefix"`
	RateLimitFraction float64      `json:"rate_limit_fraction"`
	Scope             scope.Config `json:"scope"`
}

type hostKeys struct {
	Sweep struct {
		Fast string `json:"fast"`
		Slow string `json:"slow"`
	} `json:"sweep"`
	Views map[string]struct {
		Columns map[string]string `json:"columns"`
	} `json:"views"`
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
	return out, nil
}

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
