---
title: Commands
description: Every command of the CLI, generated from its help.
---

Every command but `doctor`'s local checks, `install`, `uninstall`, `config get` and `app install` talks to the daemon. Run `{{command}} --help` for the same list, including the commands of the intents your profile adds, and `{{command}} <command> --help` for one command's help.

## Get started

### init

Set up from zero: config, agent homes, the app.

```sh
{{command}} init [flags]
```

Check the prerequisites, write the config, log in to Slack, install into the agent homes and the app, then run doctor. At a terminal it asks for each answer; elsewhere every answer comes from the flags.

| Flag | Description |
| --- | --- |
| `--projects-dir <dir>` | Where review clones a repository the workspace manager does not know |
| `--agent <name>` | The coding agent that runs the turns |
| `--scope-repo <owner/repo>` | A repository to follow; repeatable |
| `--scope-team <org/slug>` | A team whose review requests and pull requests to follow; repeatable |
| `--scope-person <login>` | A login whose pull requests to follow; repeatable |
| `--profile <dir>` | A personal profile directory, created with example blocks when absent |
| `--profile-default` | Use the bundled profile alone |
| `--slack-client-id <id>` | The client id of your Slack app |
| `--skip-slack` | Leave Slack as it is |
| `--no-install` | Install into neither the agent homes nor the app |
| `--yes` | Say yes to every confirmation: overwrite the config, install, app install |
| `--color auto\|always\|never` | Lay the output out for a terminal; auto styles it on a terminal alone |

Examples:

```sh
{{command}} init
{{command}} init --scope-repo acme/api --profile-default --skip-slack --yes
```

See [Set up with init](/getting-started/init/).

### doctor

Diagnose the setup without changing anything.

```sh
{{command}} doctor [--git] [--color auto|always|never]
{{command}} doctor --load [--json] [--color auto|always|never]
```

Check the prerequisites, the config, the agent homes, the profile and the daemon, and print the command that fixes each problem. The checks of {{name}}'s state ask the daemon. With `--load`, print what the daemon spent since it started instead.

| Flag | Description |
| --- | --- |
| `--git` | Also check git access live with an ls-remote, as the daemon would run it |
| `--load` | The calls, spawns and points by resource of every job, route and loop since the daemon started |
| `--json` | With `--load`, print the report as JSON |
| `--color auto\|always\|never` | Lay the output out for a terminal; auto styles it on a terminal alone |

Examples:

```sh
{{command}} doctor
{{command}} doctor --load
```

See [Troubleshooting](/guides/troubleshooting/).

## Pull requests

### act

Resolve a unit's open items, replies as drafts.

```sh
{{command}} act <url|ref|permalink|slack:key> [guidance...]
```

Act on the open items of a unit without touching its code: settle what you can and propose the replies it needs as drafts for you to approve; Address works on the code of a pull request you develop. The target is a pull request URL, a Slack message ref (&lt;conversation&gt;/&lt;ts&gt;) or permalink, or &lt;provider&gt;:&lt;key&gt; of a unit the intent serves; words after it are guidance for the turn.

Examples:

```sh
{{command}} act https://github.com/acme/api/pull/7
{{command}} act https://acme.slack.com/archives/C0123/p1700000000000100 say we ship Friday
```

See [Tasks and views](/concepts/tasks/).

### address

Triage the reviews and failing checks of yours.

```sh
{{command}} address <url> [--self-review] [--take-over] [--no-wait] [--no-brief] [guidance...]
```

Triage the reviews and failing checks of a pull request you develop, in its workspace, changing its code where they call for it; Act only settles open items and drafts replies. The target is a pull request URL; words after it are guidance for the turn. It runs as an intake this command follows; Ctrl-C stops following, not the intake.

| Flag | Description |
| --- | --- |
| `--self-review` | Reviews the change with the review profile first, against the guidance as its plan, and fixes what it finds; nothing of that review is published |
| `--take-over` | Takes it over first |
| `--no-wait` | Print the intake's number instead of following it |
| `--no-brief` | Start the round without its brief |

Examples:

```sh
{{command}} address https://github.com/acme/api/pull/7
{{command}} address --self-review https://github.com/acme/api/pull/7 the plan is in the description
```

See [Address reviews on your pull request](/guides/address/).

### nudge

Ask whoever it waits on for the next step.

```sh
{{command}} nudge <url> [guidance...]
```

Ask whoever the pull request waits on for the next step, in a comment; guidance is the topic. The target is a pull request URL; words after it are guidance for the turn.

Examples:

```sh
{{command}} nudge https://github.com/acme/api/pull/7
{{command}} nudge https://github.com/acme/api/pull/7 the release is on Friday
```

See [Nudge whoever a pull request waits on](/guides/nudge/).

### review

Review someone else's pull request in its own worktree.

```sh
{{command}} review <url> [--no-wait] [--no-brief] [guidance...]
```

Start or continue a review round on someone else's pull request, in a dedicated workspace. The target is a pull request URL; words after it are guidance for the turn. It runs as an intake this command follows; Ctrl-C stops following, not the intake.

| Flag | Description |
| --- | --- |
| `--no-wait` | Print the intake's number instead of following it |
| `--no-brief` | Start the round without its brief |

Examples:

```sh
{{command}} review https://github.com/acme/api/pull/7
{{command}} review https://github.com/acme/api/pull/7 look at the migration first
```

See [Review someone else's pull request](/guides/review/).

### start

Start new work from a brief.

```sh
{{command}} start [task:<id>] [--repo <repo>] [--manager <name>] [--parent <worktree>] [guidance...]
```

Start new work from a brief: in a fresh worktree of a repository, or with no checkout. The target is entry:&lt;id&gt;, or none to capture the brief as a new task; words after it are guidance for the turn.

| Flag | Description |
| --- | --- |
| `--repo <repo>` | Owner/name of the repository to work in; without it the session has no checkout |
| `--manager <name>` | The workspace manager that creates the worktree, instead of the configured ones |
| `--parent <worktree>` | The worktree the new one goes under, where its manager keeps parents; the one the command runs in by default |

Examples:

```sh
{{command}} start --repo acme/api add retries to the webhook sender; done when a failed delivery is retried three times
{{command}} start entry:e-3f9a2c4b5d6e7f80 --repo acme/api
```

See [Tasks and views](/concepts/tasks/).

### take-over

Make a pull request someone else opened yours.

```sh
{{command}} take-over <url>
```

Make a pull request someone else opened yours to develop: from then on {{name}} treats it as one of yours, and address works on it.

Examples:

```sh
{{command}} take-over https://github.com/acme/api/pull/7
```

See [Take over a pull request](/guides/take-over/).

### remove

Delete the workspace of a merged or closed one.

```sh
{{command}} remove <url> [--force]
```

Remove the workspace of a merged or closed pull request when nothing in it would be lost. `--force` removes it whatever it holds, for good; an open pull request's workspace stays.

| Flag | Description |
| --- | --- |
| `--force` | Remove whatever the workspace holds |

Examples:

```sh
{{command}} remove https://github.com/acme/api/pull/7
```

See [Workspaces and board columns](/concepts/board/).

## What waits on you

### status

Pull requests by view, those needing you first.

```sh
{{command}} status [--color auto|always|never]
```

List the daemon's pull requests by view, those needing you first. Piped, it prints one tab-separated line per pull request and view.

| Flag | Description |
| --- | --- |
| `--color auto\|always\|never` | Lay the output out for a terminal; auto styles it on a terminal alone |

Examples:

```sh
{{command}} status
{{command}} status | grep needs-me
```

See [Cockpit, terminal client and menu bar](/concepts/surfaces/).

### tui

The same views in the terminal, one-key actions.

```sh
{{command}} tui
```

The Needs me, In loop and My PRs views in the terminal, with one-key actions on the selected pull request.

Examples:

```sh
{{command}} tui
```

See [Cockpit, terminal client and menu bar](/concepts/surfaces/).

### open

The cockpit, a workspace or a pull request.

```sh
{{command}} open [<url>]
{{command}} open [<url>] --print [--view <view>]
```

Open the workspace of a pull request in Claude Desktop or Orca, or the pull request itself when it has none. With no URL, open the cockpit. With `--print`, print the cockpit URL with its token instead of opening it.

| Flag | Description |
| --- | --- |
| `--print` | Print the cockpit URL with its token instead of opening it |
| `--view <view>` | The cockpit view: needs-me, stalled, mine, loop, dash, done, all, recent, explore |

Examples:

```sh
{{command}} open
{{command}} open https://github.com/acme/api/pull/7
{{command}} open --print --view needs-me
```

See [Cockpit, terminal client and menu bar](/concepts/surfaces/).

### outbox

Drafts waiting for your approval, and yours.

```sh
{{command}} outbox [list] [--color <when>]
{{command}} outbox show <id> [--color <when>]
{{command}} outbox new <unit|message> <markdown> [--send [--yes]]
{{command}} outbox new <unit|message> --react <emoji> [--send [--yes]]
{{command}} outbox approve <id> [--text <markdown>] [--yes]
{{command}} outbox retry <id> [--confirm] [--yes]
{{command}} outbox edit <id> <markdown>
{{command}} outbox discard <id> [--reason <why>]
```

List the drafts waiting on you, proposed, failed or left sending, grouped by kind, or print one whole: its text as it reads, the items it is about and the commands it takes. Piped, the list is one tab-separated line per draft: id, kind, state, unit, label, mentions, creation time and first line. new writes a reply, or a reaction, on a unit: a Slack message ref (&lt;conversation&gt;/&lt;ts&gt;) or permalink (its thread, or with `--react` the message itself), a pull request URL or owner/repo#n, or &lt;provider&gt;:&lt;key&gt;. It waits in the Outbox for your approval; with `--send` it posts once you read it and say yes. approve shows a draft whole and posts it once you say yes, only from a terminal of your own, never an agent's shell; retry posts a failed one again. edit changes a draft's text and discard drops it.

| Flag | Description |
| --- | --- |
| `--color <when>` | list, show: auto, always or never |
| `--react <emoji>` | new: react instead of replying: to the Slack message named, else to the unit's message |
| `--send` | new: post it now, through the Outbox, instead of saving it there |
| `--text <markdown>` | approve: post this text instead of the draft's |
| `--confirm` | retry: post again a draft that may have been posted |
| `--yes` | new `--send`, approve, retry: go ahead without asking |
| `--reason <why>` | discard: why the draft is dropped |

Examples:

```sh
{{command}} outbox
{{command}} outbox new https://acme.slack.com/archives/C0123/p1700000000000100 "Friday works"
{{command}} outbox approve 12
{{command}} outbox discard 12 --reason "answered in person"
```

See [What waits for you](/concepts/approval/).

### explore

Open pull requests of others to help on.

```sh
{{command}} explore <url|owner/repo|repo|login|@org/team|search>
```

List open pull requests of others to help on, with their size, review state, checks and your last review: one GitHub search, 30 results. A bare word is a repository you follow, else a login, else text searched in the owners of the repositories you follow.

Examples:

```sh
{{command}} explore acme/api
{{command}} explore @acme/platform
{{command}} explore flaky test
```

See [The daemon](/concepts/daemon/).

## Automation

### plan

What the workflows would do now, and why.

```sh
{{command}} plan [--workflow <name>] [--provider <id>] [--stored]
{{command}} plan --unit <url|owner/repo#n|provider:key|id>
{{command}} plan apply <id|url>...
{{command}} plan dismiss <id|url>...
```

Print what the workflows would do now, grouped by decision, with each item's id, unit as its row names it, reason, window and gain; it changes nothing. `--unit` tells why nothing happens to a unit, such as session:&lt;session-id&gt;, or to the unit of an item by the id plan printed: its items, then every workflow that gives it none, with the facts it read. apply runs pending items now, with the same guards, and dismiss drops them for good; a pull request's URL names its one pending item.

| Flag | Description |
| --- | --- |
| `--workflow <name>` | Only this workflow's items |
| `--provider <id>` | Only the units of this provider: github, session or workspace |
| `--stored` | The plan the daemon holds, of every trigger, instead of evaluating the sweep now |
| `--unit <unit>` | What every workflow decides for this unit now, a workflow that gives it no item included |

Examples:

```sh
{{command}} plan
{{command}} plan --unit acme/api#7
{{command}} plan apply https://github.com/acme/api/pull/7
```

See [Tune workflows](/guides/workflows/).

### pause/resume

Stop or restart automatic rounds.

```sh
{{command}} pause [<url>|<owner/repo>]...
{{command}} resume [<url>|<owner/repo>]...
```

Stop or restart the workflows that do not observe and the board moves on those pull requests and repositories; with none, everywhere. Workflows that observe keep planning.

Examples:

```sh
{{command}} pause
{{command}} pause acme/api
{{command}} resume https://github.com/acme/api/pull/7
```

See [The sweep and workflows](/concepts/workflows/).

### track/untrack

Follow a pull request out of scope, or stop.

```sh
{{command}} track <url>
{{command}} untrack <url>
```

Add a pull request to {{name}}, which then follows it like any in scope. untrack drops that reason; other reasons still hold it.

Examples:

```sh
{{command}} track https://github.com/acme/api/pull/7
```

See [The daemon](/concepts/daemon/).

### mute/unmute

Leave or rejoin the pending views.

```sh
{{command}} mute <url> [--until <duration> | --until-state]
{{command}} unmute <url>
```

Take a pull request out of the pending views, for good, for a while, or until who it waits on changes; unmute brings it back.

| Flag | Description |
| --- | --- |
| `--until <duration>` | Mute for a duration such as 2d or 36h |
| `--until-state` | Mute until who the pull request waits on changes |

Examples:

```sh
{{command}} mute https://github.com/acme/api/pull/7 --until 2d
{{command}} unmute https://github.com/acme/api/pull/7
```

See [What waits for you](/concepts/approval/).

### workflows

The facts and steps a workflow can use.

```sh
{{command}} workflows facts [github|session|workspace]
{{command}} workflows steps
```

Print the facts a workflow's predicates read, by subject, and the steps it may run. The documentation has the manifest reference.

Examples:

```sh
{{command}} workflows facts github
{{command}} workflows steps
```

See [Facts and steps](/reference/facts/).

## Profile

### profile show

The blocks that apply to a repository.

```sh
{{command}} profile show <owner/repo|url> [--intent <intent>] [--show] [--color auto|always|never]
```

List the profile's blocks per intent, their conditions and which of them apply to that repository or pull request. `--show` prints the instructions a turn there reads.

| Flag | Description |
| --- | --- |
| `--intent <intent>` | Only this intent's blocks |
| `--show` | Print the instructions a turn there would read |
| `--color auto\|always\|never` | Lay the output out for a terminal; auto styles it on a terminal alone |

Examples:

```sh
{{command}} profile show acme/api
{{command}} profile show https://github.com/acme/api/pull/7 --intent review --show
```

See [Write your review profile](/guides/review-profile/).

### profile where

The block a new rule goes in.

```sh
{{command}} profile where <intent> [conditions]
```

Name the block of the profile a rule with these conditions goes in, and the blocks that join it in a turn.

| Flag | Description |
| --- | --- |
| `--<dimension> <value>` | A condition of the block, one flag per dimension: `--organization`, `--repo`, `--user`, `--team`, `--association`, `--label`, `--base`, `--stage`, `--reviewer`, `--kind`, `--size`, `--risk` |
| `--tag <key>=<value>` | A tag condition; repeatable |

Examples:

```sh
{{command}} profile where review --organization acme --repo api
```

See [Profile format](/reference/profile-blocks/).

### profile add

Add a rule to the profile.

```sh
{{command}} profile add <intent>[,<intent>...] [conditions] [flags] [-- <text>]
```

Add a rule to the block these conditions name, creating the block when it is new. It prints the diff and writes it with `--apply`; the rule's text comes after --, else from standard input.

| Flag | Description |
| --- | --- |
| `--<dimension> <value>` | A condition of the block, one flag per dimension: `--organization`, `--repo`, `--user`, `--team`, `--association`, `--label`, `--base`, `--stage`, `--reviewer`, `--kind`, `--size`, `--risk` |
| `--tag <key>=<value>` | A tag condition; repeatable |
| `--section <heading>` | The ## heading the rule goes under |
| `--title <title>` | The block's heading, for a new block |
| `--override` | The new block replaces the guidance above it |
| `--paths <glob>` | A glob a changed file must match, for a new block; repeatable |
| `--apply` | Write the change; without it, print the diff |

Examples:

```sh
{{command}} profile add review --organization acme -- Every migration needs a down step.
{{command}} profile add nudge --apply -- Keep nudges to two sentences.
```

See [Write your review profile](/guides/review-profile/).

### profile check

Load the profile and list its problems.

```sh
{{command}} profile check [<dir>] [--color auto|always|never]
```

Load the profile, the configured one or the one in &lt;dir&gt;, and list its intents, blocks and conditions. It exits 1 when anything fails to load.

| Flag | Description |
| --- | --- |
| `--color auto\|always\|never` | Lay the output out for a terminal; auto styles it on a terminal alone |

Examples:

```sh
{{command}} profile check
{{command}} profile check ~/work/my-profile
```

See [Write your review profile](/guides/review-profile/).

### profile try

A review round that publishes nothing.

```sh
{{command}} profile try <url>
```

Run a review round in a throwaway worktree with the review profile and print the review it would publish. Nothing reaches the pull request.

Examples:

```sh
{{command}} profile try https://github.com/acme/api/pull/7
```

See [Write your review profile](/guides/review-profile/).

### profile schema

The JSON Schemas of the profile files, for editors.

```sh
{{command}} profile schema [--workflow|--actions|--tasks|--views|--gates|--records|--keys|--config]
```

Print the JSON Schema of intent.toml, or of workflow.toml, actions.toml, tasks.toml, views.toml, gates.toml, records.toml, keys.toml or config.toml, for an editor. Every schema comes from the grammar the loaders check.

| Flag | Description |
| --- | --- |
| `--workflow` | The schema of workflow.toml |
| `--actions` | The schema of actions.toml |
| `--tasks` | The schema of tasks.toml |
| `--views` | The schema of views.toml |
| `--gates` | The schema of gates.toml |
| `--records` | The schema of records.toml |
| `--keys` | The schema of keys.toml |
| `--config` | The schema of config.toml |

Examples:

```sh
{{command}} profile schema > intent.schema.json
{{command}} profile schema --config > config.schema.json
```

See [Profile language](/reference/language/).

## Slack

### slack login/logout

Connect to Slack, or disconnect.

```sh
{{command}} slack login [--no-browser]
{{command}} slack login --app-token
{{command}} slack logout
```

Log in to Slack in the browser; it needs plugins.slack.client_id in the config. `--app-token` saves the app-level token read from standard input. logout revokes and deletes the tokens.

| Flag | Description |
| --- | --- |
| `--no-browser` | Print the login URL instead of opening the browser |
| `--app-token` | Save the app-level token read from standard input |

Examples:

```sh
{{command}} slack login
{{command}} pbpaste | {{command}} slack login --app-token
```

See [Connect Slack](/guides/slack/).

### slack status

Who you are logged in to Slack as.

```sh
{{command}} slack status
```

Show the Slack login: the team, the user, when the token expires and whether the app-level token is stored.

Examples:

```sh
{{command}} slack status
```

See [Connect Slack](/guides/slack/).

### slack send

Post as you to a thread, channel or person.

```sh
{{command}} slack send <target> <text> [--thread=false] [--broadcast] [--yes]
```

Post as you to a message, by its ref (&lt;conversation&gt;/&lt;ts&gt;) or permalink, a #channel, channel id, @handle, email or user id. A message target replies in its thread, through the Outbox: a draft of yours on the thread, approved once you say yes, so it feeds the thread's items and shows in the Logbook. It asks first unless `--yes`.

| Flag | Description |
| --- | --- |
| `--thread=false` | Post to a message's conversation instead of its thread |
| `--broadcast` | Also send a thread reply to the conversation |
| `--yes` | Post without asking |

Examples:

```sh
{{command}} slack send '#releases' 'api 1.4 is out'
{{command}} slack send @maria 'can you look at acme/api#7?'
```

See [Connect Slack](/guides/slack/).

### slack follow/unfollow

Admit every message of a thread, or stop.

```sh
{{command}} slack follow <message>
{{command}} slack unfollow <message>
```

Follow the Slack thread of a message, named by its ref (&lt;conversation&gt;/&lt;ts&gt;) or permalink, so every message of it is admitted, or stop following it.

Examples:

```sh
{{command}} slack follow https://acme.slack.com/archives/C024BE91L/p1700000000000100
```

See [Connect Slack](/guides/slack/).

## Setup

### install

Write the hooks, MCP and skills into agent homes.

```sh
{{command}} install [--home <dir>]... [--dry-run] [--print-skill <name>]
```

Write into every agent home {{name}} finds, or each `--home`: the MCP registration, the Stop, SessionStart and UserPromptSubmit hooks, the turn_end permission and the bundled skills, also into ~/.agents. settings.json and the MCP registration are backed up before their first change; a skill is replaced only while it is unedited. Run it again after each upgrade.

| Flag | Description |
| --- | --- |
| `--home <dir>` | An agent home to install into, instead of every one found; repeatable |
| `--dry-run` | Print what would change and change nothing |
| `--print-skill <name>` | Print the bundled skill with this name and exit |

Examples:

```sh
{{command}} install
{{command}} install --home ~/agent-home --dry-run
```

See [Install](/getting-started/install/).

### app install

macOS: the app, menu bar and the daemon at login.

```sh
{{command}} app install [--no-menu-bar]
```

Install {{name}}.app, with the menu bar and notifications, and the LaunchAgents that run the daemon and the menu bar at login. `--no-menu-bar` installs only the daemon's, which keeps serve running.

| Flag | Description |
| --- | --- |
| `--no-menu-bar` | Install only the daemon's LaunchAgent |

Examples:

```sh
{{command}} app install
```

See [Install](/getting-started/install/).

### trust

Trust a repository in an agent home.

```sh
{{command}} trust [--home <dir>] [--exact] <folder|workspace>
```

Trust a folder's repository in an agent home, the configured one by default, as accepting the agent's trust dialog would; it covers every worktree of it. {{name}} never trusts a folder unless you run this, or the folder is a repository it just cloned for a project whose trust_clones is on.

| Flag | Description |
| --- | --- |
| `--home <dir>` | The agent home that trusts it |
| `--exact` | Trust the folder alone rather than its repository's main checkout |

Examples:

```sh
{{command}} trust ~/src/api
```

See [Troubleshooting](/guides/troubleshooting/).

### uninstall

Take out what install and app install set up.

```sh
{{command}} uninstall [--purge] [--dry-run] [--yes]
```

Take out the daemon's and the menu bar's LaunchAgents, {{name}}.app, the agent homes' entries, the unchanged bundled skills and the Slack tokens. It keeps the config and the data unless `--purge`, and asks first unless `--yes`.

| Flag | Description |
| --- | --- |
| `--purge` | Also delete the state and config directories: the store and its backups, MCP key, dossiers, logs, config and review profiles |
| `--dry-run` | Print what would be removed and change nothing |
| `--yes` | Remove without asking |

Examples:

```sh
{{command}} uninstall --dry-run
{{command}} uninstall
```

See [Uninstall](/guides/uninstall/).

### store migrate

Migrate the store to this release's schema.

```sh
{{command}} store migrate [--dry-run] [--yes]
```

Take the store to this release's schema version: copy it into &lt;state&gt;/backups, run each migration in its own transaction and check the result. A release's daemon does this on its own when it starts; run it when a build that is not a release asks for it, or to retry a migration that failed and see each step. It asks first unless `--yes`.

| Flag | Description |
| --- | --- |
| `--dry-run` | Print the migrations, the backup and the space it needs, and change nothing |
| `--yes` | Migrate without asking |

Examples:

```sh
{{command}} store migrate --dry-run
{{command}} store migrate
```

See [Upgrade](/guides/upgrade/).

### store restore

Put a backup of the store back.

```sh
{{command}} store restore [--list] [<backup>] [--yes]
```

Replace the store with a backup from &lt;state&gt;/backups, by default the newest this release reads, keeping the replaced store there as a pre-restore copy. What changed since the backup is lost. A backup older than this release is migrated after. It asks first unless `--yes`.

| Flag | Description |
| --- | --- |
| `--list` | List the backups with their schema versions and dates |
| `--yes` | Restore without asking |

Examples:

```sh
{{command}} store restore --list
{{command}} store restore
```

See [Upgrade](/guides/upgrade/).

### config get

Print a configuration value.

```sh
{{command}} config get <key>
```

Print a configuration value, defaults included. A dot names a nested key.

Examples:

```sh
{{command}} config get agent
{{command}} config get sweep.fast
```

See [Configuration](/reference/configuration/).

### version

Print the version.

```sh
{{command}} version
```

Print the version of {{name}}.

Examples:

```sh
{{command}} version
```

## Troubleshooting

### timings

Review, merge and turn durations, waits, tokens.

```sh
{{command}} timings [--since <days>] [--prs] [--turns] [--tags] [--json]
```

What {{name}} recorded: time to review, approve and merge, lead time, who pull requests waited on, turn and brief durations, waits, tokens and a change failure proxy.

| Flag | Description |
| --- | --- |
| `--since <days>` | The days to look back (default 30) |
| `--prs` | Also list each pull request with its milestones |
| `--turns` | Also list each turn with its duration |
| `--tags` | Also list each tag key's values and the dropped spellings |
| `--json` | Print the report as JSON |

Examples:

```sh
{{command}} timings
{{command}} timings --since 7 --prs
```

See [The daemon](/concepts/daemon/).

### intake

Follow, start or cancel an intake.

```sh
{{command}} intake <n>
{{command}} intake without <n>
{{command}} intake cancel <n>
```

Follow an intake until its round starts, start it without waiting for its brief, or cancel it. Ctrl-C stops following, not the intake.

Examples:

```sh
{{command}} intake 42
{{command}} intake without 42
```

See [The brief](/concepts/brief/).

### brief refresh

Make a pull request's brief again.

```sh
{{command}} brief refresh <url>
```

Make a pull request's brief again now. It needs brief.enabled.

Examples:

```sh
{{command}} brief refresh https://github.com/acme/api/pull/7
```

See [The brief](/concepts/brief/).

### turn show

A pull request's turns and their records.

```sh
{{command}} turn show <url> [--json] [--color auto|always|never]
```

Print the pull request's turns with their records and answers. Piped or with `--json`, it prints JSON with each one's instructions.

| Flag | Description |
| --- | --- |
| `--json` | Print the book as JSON, as off a terminal |
| `--color auto\|always\|never` | Lay the output out for a terminal; auto styles it on a terminal alone |

Examples:

```sh
{{command}} turn show https://github.com/acme/api/pull/7
```

See [Turns and rounds](/concepts/turns/).

### probe

Prove an agent home end to end.

```sh
{{command}} probe --workspace <workspace-id-or-path> [--home <dir>] [--model <name>]
```

The daemon sends a self-test turn into a new session of the Claude Desktop or Orca workspace and prints its turn as JSON, which proves the agent home, its hooks and its MCP registration work together.

| Flag | Description |
| --- | --- |
| `--workspace <workspace-id-or-path>` | The workspace the probe's session runs in: its Claude Desktop or Orca id or its worktree path |
| `--home <dir>` | The agent home the session runs with (default: the agent's own) |
| `--model <name>` | The agent's model (default: the agent's own) |

Examples:

```sh
{{command}} probe --workspace ws-123
```

See [Troubleshooting](/guides/troubleshooting/).

## For agents and scripts

### instructions

Run a turn by hand from an agent session.

```sh
{{command}} instructions <intent> [<url>] [<intent flags>] [-- <guidance>]
{{command}} instructions <intent> --turn <id>
```

Run a turn of an intent by hand from an agent session: the daemon prepares this worktree, starts the turn on this session and prints its instructions. `--turn` starts the turn {{name}} sent instead and prints its instructions.

| Flag | Description |
| --- | --- |
| `--turn <id>` | Start the turn {{name}} sent with this id |
| `--agent <name>` | The agent that reads them (default: the configured one) |

Examples:

```sh
{{command}} instructions review https://github.com/acme/api/pull/7 -- focus on the API
{{command}} instructions review --turn 66ea4cdcae20
```

See [Turns and rounds](/concepts/turns/).

### turn end/answer

Record how a turn ended; read your answer.

```sh
{{command}} turn end <url> --turn <id> --file <path>
{{command}} turn answer <url> [--message <id>]
```

end records how this session's turn ended, from a JSON file, when the session has no {{name}} MCP tool; the file is deleted once stored. answer prints the user's answers to this session's Inbox messages that it has not read, or, with `--message`, every answer to that message, and marks them read.

| Flag | Description |
| --- | --- |
| `--turn <id>` | The id on the first line of the turn's instructions |
| `--file <path>` | The JSON file that holds the record |

Examples:

```sh
{{command}} turn end https://github.com/acme/api/pull/7 --turn 66ea4cdcae20 --file /tmp/record.json
{{command}} turn answer https://github.com/acme/api/pull/7
```

See [Turns and rounds](/concepts/turns/).

### hook

The agent hooks install writes.

```sh
{{command}} hook session-start [--agent <name>]
{{command}} hook stop [--agent <name>]
{{command}} hook prompt [--agent <name>]
{{command}} hook mcp-headers
```

The agent's hooks, which install writes into each agent home; they read the payload on standard input and never block by failing. prompt records when you type, which closing a session reads. mcp-headers prints the session's handle as the header of {{name}}'s MCP server; the MCP registration runs it.

| Flag | Description |
| --- | --- |
| `--agent <name>` | The agent whose hook this is |

Examples:

```sh
{{command}} hook stop < payload.json
```

See [Turns and rounds](/concepts/turns/).

### serve

Run the daemon.

```sh
{{command}} serve
```

Run the daemon, headless: the store, the sweeps, the cockpit and {{name}}'s MCP server. The daemon's LaunchAgent runs it.

Examples:

```sh
{{command}} serve
```

See [The daemon](/concepts/daemon/).

### app

macOS: the menu bar and notifications.

```sh
{{command}} app
```

Run the menu bar and the notifications, a client of the daemon. {{name}}.app runs it.

Examples:

```sh
{{command}} app
```

See [Cockpit, terminal client and menu bar](/concepts/surfaces/).

This page is generated from `{{command}} help` by `make docs-ref`.
