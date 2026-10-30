// Package config is the configuration the Slack plugin reads: its
// [plugins.slack] table and the host keys its manifest's config_reads
// names, as the host passes them.
package config

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/pkg/plugin/source"
)

// DefaultSlackRedirectPort is the login callback port registered in the Slack app.
const DefaultSlackRedirectPort = 53682

// Config is what the plugin reads of the host's configuration.
type Config struct {
	Slack Slack
	// Placement are the host's placement rules.
	Placement []PlacementRule
	Items     Items
	// Brand is the host's name, which names the default Keychain service.
	Brand string
}

// Parse reads c, the configuration the host passes; brand is the host's
// name.
func Parse(c source.Config, brand string) (Config, error) {
	out := Config{Brand: brand}
	if len(c.Plugin) > 0 && string(c.Plugin) != "null" {
		if err := json.Unmarshal(c.Plugin, &out.Slack); err != nil {
			return Config{}, fmt.Errorf("plugins.slack: %w", err)
		}
	}
	if len(c.Host) > 0 && string(c.Host) != "null" {
		var host struct {
			Placement []PlacementRule `json:"placement"`
			Heat      ItemsHeat       `json:"items.heat"`
		}
		if err := json.Unmarshal(c.Host, &host); err != nil {
			return Config{}, fmt.Errorf("the host's configuration: %w", err)
		}
		out.Placement, out.Items.Heat = host.Placement, host.Heat
	}
	return out, nil
}

// Slack holds the Slack source's settings; no secret lives here.
type Slack struct {
	// ClientID is the Slack app's client id; empty leaves Slack unconfigured.
	ClientID     string `json:"client_id"`
	RedirectPort int    `json:"redirect_port"`
	// KeychainService names the Keychain items; empty is the brand's name plus "-slack".
	KeychainService string `json:"keychain_service"`
	// PostAllowlist, when not empty, holds the only channel ids, or user ids naming
	// the DM with that user, a post may go to.
	PostAllowlist []string `json:"post_allowlist"`
	// RequestLog logs one line per Slack call: method and HTTP status.
	RequestLog bool            `json:"request_log"`
	Ingest     SlackIngest     `json:"ingest"`
	Extraction SlackExtraction `json:"extraction"`
	Turns      SlackTurns      `json:"turns"`
	// EndedAfter is how long a unit stays quiet before it has ended; zero is DefaultSlackEndedAfter.
	EndedAfter Days `json:"ended_after,omitempty"`
}

// SlackTurns sets what a turn on a Slack unit reads.
type SlackTurns struct {
	// MaxChars bounds the unit's messages rendered into the instructions; zero is 30000, and less than MinSlackTurnChars reads as it.
	MaxChars int `json:"max_chars,omitempty"`
}

// MinSlackTurnChars is the smallest cap Chars returns, which holds a parent and a reply.
const MinSlackTurnChars = 2000

func (t SlackTurns) Chars() int {
	switch {
	case t.MaxChars <= 0:
		return 30000
	case t.MaxChars < MinSlackTurnChars:
		return MinSlackTurnChars
	}
	return t.MaxChars
}

// SlackIngest is what the Slack source admits and how it listens.
type SlackIngest struct {
	// Terms admit a message that holds one of them as a word, in any case.
	Terms []string `json:"terms"`
	// AllowApps and DenyApps hold bot ids (B...) or app ids (A...).
	AllowApps []string `json:"allow_apps"`
	DenyApps  []string `json:"deny_apps"`
	// Socket nil is on.
	Socket *bool `json:"socket"`
	// Since is the first start's backfill date, YYYY-MM-DD; empty is 7 days back.
	Since string `json:"since"`
}

func (i SlackIngest) SocketOn() bool { return i.Socket == nil || *i.Socket }

// DefaultSlackEndedAfter is plugins.slack.ended_after's default.
const DefaultSlackEndedAfter = 7 * 24 * time.Hour

// EndedAfterFor is how long a Slack unit stays quiet before it has ended.
func (s Slack) EndedAfterFor() time.Duration {
	return cmp.Or(time.Duration(s.EndedAfter), DefaultSlackEndedAfter)
}

func (s Slack) Configured() bool { return s.ClientID != "" }

func (s Slack) Port() int { return cmp.Or(s.RedirectPort, DefaultSlackRedirectPort) }

// Service is the Keychain service of the tokens.
func (c Config) Service() string { return cmp.Or(c.Slack.KeychainService, c.Brand+"-slack") }

// SlackExtraction sets the extraction job; a zero value keeps its default.
type SlackExtraction struct {
	// Enabled nil is on.
	Enabled *bool `json:"enabled,omitempty"`
	// Tier is fast, balanced or frontier; empty is fast.
	Tier       string  `json:"tier,omitempty"`
	Language   string  `json:"language,omitempty"`
	PerCallUSD float64 `json:"per_call_usd,omitempty"`
	PerDayUSD  float64 `json:"per_day_usd,omitempty"`
	PerDay     int     `json:"per_day,omitempty"`
	// Quiet is how long a unit stays unchanged before it runs; MaxWait bounds the delay of its oldest change.
	Quiet       Days `json:"quiet,omitempty"`
	MaxWait     Days `json:"max_wait,omitempty"`
	WindowChars int  `json:"window_chars,omitempty"`
	// MaxFailures failed calls in a row make the job give up on a unit until its next change.
	MaxFailures int `json:"max_failures,omitempty"`
	// ContextMessages and ContextChars bound the read-only messages shown before a DM's window; -1 turns either off.
	ContextMessages int `json:"context_messages,omitempty"`
	ContextChars    int `json:"context_chars,omitempty"`
	// EscalateOverChars sends a unit whose rendered conversation is longer to the frontier tier; 0 or -1 is off.
	EscalateOverChars int `json:"escalate_over_chars,omitempty"`
}

func (e SlackExtraction) On() bool { return e.Enabled == nil || *e.Enabled }

func (e SlackExtraction) TierName() string { return cmp.Or(e.Tier, "fast") }

func (e SlackExtraction) LanguageName() string { return cmp.Or(e.Language, "English") }

func (e SlackExtraction) MaxCallUSD() float64 { return cmp.Or(e.PerCallUSD, 0.10) }

func (e SlackExtraction) MaxDayUSD() float64 { return cmp.Or(e.PerDayUSD, 3.0) }

func (e SlackExtraction) MaxDayCalls() int { return cmp.Or(e.PerDay, 500) }

func (e SlackExtraction) QuietFor() time.Duration {
	return cmp.Or(time.Duration(e.Quiet), 90*time.Second)
}

func (e SlackExtraction) MaxWaitFor() time.Duration {
	return cmp.Or(time.Duration(e.MaxWait), 10*time.Minute)
}

func (e SlackExtraction) Window() int { return cmp.Or(e.WindowChars, 40000) }

func (e SlackExtraction) GiveUpAfter() int { return cmp.Or(e.MaxFailures, 3) }

func (e SlackExtraction) ContextN() int { return max(cmp.Or(e.ContextMessages, 10), 0) }

func (e SlackExtraction) ContextBudget() int { return max(cmp.Or(e.ContextChars, 4000), 0) }

func (e SlackExtraction) EscalateOver() int { return max(e.EscalateOverChars, 0) }

// Items holds the host's items ledger settings the plugin reads.
type Items struct {
	Heat ItemsHeat `json:"heat"`
}

type ItemsHeat struct {
	// WarmFor is how long a unit stays warm after its last item resolved; zero is DefaultWarmFor.
	WarmFor Days `json:"warm_for,omitempty"`
}

// DefaultWarmFor is items.heat.warm_for when unset.
const DefaultWarmFor = 72 * time.Hour

// WarmForOr is how long a unit stays warm after its last item resolved.
func (h ItemsHeat) WarmForOr() time.Duration { return cmp.Or(time.Duration(h.WarmFor), DefaultWarmFor) }

// PlacementRule places the work of what matches Match.
type PlacementRule struct {
	Match    string `json:"match"`
	Repo     string `json:"repo,omitempty"`
	Checkout string `json:"checkout,omitempty"`
	Role     string `json:"role,omitempty"`
	Shared   bool   `json:"shared,omitempty"`
}

// Days is a duration written as a Go duration or a whole number of days:
// "2d", "36h", "90m".
type Days time.Duration

// Day is what d means: 24 hours.
const Day = 24 * time.Hour

func (d *Days) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Days(v)
	return nil
}

// ParseDuration reads s, a Go duration such as 10m or 1h30m, or a whole
// number of days such as 7d; zero and negative durations are refused.
func ParseDuration(s string) (time.Duration, error) {
	var d time.Duration
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || n == "" || n[0] < '0' || n[0] > '9' {
			return 0, fmt.Errorf("%q is not a duration: a whole number of days is written like 7d", s)
		}
		d = time.Duration(days) * Day
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("%q is not a duration, such as 30s, 10m, 2h or 1d", s)
		}
	}
	if d <= 0 {
		return 0, errors.New("a duration must be longer than zero")
	}
	return d, nil
}
