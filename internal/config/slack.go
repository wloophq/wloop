package config

import (
	"cmp"
	"time"

	"github.com/wloophq/wloop/internal/brand"
)

// DefaultSlackRedirectPort is the login callback port registered in the Slack app.
const DefaultSlackRedirectPort = 53682

// Slack holds the Slack source's settings; no secret lives here.
type Slack struct {
	// ClientID is the Slack app's client id; empty leaves Slack unconfigured.
	ClientID     string `json:"client_id"`
	RedirectPort int    `json:"redirect_port"`
	// KeychainService names the Keychain items; empty is brand.StateDir()+"-slack".
	KeychainService string `json:"keychain_service"`
	// PostAllowlist, when not empty, holds the only channel ids, or user ids naming
	// the DM with that user, a post may go to.
	PostAllowlist []string `json:"post_allowlist"`
	// RequestLog logs one line per Slack call: method and HTTP status.
	RequestLog bool            `json:"request_log"`
	Ingest     SlackIngest     `json:"ingest"`
	Extraction SlackExtraction `json:"extraction"`
	Turns      SlackTurns      `json:"turns"`
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

func (s Slack) Configured() bool { return s.ClientID != "" }

func (s Slack) Port() int { return cmp.Or(s.RedirectPort, DefaultSlackRedirectPort) }

func (s Slack) Service() string { return cmp.Or(s.KeychainService, brand.StateDir()+"-slack") }

// SlackExtraction sets the extraction job; a zero value keeps its default.
type SlackExtraction struct {
	// Enabled nil is on.
	Enabled *bool `json:"enabled,omitempty"`
	// Tier is fast, balanced or frontier; empty is fast.
	Tier     string  `json:"tier,omitempty"`
	Language string  `json:"language,omitempty"`
	MaxCall  float64 `json:"max_call_usd,omitempty"`
	MaxDay   float64 `json:"max_day_usd,omitempty"`
	MaxCalls int     `json:"max_day_calls,omitempty"`
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

func (e SlackExtraction) MaxCallUSD() float64 { return cmp.Or(e.MaxCall, 0.10) }

func (e SlackExtraction) MaxDayUSD() float64 { return cmp.Or(e.MaxDay, 3.0) }

func (e SlackExtraction) MaxDayCalls() int { return cmp.Or(e.MaxCalls, 500) }

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

// Items holds the items ledger's settings.
type Items struct {
	Heat ItemsHeat `json:"heat"`
}

type ItemsHeat struct {
	// WarmFor is how long a unit stays warm after its last item resolved; zero is the heat reader's default.
	WarmFor Days `json:"warm_for,omitempty"`
}
