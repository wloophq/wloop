---
name: {{name}}
description: Use when the user wants a GitHub pull request reviewed, followed or monitored by the review loop, wants to take over someone else's PR to finish it, or asks which reviews or PRs need them, what the loop is doing, or why a review did not continue, or wants new work started from a description or a diagnosis. Drives the {{name}} CLI, which runs each review in its own workspace with its own agent. Do NOT review the PR yourself in the current session.
---

# Operating the review loop

{{name}} runs reviews in dedicated workspaces (`REVIEW <branch>` in Orca),
each with its own agent, and its daemon follows them and sends the next
rounds. This session only drives the CLI: never review the PR here.

## Requests and commands

| The user asks to | Run |
|---|---|
| set {{name}} up, or change its projects directory, agent, scope, profile or Slack login | tell them to run `{{command}} init` in their own terminal (it asks, checks the prerequisites, installs and ends with `doctor`); here, only with every answer as a flag (`{{command}} init --help` lists them), and `--yes` only when the user asked |
| review a PR, follow it, start monitoring it | `{{command}} review <url>` |
| the same, with a focus or a question for the reviewer | `{{command}} review <url> <guidance>` (free text) |
| another round on a PR already under review | `{{command}} review <url> [guidance]` (it continues in the same workspace) |
| take over a PR someone else opened, to finish it | `{{command}} take-over <url>` |
| address, triage or answer the reviews of a PR they develop | `{{command}} address <url> [--take-over] [guidance]` (runs in that PR's workspace; the daemon also does it on its own when a review arrives) |
| review a PR they develop first (adversarially, or against a plan), then address it | `{{command}} address <url> --self-review [plan]` (nothing of that review is published) |
| know what needs them, or the state of the loop | `{{command}} status` |
| see a PR's turns: what each agent recorded, asked and was answered | `{{command}} turn show <url>` |
| know what the next sweep would do | `{{command}} plan` (grouped by decision, with each item's reason; `plan apply <id|url>` and `plan dismiss <id|url>` act on an item, or on a PR's one pending item, only when the user asks) |
| know why nothing happens to a PR, a session or a workspace | `{{command}} plan --unit <url>` (its items, then every workflow that gives it none, with the facts its `when` read and the reasons beside them, such as `pr.first.reason`) |
| run a sweep now | tell them to choose Sweep now in the menu bar; `{{command}} plan` prints what it would do |
| stop or restart the automatic workflows, everywhere or on some PRs or repositories | `{{command}} pause [<url>\|<owner/repo>]...`, `{{command}} resume [<url>\|<owner/repo>]...` (none means everywhere; only when the user asks) |
| start new work from a description or a diagnosis: code to change, or a message to post | map it into a task: a title (one short line naming the work), a brief in a markdown file (what you found, what to do, how to know it is done) and the properties the user gave, such as a priority or a due date; resolve the repository with `gh` (ask which when more than one matches); run `{{command}} task new --title <title> --brief-file <path> [--prop <name>=<value>]... [--repo <owner/name>]`, which prints `task:<id>`, then `{{command}} start task:<id>`, which works in the repository `--repo` named. Leave out `--repo` when no code is involved |
| make this session a run of a task, so the task lists it and what it posts lands on the task | `{{command}} task attach task:<id>` (in a session a workspace manager started) |
| ask whoever a PR waits on for the next step (a reviewer, the author) | `{{command}} nudge <url> [guidance]` (an agent posts one comment under the user's name; only when the user asks, and it refuses when the PR waits on the user or the loop) |
| run a kind of turn their profile defines | `{{command}} <intent> <url> [flags] [guidance]`; `{{command}} --help` lists the intents and their flags |
| watch what needs them in the terminal, with one-key actions | tell them to run `{{command}} tui` in their own terminal (it needs one; do not run it here) |
| know a configuration value the loop uses | `{{command}} config get <key>` (defaults included; `sweep.fast` for nested keys) |
| find out why something did not happen | `{{command}} doctor`, then the logs below |
| change how reviews are done (what to look at, labels, voice), everywhere or in one project | follow the `{{name}}-profile` skill, which places the rule with `{{command}} profile add`; `{{command}} profile show <owner/repo>` lists the blocks a round there reads; it applies from the next round |
| see what a review would publish, without publishing | `{{command}} profile try <url>` |

Several PRs: run one `{{command}} review` per URL, one after the other. Pass
the user's guidance in their own words; do not add your own. The review agent
runs in the agent home of this session (for Claude Code, `CLAUDE_CONFIG_DIR`)
unless the config sets `agent_home` for the project.

## Turns and answers

Every round is a turn that {{name}}'s daemon keeps. The agent that runs it ends
every turn with a record, through the `turn_end` tool of the {{name}} MCP server
(or `{{command}} turn end` in a session without it): done, settled (nothing new
to publish), asked (a question for the user) or failed. A session that stops
without one is marked unrecorded.

Every decision waiting on the user is a gate on its task: an agent's question,
a draft to approve, a pending round. The user resolves it from the cockpit
(`{{command}} open`), the notification or `{{command}} tui` (`w` answers, `c`
continues without an answer, `d` dismisses). {{name}} types an answer into the
agent's session as its next prompt (a long one tells the agent to run
`{{command}} turn answer <url>`), or starts a new session that resumes the turn
when the old one is gone; an agent reads the answers to what it posted, and
what you wrote on its task's chat, with `task_read`. Do not answer for the user and do not type into that agent's
terminal.

## Reading `{{command}} status`

Piped, as you run it, it prints one tab-separated line per pull request and
view, full values: `view, who it waits on, age, owner/repo#n, title, reason`
(`tracked` lines too). No line outside `tracked` means no pull request waits.

| View | Meaning |
|---|---|
| `needs-me` | the user must act: an agent asked or stopped, a round is pending or held, a review or their own PR waits on them |
| `stalled` | waiting on someone past the configured working days |
| `mine` | the user's own PRs, those they took over included |
| `loop` | PRs with a review workspace, which the loop follows |

Lead with `needs-me`, then `stalled`. Link every PR as a markdown link.

## Diagnosing

- `{{command}} doctor` lists anything missing with the command that fixes it,
  the PRs a daily cap held back today, and whether automatic rounds are
  paused (`{{command}} resume` restarts them, when the user asks).
- Git access without prompts comes from the daemon's latest fetch per host;
  `{{command}} doctor --git` checks it live with an `ls-remote`.
- Every command but `doctor`'s local checks, `install`, `uninstall` and
  `config get` needs the daemon; when it is down, the error names the
  `launchctl kickstart` command that starts it.
- Daemon logs: `serve.out.log` and `serve.err.log` in the state directory
  (`$XDG_STATE_HOME/{{statedir}}`, by default `~/.local/state/{{statedir}}`). A
  `held:` line means a cap stopped a round.
- `{{command}} plan` prints every workflow's decision with its reason,
  `{{command}} plan --unit <url>` why nothing happens to a given PR, and
  `{{command}} doctor` each workflow's mode, last run and refusals.

The full state machine is on the board columns page of {{name}}'s documentation.
