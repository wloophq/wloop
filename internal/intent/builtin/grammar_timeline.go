package builtin

// SurfaceTimeline is timeline.toml, the words of the core's event kinds.
const SurfaceTimeline Surface = "timeline"

// EventKinds are the kinds of the events the core records on a unit's timeline; a plugin's own kinds are in its
// manifest.
var EventKinds = []Value{
	{Name: "held", Doc: "Automatic rounds paused on the unit."},
	{Name: "released", Doc: "Automatic rounds resumed."},
	{Name: "muted", Doc: "The unit's task deferred, out of the lists that wait on you."},
	{Name: "unmuted", Doc: "The deferral lifted."},
	{Name: "adopted", Doc: "Someone else's pull request taken over."},
	{Name: "status", Doc: "The unit's board status changed."},
	{Name: "removed", Doc: "The unit's workspace removed."},
	{Name: "nudged", Doc: "A nudge posted."},
	{Name: "tracked", Doc: "The unit followed."},
	{Name: "untracked", Doc: "The unit no longer followed."},
	{Name: "answered", Doc: "An agent's question answered."},
	{Name: "turn_sent", Doc: "A turn sent to an agent session."},
	{Name: "turn_started", Doc: "A turn started."},
	{Name: "turn_ended", Doc: "A turn ended."},
	{Name: "turn_resumed", Doc: "A turn resumed."},
	{Name: "turn_withdrawn", Doc: "A turn withdrawn."},
	{Name: "question_dismissed", Doc: "An agent's question dismissed."},
	{Name: "turn_delivered", Doc: "A turn delivered to its session."},
	{Name: "answer_routed", Doc: "An answer routed to its session."},
	{Name: "late_record", Doc: "A record that arrived after its turn ended."},
	{Name: "stop_blocked", Doc: "An agent's stop blocked for a reason the loop gave."},
	{Name: "turn_stopped", Doc: "A turn stopped."},
	{Name: "guide_trimmed", Doc: "A turn's guide trimmed to its limit."},
	{Name: "dossier_restored", Doc: "A dossier file restored."},
	{Name: "dossier_granted", Doc: "A dossier granted to a session."},
	{Name: "dossier_call", Doc: "A dossier compaction call, with its cost."},
	{Name: "omnibox_call", Doc: "An omnibox agent call, with its cost."},
	{Name: "brief_waiting", Doc: "A request waiting for its brief."},
	{Name: "intake_accepted", Doc: "A request accepted."},
	{Name: "intake_briefing", Doc: "A request waiting for its brief."},
	{Name: "intake_starting", Doc: "A request starting."},
	{Name: "intake_started", Doc: "A request started."},
	{Name: "intake_failed", Doc: "A request that failed."},
	{Name: "intake_canceled", Doc: "A request canceled."},
	{Name: "intake_guidance", Doc: "A request's guidance replaced."},
	{Name: "workflow_step_ran", Doc: "A workflow's step ran."},
}

var timelineGrammar = []Key{
	{Path: "events", In: SurfaceTimeline, Kind: KindMap, Doc: "The core's event kinds, by kind, as the timeline names them."},
	{Path: "events.*", In: SurfaceTimeline, Kind: KindTable, Values: func() []Value { return EventKinds }, Doc: "One event kind."},
	{Path: "events.*.label", In: SurfaceTimeline, Kind: KindText, Required: true, Localized: true, Doc: "What the timeline calls an event of the kind, such as \"paused\"."},
}
