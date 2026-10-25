package port

import "github.com/wloophq/wloop/internal/domain/unit"

// Generation counts the notes of one process's change log: 0 before the
// first note, +1 per note.
type Generation uint64

// Topic is a set of input kinds; a note carries one or more.
type Topic uint32

const (
	TopicPRs          Topic = 1 << iota // pull_requests, pr_details
	TopicFollow                         // pull_requests.reasons and ws_*: follow state (noted with TopicPRs)
	TopicSourceEvents                   // events a source writes (GitHub timeline, Slack changes)
	TopicLoopEvents                     // events the loop writes: rounds, turns, sessions, situations, views, briefs, would_*
	TopicOverrides                      // overrides
	TopicTurns                          // turns, turn_records, turn_answers
	TopicSessions                       // sessions, agent_sessions, turn_stops
	TopicIntakes                        // intakes
	TopicBriefs                         // unit_briefs, brief_runs, unit_tags
	TopicUsage                          // turn_usage
	TopicRounds                         // round_state, round_days, round_day_prs, round_day_notes, turn_instructions, instruction_parts
	TopicPause                          // pause
	TopicViews                          // view_applied
	TopicMeta                           // meta
	TopicGate                           // the pending-round set (in memory)
	TopicAgents                         // the workspace snapshot's projection
	TopicConfig                         // a new loaded intent set or a reloaded profile
	TopicViewer                         // viewer login or teams
	TopicRefusals                       // the daemon's remove refusals
	TopicApp                            // the menu bar's reported status
	TopicJobs                           // a job run ended
	TopicLinks                          // links
	TopicItems                          // items, item_proposals
	TopicCards                          // unit_cards
	TopicEntries                        // entries and their tables
	TopicDrafts                         // drafts
	TopicMarks                          // unit_marks of units other than pull requests, whose are TopicRounds
)

// pr_seen notes nothing. A write that inserts events notes SourceEvents or
// LoopEvents by who writes them: the source's snapshot, append and merge
// fills note SourceEvents, every other event writer LoopEvents.
const (
	// RowInputs are the topics a row is derived from.
	RowInputs = TopicPRs | TopicFollow | TopicSourceEvents | TopicLoopEvents | TopicOverrides |
		TopicTurns | TopicSessions | TopicIntakes | TopicBriefs | TopicUsage | TopicPause |
		TopicGate | TopicAgents | TopicConfig | TopicViewer | TopicRefusals | TopicItems
	// HealthInputs are the topics the health endpoint shows.
	HealthInputs = TopicMeta | TopicPause | TopicBriefs | TopicApp | TopicJobs | TopicConfig
	// SweepInputs leaves out LoopEvents and Rounds: the sweep's own writes.
	SweepInputs = TopicPRs | TopicFollow | TopicSourceEvents | TopicOverrides | TopicTurns |
		TopicIntakes | TopicBriefs | TopicPause | TopicGate | TopicAgents | TopicConfig | TopicViewer
	// BriefInputs leaves out the queue's own Briefs writes.
	BriefInputs = TopicPRs | TopicFollow | TopicConfig
)

// Change is one note, or the merge of the notes in (from, Gen] a Since returns.
type Change struct {
	Gen    Generation
	Epoch  string      // per process; a client that sees a new epoch reloads
	Topics Topic       // the matching notes' topics intersected with the filter; 0 when nothing matched
	Units  []unit.Unit // distinct, sorted by Unit.Less; empty when All
	All    bool        // a matching note was NoteAll, or from is outside the window
}

func (c Change) Empty() bool { return c.Topics == 0 && !c.All }

func (c Change) Has(t Topic) bool { return c.Topics&t != 0 }

// Changes is the read side of the change log.
type Changes interface {
	Gen() Generation
	Since(from Generation, topics Topic) Change
	// Subscribe calls fn after every note, in generation order, synchronously
	// from Note: fn must return at once (hand off), must not write the store
	// and must not note the log.
	// A panicking fn is recovered, logged and unsubscribed.
	Subscribe(fn func(Change)) (cancel func())
}
