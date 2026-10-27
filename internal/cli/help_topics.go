package cli

import (
	"strings"

	"github.com/wloophq/wloop/internal/brand"
)

// helpFlag is one flag of a command's help: Name with its argument, Text
// what it does.
type helpFlag struct{ Name, Text string }

// helpTopic is the help of one command, or of a pair that shares it. Usage
// and Examples leave out the command's own name; Docs is a page path of the
// documentation site.
type helpTopic struct {
	Names    []string
	Summary  string
	Usage    []string
	About    string
	Flags    []helpFlag
	Examples []string
	Docs     string
}

type helpGroup struct {
	Title  string
	Topics []helpTopic
}

const groupYourIntents = "YOUR INTENTS"

var colorFlag = helpFlag{"--color auto|always|never", "Lay the output out for a terminal; auto styles it on a terminal alone"}

// conditionFlags are the flags that name a block of the profile, one per
// dimension.
func conditionFlags() []helpFlag {
	return []helpFlag{
		{"--<dimension> <value>", "A condition of the block, one flag per dimension: " + conditionNames()},
		{"--tag <key>=<value>", "A tag condition; repeatable"},
	}
}

// helpGroups are the commands of the main help by task; manager names the
// configured workspace manager.
func helpGroups(manager string) []helpGroup {
	name, app := brand.Name, brand.App
	const pr = "https://github.com/acme/api/pull/7"
	return []helpGroup{
		{"GET STARTED", []helpTopic{
			{
				Names:   []string{"init"},
				Summary: "Set up from zero: config, agent homes, the app",
				Usage:   []string{"init [flags]"},
				About: "Check the prerequisites, write the config, log in to Slack, install into the agent homes and the app, then run doctor. " +
					"At a terminal it asks for each answer; elsewhere every answer comes from the flags.",
				Flags: []helpFlag{
					{"--projects-dir <dir>", "Where review clones a repository the workspace manager does not know"},
					{"--agent <name>", "The coding agent that runs the turns"},
					{"--scope-repo <owner/repo>", "A repository to follow; repeatable"},
					{"--scope-team <org/slug>", "A team whose review requests and pull requests to follow; repeatable"},
					{"--scope-person <login>", "A login whose pull requests to follow; repeatable"},
					{"--profile <dir>", "A personal profile directory, created with example blocks when absent"},
					{"--profile-default", "Use the bundled profile alone"},
					{"--slack-client-id <id>", "The client id of your Slack app"},
					{"--skip-slack", "Leave Slack as it is"},
					{"--no-install", "Install into neither the agent homes nor the app"},
					{"--yes", "Say yes to every confirmation: overwrite the config, install, app install"},
					colorFlag,
				},
				Examples: []string{"init", "init --scope-repo acme/api --profile-default --skip-slack --yes"},
				Docs:     "getting-started/init",
			},
			{
				Names:   []string{"doctor"},
				Summary: "Diagnose the setup without changing anything",
				Usage:   []string{"doctor [--git] [--color auto|always|never]", "doctor --load [--json] [--color auto|always|never]"},
				About: "Check the prerequisites, the config, the agent homes, the profile and the daemon, and print the command that fixes each problem. " +
					"The checks of " + name + "'s state ask the daemon. With --load, print what the daemon spent since it started instead.",
				Flags: []helpFlag{
					{"--git", "Also check git access live with an ls-remote, as the daemon would run it"},
					{"--load", "The calls, spawns and points by resource of every job, route and loop since the daemon started"},
					{"--json", "With --load, print the report as JSON"},
					colorFlag,
				},
				Examples: []string{"doctor", "doctor --load"},
				Docs:     "guides/troubleshooting",
			},
		}},
		{"PULL REQUESTS", []helpTopic{
			{
				Names:    []string{"take-over"},
				Summary:  "Make a pull request someone else opened yours",
				Usage:    []string{"take-over <url>"},
				About:    "Make a pull request someone else opened yours to develop: from then on " + name + " treats it as one of yours, and address works on it.",
				Examples: []string{"take-over " + pr},
				Docs:     "guides/take-over",
			},
			{
				Names:   []string{"remove"},
				Summary: "Delete the workspace of a merged or closed one",
				Usage:   []string{"remove <url> [--force]"},
				About: "Remove the workspace of a merged or closed pull request when nothing in it would be lost. " +
					"--force removes it whatever it holds, for good; an open pull request's workspace stays.",
				Flags:    []helpFlag{{"--force", "Remove whatever the workspace holds"}},
				Examples: []string{"remove " + pr},
				Docs:     "concepts/board",
			},
		}},
		{"WHAT WAITS ON YOU", []helpTopic{
			{
				Names:   []string{"status"},
				Summary: "Pull requests by view, those needing you first",
				Usage:   []string{"status [--color auto|always|never]"},
				About: "List the daemon's pull requests by view, those needing you first. " +
					"Piped, it prints one tab-separated line per pull request and view.",
				Flags:    []helpFlag{colorFlag},
				Examples: []string{"status", "status | grep needs-me"},
				Docs:     "concepts/surfaces",
			},
			{
				Names:    []string{"tui"},
				Summary:  "The same views in the terminal, one-key actions",
				Usage:    []string{"tui"},
				About:    "The Needs me, In loop and My PRs views in the terminal, with one-key actions on the selected pull request.",
				Examples: []string{"tui"},
				Docs:     "concepts/surfaces",
			},
			{
				Names:   []string{"open"},
				Summary: "The cockpit, a workspace or a pull request",
				Usage:   []string{"open [<url>]", "open [<url>] --print [--view <view>]"},
				About: "Open the workspace of a pull request in " + manager + ", or the pull request itself when it has none. " +
					"With no URL, open the cockpit. With --print, print the cockpit URL with its token instead of opening it.",
				Flags: []helpFlag{
					{"--print", "Print the cockpit URL with its token instead of opening it"},
					{"--view <view>", "The cockpit view: " + strings.Join(cockpitViews, ", ")},
				},
				Examples: []string{"open", "open " + pr, "open --print --view needs-me"},
				Docs:     "concepts/surfaces",
			},
			{
				Names:   []string{"outbox"},
				Summary: "Drafts waiting for your approval, and yours",
				Usage: []string{
					"outbox [list] [--color <when>]", "outbox show <id> [--color <when>]",
					"outbox new <unit|message> <markdown> [--send [--yes]]", "outbox new <unit|message> --react <emoji> [--send [--yes]]",
					"outbox approve <id> [--text <markdown>] [--yes]", "outbox retry <id> [--confirm] [--yes]",
					"outbox edit <id> <markdown>", "outbox discard <id> [--reason <why>]",
				},
				About: "List the drafts waiting on you, proposed, failed or left sending, grouped by kind, or print one whole: its text as it reads, the items it is about and the commands it takes. " +
					"Piped, the list is one tab-separated line per draft: id, kind, state, unit, label, mentions, creation time and first line. " +
					"new writes a reply, or a reaction, on a unit: a Slack message ref (<conversation>/<ts>) or permalink (its thread, or with --react the message itself), a pull request URL or owner/repo#n, or <provider>:<key>. " +
					"It waits in the Outbox for your approval; with --send it posts once you read it and say yes. " +
					"approve shows a draft whole and posts it once you say yes, only from a terminal of your own, never an agent's shell; retry posts a failed one again. " +
					"edit changes a draft's text and discard drops it.",
				Flags: []helpFlag{
					{"--color <when>", "list, show: auto, always or never"},
					{"--react <emoji>", "new: react instead of replying: to the Slack message named, else to the unit's message"},
					{"--send", "new: post it now, through the Outbox, instead of saving it there"},
					{"--text <markdown>", "approve: post this text instead of the draft's"},
					{"--confirm", "retry: post again a draft that may have been posted"},
					{"--yes", "new --send, approve, retry: go ahead without asking"},
					{"--reason <why>", "discard: why the draft is dropped"},
				},
				Examples: []string{"outbox", "outbox new https://acme.slack.com/archives/C0123/p1700000000000100 \"Friday works\"", "outbox approve 12",
					"outbox discard 12 --reason \"answered in person\""},
				Docs: "concepts/approval",
			},
			{
				Names:   []string{"explore"},
				Summary: "Open pull requests of others to help on",
				Usage:   []string{"explore <url|owner/repo|repo|login|@org/team|search>"},
				About: "List open pull requests of others to help on, with their size, review state, checks and your last review: one GitHub search, 30 results. " +
					"A bare word is a repository you follow, else a login, else text searched in the owners of the repositories you follow.",
				Examples: []string{"explore acme/api", "explore @acme/platform", "explore flaky test"},
				Docs:     "concepts/daemon",
			},
		}},
		{"AUTOMATION", []helpTopic{
			{
				Names:   []string{"plan"},
				Summary: "What the workflows would do now, and why",
				Usage: []string{
					"plan [--workflow <name>] [--provider <id>] [--stored]",
					"plan --unit <url|owner/repo#n|provider:key|id>",
					"plan apply <id|url>...", "plan dismiss <id|url>...",
				},
				About: "Print what the workflows would do now, grouped by decision, with each item's id, unit as its row names it, reason, window and gain; it changes nothing. " +
					"--unit tells why nothing happens to a unit, such as session:<session-id>, or to the unit of an item by the id plan printed: its items, then every workflow that gives it none, with the facts it read. " +
					"apply runs pending items now, with the same guards, and dismiss drops them for good; a pull request's URL names its one pending item.",
				Flags: []helpFlag{
					{"--workflow <name>", "Only this workflow's items"},
					{"--provider <id>", "Only the units of this provider: github, session or workspace"},
					{"--stored", "The plan the daemon holds, of every trigger, instead of evaluating the sweep now"},
					{"--unit <unit>", "What every workflow decides for this unit now, a workflow that gives it no item included"},
				},
				Examples: []string{"plan", "plan --unit acme/api#7", "plan apply " + pr},
				Docs:     "guides/workflows",
			},
			{
				Names:   []string{"pause", "resume"},
				Summary: "Stop or restart automatic rounds",
				Usage:   []string{"pause [<url>|<owner/repo>]...", "resume [<url>|<owner/repo>]..."},
				About: "Stop or restart the workflows that do not observe and the board moves on those pull requests and repositories; with none, everywhere. " +
					"Workflows that observe keep planning.",
				Examples: []string{"pause", "pause acme/api", "resume " + pr},
				Docs:     "concepts/workflows",
			},
			{
				Names:   []string{"track", "untrack"},
				Summary: "Follow a pull request out of scope, or stop",
				Usage:   []string{"track <url>", "untrack <url>"},
				About: "Add a pull request to " + name + ", which then follows it like any in scope. " +
					"untrack drops that reason; other reasons still hold it.",
				Examples: []string{"track " + pr},
				Docs:     "concepts/daemon",
			},
			{
				Names:   []string{"mute", "unmute"},
				Summary: "Leave or rejoin the pending views",
				Usage:   []string{"mute <url> [--until <duration> | --until-state]", "unmute <url>"},
				About:   "Take a pull request out of the pending views, for good, for a while, or until who it waits on changes; unmute brings it back.",
				Flags: []helpFlag{
					{"--until <duration>", "Mute for a duration such as 2d or 36h"},
					{"--until-state", "Mute until who the pull request waits on changes"},
				},
				Examples: []string{"mute " + pr + " --until 2d", "unmute " + pr},
				Docs:     "concepts/approval",
			},
			{
				Names:    []string{"workflows"},
				Summary:  "The facts and steps a workflow can use",
				Usage:    []string{"workflows facts [github|session|workspace]", "workflows steps"},
				About:    "Print the facts a workflow's predicates read, by subject, and the steps it may run. The documentation has the manifest reference.",
				Examples: []string{"workflows facts github", "workflows steps"},
				Docs:     "reference/facts",
			},
		}},
		{"PROFILE", []helpTopic{
			{
				Names:   []string{"profile show"},
				Summary: "The blocks that apply to a repository",
				Usage:   []string{"profile show <owner/repo|url> [--intent <intent>] [--show] [--color auto|always|never]"},
				About: "List the profile's blocks per intent, their conditions and which of them apply to that repository or pull request. " +
					"--show prints the instructions a turn there reads.",
				Flags: []helpFlag{
					{"--intent <intent>", "Only this intent's blocks"},
					{"--show", "Print the instructions a turn there would read"},
					colorFlag,
				},
				Examples: []string{"profile show acme/api", "profile show " + pr + " --intent review --show"},
				Docs:     "guides/review-profile",
			},
			{
				Names:    []string{"profile where"},
				Summary:  "The block a new rule goes in",
				Usage:    []string{"profile where <intent> [conditions]"},
				About:    "Name the block of the profile a rule with these conditions goes in, and the blocks that join it in a turn.",
				Flags:    conditionFlags(),
				Examples: []string{"profile where review --organization acme --repo api"},
				Docs:     "reference/profile-blocks",
			},
			{
				Names:   []string{"profile add"},
				Summary: "Add a rule to the profile",
				Usage:   []string{"profile add <intent>[,<intent>...] [conditions] [flags] [-- <text>]"},
				About: "Add a rule to the block these conditions name, creating the block when it is new. " +
					"It prints the diff and writes it with --apply; the rule's text comes after --, else from standard input.",
				Flags: append(conditionFlags(),
					helpFlag{"--section <heading>", "The ## heading the rule goes under"},
					helpFlag{"--title <title>", "The block's heading, for a new block"},
					helpFlag{"--override", "The new block replaces the guidance above it"},
					helpFlag{"--paths <glob>", "A glob a changed file must match, for a new block; repeatable"},
					helpFlag{"--apply", "Write the change; without it, print the diff"},
				),
				Examples: []string{"profile add review --organization acme -- Every migration needs a down step.", "profile add nudge --apply -- Keep nudges to two sentences."},
				Docs:     "guides/review-profile",
			},
			{
				Names:    []string{"profile check"},
				Summary:  "Load the profile and list its problems",
				Usage:    []string{"profile check [<dir>] [--color auto|always|never]"},
				About:    "Load the profile, the configured one or the one in <dir>, and list its intents, blocks and conditions. It exits 1 when anything fails to load.",
				Flags:    []helpFlag{colorFlag},
				Examples: []string{"profile check", "profile check ~/work/my-profile"},
				Docs:     "guides/review-profile",
			},
			{
				Names:   []string{"profile try"},
				Summary: "A review round that publishes nothing",
				Usage:   []string{"profile try <url>"},
				About: "Run a review round in a throwaway worktree with the review profile and print the review it would publish. " +
					"Nothing reaches the pull request.",
				Examples: []string{"profile try " + pr},
				Docs:     "guides/review-profile",
			},
			{
				Names:    []string{"profile schema"},
				Summary:  "The JSON Schemas of the profile files, for editors",
				Usage:    []string{"profile schema [--workflow|--actions|--config]"},
				About:    "Print the JSON Schema of intent.toml, or of workflow.toml, actions.toml or config.json, for an editor. Every schema comes from the grammar the loaders check.",
				Flags:    []helpFlag{{"--workflow", "The schema of workflow.toml"}, {"--actions", "The schema of actions.toml"}, {"--config", "The schema of config.json"}},
				Examples: []string{"profile schema > intent.schema.json", "profile schema --config > config.schema.json"},
				Docs:     "reference/language",
			},
		}},
		{"SLACK", []helpTopic{
			{
				Names:   []string{"slack login", "slack logout"},
				Summary: "Connect to Slack, or disconnect",
				Usage:   []string{"slack login [--no-browser]", "slack login --app-token", "slack logout"},
				About: "Log in to Slack in the browser; it needs slack.client_id in the config. " +
					"--app-token saves the app-level token read from standard input. logout revokes and deletes the tokens.",
				Flags: []helpFlag{
					{"--no-browser", "Print the login URL instead of opening the browser"},
					{"--app-token", "Save the app-level token read from standard input"},
				},
				Examples: []string{"slack login", "pbpaste | " + brand.Command + " slack login --app-token"},
				Docs:     "guides/slack",
			},
			{
				Names:    []string{"slack status"},
				Summary:  "Who you are logged in to Slack as",
				Usage:    []string{"slack status"},
				About:    "Show the Slack login: the team, the user, when the token expires and whether the app-level token is stored.",
				Examples: []string{"slack status"},
				Docs:     "guides/slack",
			},
			{
				Names:   []string{"slack send"},
				Summary: "Post as you to a thread, channel or person",
				Usage:   []string{"slack send <target> <text> [--thread=false] [--broadcast] [--yes]"},
				About: "Post as you to a message, by its ref (<conversation>/<ts>) or permalink, a #channel, channel id, @handle, email or user id. " +
					"A message target replies in its thread, through the Outbox: a draft of yours on the thread, approved once you say yes, so it feeds the thread's items and shows in the Logbook. " +
					"It asks first unless --yes.",
				Flags: []helpFlag{
					{"--thread=false", "Post to a message's conversation instead of its thread"},
					{"--broadcast", "Also send a thread reply to the conversation"},
					{"--yes", "Post without asking"},
				},
				Examples: []string{"slack send '#releases' 'api 1.4 is out'", "slack send @maria 'can you look at acme/api#7?'"},
				Docs:     "guides/slack",
			},
			{
				Names:    []string{"slack follow", "slack unfollow"},
				Summary:  "Admit every message of a thread, or stop",
				Usage:    []string{"slack follow <message>", "slack unfollow <message>"},
				About:    "Follow the Slack thread of a message, named by its ref (<conversation>/<ts>) or permalink, so every message of it is admitted, or stop following it.",
				Examples: []string{"slack follow https://acme.slack.com/archives/C024BE91L/p1700000000000100"},
				Docs:     "guides/slack",
			},
		}},
		{"SETUP", []helpTopic{
			{
				Names:   []string{"install"},
				Summary: "Write the hooks, MCP and skills into agent homes",
				Usage:   []string{"install [--home <dir>]... [--dry-run] [--print-skill <name>]"},
				About: "Write into every agent home " + name + " finds, or each --home: the MCP registration, the Stop, SessionStart and UserPromptSubmit hooks, " +
					"the turn_end permission and the bundled skills, also into ~/.agents. settings.json and the MCP registration are backed up before their first change; " +
					"a skill is replaced only while it is unedited. Run it again after each upgrade.",
				Flags: []helpFlag{
					{"--home <dir>", "An agent home to install into, instead of every one found; repeatable"},
					{"--dry-run", "Print what would change and change nothing"},
					{"--print-skill <name>", "Print the bundled skill with this name and exit"},
				},
				Examples: []string{"install", "install --home ~/agent-home --dry-run"},
				Docs:     "getting-started/install",
			},
			{
				Names:   []string{"app install"},
				Summary: "macOS: the app, menu bar and the daemon at login",
				Usage:   []string{"app install [--no-menu-bar]"},
				About: "Install " + app + ".app, with the menu bar and notifications, and the LaunchAgents that run the daemon and the menu bar at login. " +
					"--no-menu-bar installs only the daemon's, which keeps serve running.",
				Flags:    []helpFlag{{"--no-menu-bar", "Install only the daemon's LaunchAgent"}},
				Examples: []string{"app install"},
				Docs:     "getting-started/install",
			},
			{
				Names:   []string{"trust"},
				Summary: "Trust a repository in an agent home",
				Usage:   []string{"trust [--home <dir>] [--exact] <folder|workspace>"},
				About: "Trust a folder's repository in an agent home, the configured one by default, as accepting the agent's trust dialog would; it covers every worktree of it. " +
					name + " never trusts a folder unless you run this.",
				Flags: []helpFlag{
					{"--home <dir>", "The agent home that trusts it"},
					{"--exact", "Trust the folder alone rather than its repository's main checkout"},
				},
				Examples: []string{"trust ~/src/api"},
				Docs:     "guides/troubleshooting",
			},
			{
				Names:   []string{"uninstall"},
				Summary: "Take out what install and app install set up",
				Usage:   []string{"uninstall [--purge] [--dry-run] [--yes]"},
				About: "Take out the daemon's and the menu bar's LaunchAgents, " + app + ".app, the agent homes' entries, the unchanged bundled skills and the Slack tokens. " +
					"It keeps the config and the data unless --purge, and asks first unless --yes.",
				Flags: []helpFlag{
					{"--purge", "Also delete the state and config directories: the store and its backups, MCP key, dossiers, logs, config and review profiles"},
					{"--dry-run", "Print what would be removed and change nothing"},
					{"--yes", "Remove without asking"},
				},
				Examples: []string{"uninstall --dry-run", "uninstall"},
				Docs:     "guides/uninstall",
			},
			{
				Names:   []string{"store migrate"},
				Summary: "Migrate the store to this release's schema",
				Usage:   []string{"store migrate [--dry-run] [--yes]"},
				About: "Take the store to this release's schema version: copy it into <state>/backups, run each migration in its own transaction and check the result. " +
					"A release's daemon does this on its own when it starts; run it when a build that is not a release asks for it, or to retry a migration that failed and see each step. " +
					"It asks first unless --yes.",
				Flags: []helpFlag{
					{"--dry-run", "Print the migrations, the backup and the space it needs, and change nothing"},
					{"--yes", "Migrate without asking"},
				},
				Examples: []string{"store migrate --dry-run", "store migrate"},
				Docs:     "guides/upgrade",
			},
			{
				Names:   []string{"store restore"},
				Summary: "Put a backup of the store back",
				Usage:   []string{"store restore [--list] [<backup>] [--yes]"},
				About: "Replace the store with a backup from <state>/backups, by default the newest this release reads, keeping the replaced store there as a pre-restore copy. " +
					"What changed since the backup is lost. A backup older than this release is migrated after. It asks first unless --yes.",
				Flags: []helpFlag{
					{"--list", "List the backups with their schema versions and dates"},
					{"--yes", "Restore without asking"},
				},
				Examples: []string{"store restore --list", "store restore"},
				Docs:     "guides/upgrade",
			},
			{
				Names:    []string{"config get"},
				Summary:  "Print a configuration value",
				Usage:    []string{"config get <key>"},
				About:    "Print a configuration value, defaults included. A dot names a nested key.",
				Examples: []string{"config get agent", "config get sweep.fast"},
				Docs:     "reference/configuration",
			},
			{
				Names:    []string{"version"},
				Summary:  "Print the version",
				Usage:    []string{"version"},
				About:    "Print the version of " + name + ".",
				Examples: []string{"version"},
			},
		}},
		{"TROUBLESHOOTING", []helpTopic{
			{
				Names:   []string{"timings"},
				Summary: "Review, merge and turn durations, waits, tokens",
				Usage:   []string{"timings [--since <days>] [--prs] [--turns] [--tags] [--json]"},
				About: "What " + name + " recorded: time to review, approve and merge, lead time, who pull requests waited on, " +
					"turn and brief durations, waits, tokens and a change failure proxy.",
				Flags: []helpFlag{
					{"--since <days>", "The days to look back (default 30)"},
					{"--prs", "Also list each pull request with its milestones"},
					{"--turns", "Also list each turn with its duration"},
					{"--tags", "Also list each tag key's values and the dropped spellings"},
					{"--json", "Print the report as JSON"},
				},
				Examples: []string{"timings", "timings --since 7 --prs"},
				Docs:     "concepts/daemon",
			},
			{
				Names:    []string{"intake"},
				Summary:  "Follow, start or cancel an intake",
				Usage:    []string{"intake <n>", "intake without <n>", "intake cancel <n>"},
				About:    "Follow an intake until its round starts, start it without waiting for its brief, or cancel it. Ctrl-C stops following, not the intake.",
				Examples: []string{"intake 42", "intake without 42"},
				Docs:     "concepts/brief",
			},
			{
				Names:    []string{"brief refresh"},
				Summary:  "Make a pull request's brief again",
				Usage:    []string{"brief refresh <url>"},
				About:    "Make a pull request's brief again now. It needs brief.enabled.",
				Examples: []string{"brief refresh " + pr},
				Docs:     "concepts/brief",
			},
			{
				Names:   []string{"turn show"},
				Summary: "A pull request's turns and their records",
				Usage:   []string{"turn show <url> [--json] [--color auto|always|never]"},
				About: "Print the pull request's turns with their records and answers. " +
					"Piped or with --json, it prints JSON with each one's instructions.",
				Flags:    []helpFlag{{"--json", "Print the book as JSON, as off a terminal"}, colorFlag},
				Examples: []string{"turn show " + pr},
				Docs:     "concepts/turns",
			},
			{
				Names:   []string{"probe"},
				Summary: "Prove an agent home end to end",
				Usage:   []string{"probe --workspace <workspace-id-or-path> [--home <dir>] [--model <name>]"},
				About: "The daemon sends a self-test turn into a new session of the " + manager + " workspace and prints its turn as JSON, " +
					"which proves the agent home, its hooks and its MCP registration work together.",
				Flags: []helpFlag{
					{"--workspace <workspace-id-or-path>", "The workspace the probe's session runs in: its " + manager + " id or its worktree path"},
					{"--home <dir>", "The agent home the session runs with (default: the agent's own)"},
					{"--model <name>", "The agent's model (default: the agent's own)"},
				},
				Examples: []string{"probe --workspace ws-123"},
				Docs:     "guides/troubleshooting",
			},
		}},
		{"FOR AGENTS AND SCRIPTS", []helpTopic{
			{
				Names:   []string{"instructions"},
				Summary: "Run a turn by hand from an agent session",
				Usage:   []string{"instructions <intent> [<url>] [<intent flags>] [-- <guidance>]", "instructions <intent> --turn <id>"},
				About: "Run a turn of an intent by hand from an agent session: the daemon prepares this worktree, starts the turn on this session and prints its instructions. " +
					"--turn starts the turn " + name + " sent instead and prints its instructions.",
				Flags: []helpFlag{
					{"--turn <id>", "Start the turn " + name + " sent with this id"},
					{"--agent <name>", "The agent that reads them (default: the configured one)"},
				},
				Examples: []string{"instructions review " + pr + " -- focus on the API", "instructions review --turn 66ea4cdcae20"},
				Docs:     "concepts/turns",
			},
			{
				Names:   []string{"turn end", "turn answer"},
				Summary: "Record how a turn ended; read your answer",
				Usage:   []string{"turn end <url> --turn <id> --file <path>", "turn answer <url> [--message <id>]"},
				About: "end records how this session's turn ended, from a JSON file, when the session has no " + name + " MCP tool; the file is deleted once stored. " +
					"answer prints the user's answers to this session's Inbox messages that it has not read, or, with --message, every answer to that message, and marks them read.",
				Flags: []helpFlag{
					{"--turn <id>", "The id on the first line of the turn's instructions"},
					{"--file <path>", "The JSON file that holds the record"},
				},
				Examples: []string{"turn end " + pr + " --turn 66ea4cdcae20 --file /tmp/record.json", "turn answer " + pr},
				Docs:     "concepts/turns",
			},
			{
				Names:   []string{"hook"},
				Summary: "The agent hooks install writes",
				Usage:   []string{"hook session-start [--agent <name>]", "hook stop [--agent <name>]", "hook prompt [--agent <name>]", "hook mcp-headers"},
				About: "The agent's hooks, which install writes into each agent home; they read the payload on standard input and never block by failing. " +
					"prompt records when you type, which closing a session reads. " +
					"mcp-headers prints the session's handle as the header of " + name + "'s MCP server; the MCP registration runs it.",
				Flags:    []helpFlag{{"--agent <name>", "The agent whose hook this is"}},
				Examples: []string{"hook stop < payload.json"},
				Docs:     "concepts/turns",
			},
			{
				Names:   []string{"serve"},
				Summary: "Run the daemon",
				Usage:   []string{"serve"},
				About: "Run the daemon, headless: the store, the sweeps, the cockpit and " + name + "'s MCP server. " +
					"The daemon's LaunchAgent runs it.",
				Examples: []string{"serve"},
				Docs:     "concepts/daemon",
			},
			{
				Names:    []string{"app"},
				Summary:  "macOS: the menu bar and notifications",
				Usage:    []string{"app"},
				About:    "Run the menu bar and the notifications, a client of the daemon. " + app + ".app runs it.",
				Examples: []string{"app"},
				Docs:     "concepts/surfaces",
			},
		}},
	}
}
