package builtin

import (
	"slices"

	"github.com/wloophq/wloop/internal/domain/placement"
)

func cfgValues(ns ...string) func() []Value {
	return func() []Value {
		out := make([]Value, len(ns))
		for i, n := range ns {
			out[i] = Value{Name: n}
		}
		return out
	}
}

// ConfigTiers are the model tiers a configured call takes, the set
// core/turns.Tiers holds.
var ConfigTiers = cfgValues("fast", "balanced", "frontier")

var (
	cfgPlacementRoles = cfgValues(placement.RoleReview, placement.RoleAuthor, placement.RoleWork)
)

func cfgKey(path string, k Kind, def, doc string) Key {
	return Key{Path: path, In: SurfaceConfig, Kind: k, Default: def, Doc: doc}
}

func cfgList(path, doc string) Key {
	return Key{Path: path, In: SurfaceConfig, Kind: KindList, Elem: KindText, Doc: doc}
}

func cfgEnum(path string, values func() []Value, def, doc string) Key {
	return Key{Path: path, In: SurfaceConfig, Kind: KindEnum, Values: values, Default: def, Doc: doc}
}

// cfgApproval are an approval block's keys at prefix, with modes.
func cfgApproval(prefix string, modes func() []Value, what string) []Key {
	return []Key{
		cfgKey(prefix, KindTable, "", "The approval of "+what+", over the manifest's field by field."),
		cfgEnum(prefix+".mode", modes, "", "The mode when no rule matches; left out, the manifest's."),
		cfgKey(prefix+".window", KindDur, "", "How long a delay waits before it goes; only with mode delay."),
		cfgKey(prefix+".rule[]", KindTable, "", "Rules tried in order before the mode, the first match wins; set, they replace the manifest's, and [] clears them."),
		cfgKey(prefix+".rule[].when", KindPred, "", "When the rule applies."),
		cfgEnum(prefix+".rule[].mode", modes, "", "The rule's mode."),
		cfgKey(prefix+".rule[].window", KindDur, "", "The rule's delay; only with mode delay."),
	}
}

// cfgPluginDrafts are the drafts keys of plugin id.
func cfgPluginDrafts(id string) []Key {
	return []Key{
		cfgKey(id+".drafts", KindTable, "", "The drafts the plugin's own rules propose."),
		cfgKey(id+".drafts.approval", KindTable, "", "Their approval; left out, each waits for you."),
		cfgEnum(id+".drafts.approval.mode", DraftModes, "", "The approval mode."),
		cfgKey(id+".drafts.approval.window", KindDur, "", "How long a delay waits before the draft goes; only with mode delay."),
	}
}

// configGrammar is the configuration's keys.
var configGrammar = slices.Concat(
	[]Key{
		cfgKey("projects_dir", KindText, "", "Where review clones a repository the workspace manager does not know yet."),
		cfgList("workspace_managers", "The enabled workspace managers, in the order a new worktree picks them."),
		cfgKey("agent", KindText, "", "The agent the sessions run."),
		cfgKey("agent_home", KindText, "", "The agent's own configuration directory; empty is the agent's default."),
		cfgKey("agent_models", KindMap, "", "Per agent, a model name for each model tier."),
		cfgKey("agent_models.*", KindMap, "", "One agent's models by tier."),
		cfgKey("agent_models.*.*", KindText, "", "The agent's model name for the tier."),
		cfgKey("profile", KindText, "", "The user profile layered over the bundled one: a name under the profiles directory, or a path; an intent's profile names an intent."),

		cfgKey("github", KindTable, "", "The GitHub plugin's settings."),
		cfgKey("github.enabled", KindBool, "", "Switches the GitHub plugin; left out, it is on."),
		cfgKey("github.review_prefix", KindText, "REVIEW ", "The prefix of a review workspace's name."),
		cfgList("github.automated_reviewers", "Logins of review bots whose reviews count as automated."),
		cfgList("github.owners", "The GitHub owners automated follow-ups run on; empty is every owner the workspace manager knows."),
		cfgList("github.trusted_authors", "Logins whose pull requests pass the author association check of automated rounds."),
		cfgKey("github.followups_without_workspace", KindBool, "false", "A follow-up review recreates the review workspace the user deleted; off, the sweep only logs it."),
		cfgKey("github.address_without_workspace", KindBool, "false", "An address round creates its author workspace when it has none; off, the sweep only records it."),
		cfgKey("github.address_reviews", KindBool, "true", "Someone's review of a pull request you develop starts an address round."),
		cfgList("github.address_skip_reviewers", "Logins whose reviews never start an address round."),
		cfgKey("github.address_checks", KindBool, "true", "Failing checks on the head of a pull request you develop start an address round."),
		cfgList("github.ignore_checks", "Checks whose failure never starts an address round."),
		cfgKey("github.address_conflicts", KindBool, "true", "A pull request you develop that conflicts with its base past the conflict window starts an address round."),
		cfgKey("github.scope", KindTable, "", "Whose pull requests the GitHub plugin follows."),
		cfgList("github.scope.people", "Logins whose pull requests it follows."),
		cfgList("github.scope.teams", "Teams whose pull requests it follows."),
		cfgList("github.scope.repos", "Repositories whose pull requests it follows."),
		cfgList("github.scope.watch", "Repositories it watches."),
		cfgKey("github.stalled", KindTable, "", "When a pull request counts as stalled."),
		cfgKey("github.stalled.review", KindDur, "2d", "Waiting on a review for this long."),
		cfgKey("github.stalled.reply", KindDur, "1d", "Waiting on a reply for this long."),
		cfgKey("github.stalled.merge", KindDur, "1d", "Approved and unmerged for this long."),
		cfgKey("github.nudge", KindTable, "", "The nudges on pull requests."),
		cfgKey("github.nudge.silent_days", KindInt, "30", "Calendar days of silence on a pull request before a nudge is due; workflows read it as config.github.nudge.silent_days."),
		cfgKey("github.rate_limit_fraction", KindNumber, "0.5", "The share of the GitHub rate limit the sweeps may spend, a number between 0 and 1."),

		cfgKey("projects[]", KindTable, "", "Settings for the pull requests and the work of matching repositories; the first match wins."),
		cfgKey("projects[].match", KindText, "", "An owner/repo pattern, such as acme/* or acme/api."),
		cfgKey("projects[].projects_dir", KindText, "", "The project's projects_dir."),
		cfgKey("projects[].agent", KindText, "", "The agent the project's sessions run."),
		cfgKey("projects[].agent_home", KindText, "", "The project's agent_home."),
		cfgList("projects[].workspace_managers", "The managers a new worktree of the project picks from, in order."),
		cfgKey("projects[].trust_clones", KindBool, "", "Trusts a repository of the project in the agent home at the moment the loop clones it, so its settings and hooks run as you; the match must name its owner, such as acme/*."),
		cfgKey("projects[].github", KindTable, "", "The GitHub settings the project overrides."),
		cfgList("projects[].github.trusted_authors", "The project's github.trusted_authors."),
		cfgKey("projects[].github.followups_without_workspace", KindBool, "", "The project's github.followups_without_workspace."),
		cfgKey("projects[].github.address_without_workspace", KindBool, "", "The project's github.address_without_workspace."),

		cfgKey("sweep", KindTable, "", "How often the sweeps run."),
		cfgKey("sweep.fast", KindDur, "2m", "The fast sweep's interval."),
		cfgKey("sweep.slow", KindDur, "1h", "The slow sweep's interval."),
		cfgKey("http", KindTable, "", "The daemon's web cockpit."),
		cfgKey("http.port", KindInt, "0", "The cockpit's port; 0 is none."),
		cfgKey("mcp", KindTable, "", "The daemon's MCP endpoint, which agent homes register."),
		cfgKey("mcp.port", KindInt, "47800", "The MCP endpoint's port."),
		cfgKey("views", KindMap, "", "Each view sink's own settings, by sink."),
		cfgKey("views.*", KindTable, "", "One sink's settings."),
		cfgKey("views.*.columns", KindMap, "", "The sink's column for each status: working, working-fallback, needs-me, reviewed, ready, in-progress, in-review, completed."),
		cfgKey("views.*.columns.*", KindText, "", "The column of the status."),

		cfgKey("brief", KindTable, "", "The brief: one headless call that previews each unit."),
		cfgKey("brief.enabled", KindBool, "false", "Briefs each unit."),
		cfgEnum("brief.tier", ConfigTiers, "fast", "The brief's model tier."),
		cfgKey("brief.max_input_bytes", KindInt, "60000", "The most bytes of input a brief reads, between 1000 and 200000."),
		cfgKey("cleanup", KindTable, "", "The thresholds of the session and workspace cleanup workflows."),
		cfgKey("cleanup.idle", KindDur, "2h", "How long a finished session stays idle before it closes."),
		cfgKey("cleanup.asked_idle", KindDur, "4h", "How long a session that asked you stays idle before it closes."),
		cfgKey("cleanup.workspace_grace", KindDur, "1d", "How long, in working time, a workspace stays after its unit finished."),
		cfgKey("cleanup.manual_idle", KindDur, "1d", "How long a session you started stays idle before it closes."),
		cfgKey("cleanup.stale_workspace", KindDur, "14d", "How long an untouched workspace stays."),
		cfgList("cleanup.disposable", "Ignored files and directories a tool recreates, so removing a workspace that holds them loses nothing."),
		cfgKey("classify", KindTable, "", "The classify step."),
		cfgKey("classify.per_day", KindInt, "50", "Classify calls per day."),

		cfgKey("workflows", KindMap, "", "Overrides of each workflow's manifest, by workflow name."),
		cfgKey("workflows.*", KindTable, "", "One workflow's overrides."),
		cfgKey("workflows.*.enabled", KindBool, "", "Switches the workflow; left out, it is on."),
		cfgKey("workflows.*.observe", KindBool, "", "Plans the workflow's items pending, for you to run; left out, the manifest's."),
		cfgKey("workflows.*.only_when", KindPred, "", "A condition ANDed with the workflow's when."),
		cfgKey("workflows.*.drafts", KindTable, "", "The drafts the workflow proposes."),
	},
	cfgApproval("workflows.*.approval", RunModes, "the workflow's runs"),
	cfgApproval("workflows.*.drafts.approval", DraftModes, "the workflow's drafts"),
	[]Key{cfgKey("workflows.*.gates", KindTable, "", "The gates the workflow opens.")},
	cfgApproval("workflows.*.gates.approval", GateModes, "the workflow's gates"),
	[]Key{
		cfgKey("intents", KindMap, "", "Each intent's settings, by intent name."),
		cfgKey("intents.*", KindTable, "", "One intent's settings."),
		cfgKey("intents.*.project_settings", KindTable, "", "Where the intent's sessions load the checkout's own agent settings."),
		cfgKey("intents.*.project_settings.when", KindPred, "", "Where the intent's sessions load the checkout's settings; elsewhere they start without them."),

		cfgKey("placement[]", KindTable, "", "The rules that place units; the first match wins."),
		cfgKey("placement[].match", KindText, "", "The units the rule places."),
		cfgKey("placement[].repo", KindText, "", "The repository their work checks out."),
		cfgKey("placement[].checkout", KindText, "", "The branch their work checks out."),
		cfgEnum("placement[].role", cfgPlacementRoles, "", "The role of their workspace."),
		cfgKey("placement[].shared", KindBool, "", "The units share one workspace."),

		cfgKey("tasks", KindTable, "", "The tasks' settings."),
		cfgKey("tasks.stale_waiting_days", KindInt, "2", "Working days after which a waiting task goes stale."),
		cfgList("tasks.areas", "The areas a task is offered."),
		cfgList("tasks.contexts", "The contexts a task is offered."),
		cfgKey("notifications", KindTable, "", "The notifications."),
		cfgKey("notifications.digest_at", KindText, "09:00", "The local HH:MM of the daily digest; empty for none."),
		cfgKey("dossiers", KindTable, "", "The dossiers' lifecycle and quotas; a value of zero or less keeps its default."),
		cfgKey("dossiers.archive_after", KindDur, "30d", "How long after its task or unit resolved a dossier is archived."),
		cfgKey("dossiers.purge_after", KindDur, "400d", "How long after the archive it is purged."),
		cfgKey("dossiers.soft_cap_mb", KindInt, "50", "The size per dossier doctor reports."),
		cfgKey("dossiers.total_cap_mb", KindInt, "2048", "The size of dossiers and archives doctor reports."),
		cfgKey("dossiers.max_file_mb", KindInt, "5", "The largest file a dossier keeps; a larger one is linked."),
		cfgKey("dossiers.compact_at_kb", KindInt, "256", "The log size that triggers compaction."),

		cfgKey("drafts", KindTable, "", "The settings every proposer's drafts share."),
		cfgKey("drafts.repeat_after", KindDur, "1d", "The default repeat_after a config.drafts.repeat_after reference reads, and the plugin rules' delay before they propose a settled draft's key again."),
		cfgKey("items", KindTable, "", "The items."),
		cfgKey("items.heat", KindTable, "", "How long a unit stays warm."),
		cfgKey("items.heat.warm_for", KindDur, "", "How long a unit stays warm after its last item resolved; left out, the heat reader's default."),
		cfgKey("headless", KindTable, "", "The headless turns' caps; a cost of zero leaves it uncapped."),
		cfgEnum("headless.tier", ConfigTiers, "balanced", "The model tier of their calls."),
		cfgKey("headless.per_call_usd", KindNumber, "0.5", "What one call may cost, in USD."),
		cfgKey("headless.per_day_usd", KindNumber, "5", "What they may cost in a day, in USD."),
		cfgKey("turns", KindTable, "", "The automated turn budgets."),
		cfgKey("turns.per_day", KindInt, "20", "Automated turns per day, all workflows together, on units whose plugin keeps no per-unit count (every provider but GitHub today)."),
		cfgKey("turns.per_unit_per_day", KindInt, "5", "Automated turns on one unit per local day, on units whose plugin keeps a per-unit count (GitHub today); every automated turn spends one, whatever its intent declares, and a turn by hand none."),

		cfgKey("slack", KindTable, "", "The Slack plugin's settings; no secret lives here."),
		cfgKey("slack.enabled", KindBool, "", "Switches the Slack plugin; left out, it is on."),
		cfgKey("slack.client_id", KindText, "", "The Slack app's client id; empty leaves Slack unconfigured."),
		cfgKey("slack.redirect_port", KindInt, "", "The login callback port registered in the Slack app; 0 is 53682."),
		cfgKey("slack.keychain_service", KindText, "", "The name of the Keychain items; empty is the brand's."),
		cfgList("slack.post_allowlist", "When not empty, the only channel ids, or user ids naming the DM with that user, a post may go to."),
		cfgKey("slack.request_log", KindBool, "false", "Logs one line per Slack call: method and HTTP status."),
		cfgKey("slack.ingest", KindTable, "", "What the Slack plugin admits and how it listens."),
		cfgList("slack.ingest.terms", "A message that holds one of them as a word, in any case, is admitted."),
		cfgList("slack.ingest.allow_apps", "Bot ids (B...) or app ids (A...) whose messages are admitted."),
		cfgList("slack.ingest.deny_apps", "Bot ids (B...) or app ids (A...) whose messages are not."),
		cfgKey("slack.ingest.socket", KindBool, "", "Listens on a socket; left out, it does."),
		cfgKey("slack.ingest.since", KindText, "", "The first start's backfill date, YYYY-MM-DD; empty is 7 days back."),
		cfgKey("slack.extraction", KindTable, "", "The extraction job; a zero value keeps its default."),
		cfgKey("slack.extraction.enabled", KindBool, "", "Switches the extraction job; left out, it is on."),
		cfgEnum("slack.extraction.tier", ConfigTiers, "", "Its model tier; left out, fast."),
		cfgKey("slack.extraction.language", KindText, "", "The language the extraction writes in; empty is English."),
		cfgKey("slack.extraction.per_call_usd", KindNumber, "", "What one extraction call may cost, in USD; 0 is 0.10."),
		cfgKey("slack.extraction.per_day_usd", KindNumber, "", "What extraction may cost in a day, in USD; 0 is 3."),
		cfgKey("slack.extraction.per_day", KindInt, "", "Extraction calls per day; 0 is 500."),
		cfgKey("slack.extraction.quiet", KindDur, "", "How long a unit stays unchanged before it runs; empty is 90s."),
		cfgKey("slack.extraction.max_wait", KindDur, "", "The longest delay of a unit's oldest change; empty is 10m."),
		cfgKey("slack.extraction.window_chars", KindInt, "", "The characters of conversation one call reads; 0 is 40000."),
		cfgKey("slack.extraction.max_failures", KindInt, "", "Failed calls in a row after which the job gives up on a unit until its next change; 0 is 3."),
		cfgKey("slack.extraction.context_messages", KindInt, "", "The read-only messages shown before a DM's window; 0 is 10, -1 turns them off."),
		cfgKey("slack.extraction.context_chars", KindInt, "", "The characters of those messages; 0 is 4000, -1 turns them off."),
		cfgKey("slack.extraction.escalate_over_chars", KindInt, "", "A unit whose rendered conversation is longer goes to the frontier tier; 0 or -1 is off."),
		cfgKey("slack.ended_after", KindDur, "7d", "How long a Slack unit stays quiet, its newest stored message that old, before the plugin says it has ended (slack.ended)."),
		cfgKey("slack.turns", KindTable, "", "What a turn on a Slack unit reads."),
		cfgKey("slack.turns.max_chars", KindInt, "", "The characters of the unit's messages rendered into the instructions; 0 is 30000."),
	},
	cfgPluginApprovals("github", "slack"),
)

// cfgPluginApprovals are the drafts and gates keys of each plugin of ids.
func cfgPluginApprovals(ids ...string) []Key {
	var out []Key
	for _, id := range ids {
		out = append(out, cfgPluginDrafts(id)...)
	}
	for _, id := range ids {
		out = append(out, cfgPluginGates(id)...)
	}
	return out
}

// cfgPluginGates are the gates keys of plugin id.
func cfgPluginGates(id string) []Key {
	return []Key{
		cfgKey(id+".gates", KindTable, "", "The gates the plugin's own rules open."),
		cfgKey(id+".gates.approval", KindTable, "", "Their approval; left out, each waits for you."),
		cfgEnum(id+".gates.approval.mode", GateModes, "", "The approval mode."),
		cfgKey(id+".gates.approval.window", KindDur, "", "How long a delay waits before the gate's consequence; only with mode delay."),
	}
}
