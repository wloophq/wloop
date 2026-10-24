package api

import "time"

const (
	// AdminTimings takes the days to look back in the query parameter since.
	AdminTimings = "/api/admin/timings"
	// AdminFacts takes the pull request's URL in the query parameter pr.
	AdminFacts = "/api/admin/facts"
)

// Quantiles are a duration's nearest-rank median and 90th percentile over
// Count samples.
type Quantiles struct {
	Count int   `json:"count"`
	P50MS int64 `json:"p50_ms"`
	P90MS int64 `json:"p90_ms"`
}

type TagCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type TokenCounts struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

// BriefTimings are the brief runs of the window: the call time of the ok
// ones, the failed and the reused ones, their cost and the breaker's openings.
type BriefTimings struct {
	Call   Quantiles `json:"call"`
	Failed int       `json:"failed"`
	// Reused counts the runs that carried the brief in use over.
	Reused  int     `json:"reused"`
	CostUSD float64 `json:"cost_usd"`
	Outages int     `json:"outages"`
	// Unrecorded counts the runs started before recording began, which the
	// figures leave out.
	Unrecorded int `json:"unrecorded"`
}

// PRTimes are a pull request's milestones, zero when it has not reached
// one, with its turns and brief runs in the window.
type PRTimes struct {
	PR           string    `json:"pr"`
	Title        string    `json:"title,omitempty"`
	Author       string    `json:"author,omitempty"`
	Opened       time.Time `json:"opened,omitzero"`
	Followed     time.Time `json:"followed,omitzero"`
	FirstRequest time.Time `json:"first_request,omitzero"`
	FirstReview  time.Time `json:"first_review,omitzero"`
	Approved     time.Time `json:"approved,omitzero"`
	Merged       time.Time `json:"merged,omitzero"`
	Turns        int       `json:"turns"`
	Briefs       int       `json:"briefs"`
}

// The keys of TimingsReport.Milestones.
const (
	MilestoneReviewWait  = "request_to_my_review"
	MilestoneToApproval  = "opened_to_approved"
	MilestoneToMerge     = "opened_to_merged"
	MilestoneMineToMerge = "mine_opened_to_merged"
	MilestoneInLoop      = "followed_to_unfollowed"
	MilestoneLeadTime    = "first_push_to_merged"
	MilestoneMineLead    = "mine_first_push_to_merged"
)

// The keys of TimingsReport.Waiting: who a followed pull request waited on,
// by its situation events.
const (
	WaitingOnMe     = "me"
	WaitingOnOthers = "others"
	WaitingOnLoop   = "loop"
)

// WaitShare is the time followed pull requests spent waiting on one party
// in the window: the total, and the spans between situation changes. OpenMS
// is the part of the total in spans still open at the report's time, which
// Spans leaves out.
type WaitShare struct {
	TotalMS int64     `json:"total_ms"`
	OpenMS  int64     `json:"open_ms"`
	PRs     int       `json:"prs"`
	Spans   Quantiles `json:"spans"`
}

// ChangeFailure is the share of the window's merges a later fix or revert
// changed again: Failed of Briefed, the merges at least Days old whose
// brief listed their changed files; Recent are younger ones, left out.
// Tagged counts the merges whose own brief tags them change:fix or
// change:revert.
type ChangeFailure struct {
	Merged  int `json:"merged"`
	Briefed int `json:"briefed"`
	Failed  int `json:"failed"`
	Tagged  int `json:"tagged"`
	// Recent counts the briefed merges younger than Days, left out of
	// Briefed until a fix had the time to come.
	Recent     int    `json:"recent"`
	Days       int    `json:"days"`
	Definition string `json:"definition"`
}

// The keys of TimingsReport.Turns beyond the intents: the wait from sent
// to started, and an automated first turn's wait for its brief, from
// brief_wanted to the turn's send.
const (
	TurnStartWait = "sent_to_started"
	TurnBriefWait = "brief_wanted_to_sent"
)

// TurnTimes is one turn of the window, for --turns.
type TurnTimes struct {
	PR           string    `json:"pr"`
	Seq          int64     `json:"seq"`
	Intent       string    `json:"intent"`
	Trigger      string    `json:"trigger"`
	State        string    `json:"state"`
	Agent        string    `json:"agent,omitempty"`
	Sent         time.Time `json:"sent"`
	Started      time.Time `json:"started,omitzero"`
	Ended        time.Time `json:"ended,omitzero"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
	OutputTokens int64     `json:"output_tokens,omitempty"`
}

// TimingsReport is what the loop recorded in the window since Since;
// RecordedSince is when recording began. Milestones are pull request
// durations that start and end in the window; Turns are the durations of the
// turns that ended in it by intent, plus TurnStartWait.
type TimingsReport struct {
	Since         time.Time `json:"since"`
	RecordedSince time.Time `json:"recorded_since,omitzero"`
	// Viewer is the login the report counts as mine, "" when the daemon
	// does not know it yet.
	Viewer        string                 `json:"viewer,omitempty"`
	Milestones    map[string]Quantiles   `json:"milestones"`
	Waiting       map[string]WaitShare   `json:"waiting"`
	ChangeFailure ChangeFailure          `json:"change_failure"`
	Briefs        BriefTimings           `json:"briefs"`
	Intakes       Quantiles              `json:"intakes"`
	Turns         map[string]Quantiles   `json:"turns"`
	Tokens        map[string]TokenCounts `json:"tokens"`
	Tags          map[string][]TagCount  `json:"tags,omitempty"`
	Dropped       []string               `json:"dropped,omitempty"`
	PRs           []PRTimes              `json:"prs"`
	TurnList      []TurnTimes            `json:"turn_list"`
}

// Facts are what a pull request's guide is matched against: GitHub's read,
// the requested teams and, with a brief, its kind, size, risk, tags and
// files, with the goal, context and risk reason its turn text shows.
type Facts struct {
	Owner       string   `json:"owner"`
	Repo        string   `json:"repo"`
	Author      string   `json:"author"`
	Association string   `json:"association"`
	Base        string   `json:"base"`
	Teams       []string `json:"teams,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	Size        string   `json:"size,omitempty"`
	Risk        string   `json:"risk,omitempty"`
	Goal        string   `json:"goal,omitempty"`
	Context     string   `json:"context,omitempty"`
	RiskWhy     string   `json:"risk_why,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Files       []string `json:"files,omitempty"`
	HasBrief    bool     `json:"has_brief"`
}
