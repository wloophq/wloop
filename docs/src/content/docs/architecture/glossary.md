---
title: Glossary
description: The words the code, the configuration, the facts and these pages use, each with one meaning.
---

{{name}} uses each of these words for one thing only. The configuration keys, the facts and the API name things the same way.

## Git

- **Repository**: a Git repository with a main checkout on your disk.
- **Worktree**: a checkout of a repository, the main one or a linked one. A worktree exists without any app knowing it.

## Units and sources

- **Unit**: a thing work happens on, named by its provider and the provider's key and written `<provider>:<key>`. A pull request is `github:acme/api#7` (shown as `acme/api#7`), a Slack thread is `slack:<team>/<channel>/<ts>`, a DM topic is `slack:<team>/<channel>/seg/<segment>`, an agent session is `session:<session-id>` and a workspace is `workspace:<worktree-path>`. See [Units and items](/concepts/tasks/#units-and-items).
- **Provider**: the source that owns a kind of unit and validates its keys: `github`, `slack`, `session`, `workspace` or `entry`. A workflow's and an intent's `provider` name one. The vendor of a coding agent is never called a provider.
- **Plugin**: a compiled-in source of units, `github` or `slack`, which brings an ingestion job, the cards of its units and, as it needs, items, actions, views, filter qualifiers and a publisher for drafts. `<id>.enabled` turns one off (`plugins.github.enabled`, `plugins.slack.enabled`), and `<id>.*` holds all of its settings. `session` and `workspace` are providers the core keeps for its own units and have no plugin. See [Plugins](/architecture/plugins/).
- **Item**: one thing someone needs from someone inside a unit: a `request`, a `question`, a `decision` or a `todo`. It is `open` or `resolved` and says who asked and who acts next. An item lives in its unit; a task is what you do about it. See [Units and items](/concepts/tasks/#units-and-items).
- **Confirmed item**: an item you created or corrected. Extraction never overwrites it; a later extraction that disagrees becomes a proposal in the Inbox.
- **Extraction**: the `extract:slack` job, which turns a Slack unit's new messages into item changes with one model call per unit. See [Extraction](/concepts/slack/#extraction).
- **Link**: a connection from one unit to another, found in the facts, by extraction or by you, with the message that names the target as its evidence. A `mention` link is only shown; a `review` link resolves its item when the target pull request gets a review, and a `merge` link when it merges.
- **Admitted message**: a Slack message the daemon stores because it mentions you, matches `plugins.slack.ingest.terms`, replies to you, sits in a DM or in a thread you follow. Every other message is dropped. See [What comes in](/concepts/slack/#what-comes-in).

## Agents and sessions

- **Agent**: the coding agent program that runs a turn, such as `claude` (Claude Code) or `codex`. Its vendor is an attribute of the agent. The word *provider* is kept for the sources of units (`github`, `slack`, `session`, `workspace`).
- **Agent home**: the agent's configuration directory, such as `~/.claude` or the one `agent_home` names (`CLAUDE_CONFIG_DIR` for Claude Code). `install` puts the hooks and the skills in each home.
- **Model tier**: `fast`, `balanced` or `frontier`, the class of model a headless call runs on. `agent_models` pins a tier to a model name per agent.
- **Session**: an agent's durable conversation in one worktree. {{name}} mints its id and starts the agent under it (`claude --session-id <id>`). A session is in one of three states:
  - **active**: it has a process;
  - **dormant**: it has no process, and its manager can wake it;
  - **closed**: its worktree is gone, its transcript is gone, you closed it in its manager (archiving it in Claude Desktop), or `session.close` closed it on purpose.
- **Process**: a live run of a session. A session has at most one process at a time; waking a dormant session starts a new one.
- **Handle**: the manager's runtime address of a process, such as an Orca terminal handle. A handle reaches a process; it never identifies a session, since the next process of the same session has another one.
- **Managed session**: a session the loop started for a turn (`session.managed`). A session you started yourself is manual, and cleanup only offers to close it.
- **Hooks**: the three commands `install` adds to each Claude Code home. SessionStart registers the session, Stop holds a stop that owes a record, and UserPromptSubmit notes when you type. See [The hooks](/concepts/turns/#the-hooks).

## Managers

- **Manager**: an app that hosts worktrees and the sessions in them. It declares the agents it hosts and its capabilities.
  - **Orca** hosts any agent, in terminals, and has a board.
  - **Claude Desktop** hosts `claude` alone and has no board.
- **Workspace**: a worktree in the custody of one manager, its **owner**. {{name}}'s store records the custody of each worktree and the units a worktree stands for, such as the pull request it reviews. When two managers list the same worktree, it belongs to the one that created it, else to the one custody names, else to the one with a live agent there, else to the first of `workspace_managers`.
- **Review workspace** and **author workspace**: the two roles of a pull request's workspace. A review workspace is named with `plugins.github.review_prefix` (`REVIEW <branch>`) and reviews someone else's pull request; any other name is an author workspace, for a pull request you develop. See [Workspaces and board columns](/concepts/board/).
- **Capability**: a feature only some managers offer: a board with columns, a card comment, the unread mark, reading a process's screen, closing a terminal, waking a dormant session, and the parts of a session's launch policy it can honor. {{name}} does without a capability where the owner lacks it.
- **Launch policy**: what a session asks of the manager that starts it: whether it loads the checkout's project settings, which agent home it uses, the allows and denies {{name}} adds, and whether it runs strict or without MCP servers. A new worktree goes to the first manager in `workspace_managers` that honors it. See [`workspace_managers`](/reference/configuration/).
- **Census**: the list of live processes a manager reports each time the daemon reads it. An active session whose process two complete censuses miss turns dormant; a census never closes a session.
- **Control surface**: what drives a manager from outside: the `orca` binary, `open claude://*`, Claude Desktop's session registry and its peer sockets. Every session the loop starts is denied the control surface of every enabled manager. See [The same-user boundary](/concepts/security/#the-same-user-boundary).

## Turns

- **Turn**: one intent on one unit, from the moment {{name}} sends it until a record closes it. See [Turns and rounds](/concepts/turns/).
- **Round**: a turn on a pull request.
- **Role**: what binds an intent to a plugin's mechanism, and what {{name}} finds it by when it starts one on its own: `pr.review`, `pr.address`, `pr.nudge`, `probe` or `headless`, from the manifest's `role`. Go derives what a role implies (its round, the builtins its turns run, the policy of its sessions, whether it drives its workspace's row) from `builtin.Roles`. A turn keeps the role its intent held. See [The intent manifest](/reference/intent-manifest/).
- **Runner**: how a turn runs, `session` (in a session of a workspace) or `headless` (one read-only call). An intent's `runners` lists the ones it allows, the first being its default; `turn.start`'s `runner` picks one. Which manager hosts a session is configuration, never the runner.
- **Effect**: what sending a turn or a draft applies, once, by name: an intent's `effects` when its turn is sent (`pr.reviewed`, `turn.round_sent`, `plan.cleared`, ...), a draft's when it is sent (`task.nudged`, `unit.closed`, `pr.nudged`). An effect a plugin owns carries its slot's prefix. An agent's draft may declare only the effects its intent lists in `[drafts] allowed_effects`.
- **Turn state**: `sent`, `started`, `asked`, `done`, `settled`, `failed`, `unrecorded`, `dismissed` or `replaced`. A turn is open while it is `sent` or `started`. See [States](/concepts/turns/#states).
- **First round** and **follow-up round**: the two review rounds. The first reviews a pull request you have not reviewed; a follow-up comes after the author pushed, replied or re-requested you.
- **Stage**: which pass over a unit a turn is, `first` or `follow-up`: `turn.start`'s `stage`, the turn fact `turn.stage`, and the dimension of a block path such as `stage=first/intent=review.md`.
- **Record**: how a turn ends: its state, a one-line summary, the outcome and, for a question, the question with its options, a recommendation, the draft and notes. The agent sends it with the `turn_end` tool. See [The record](/concepts/turns/#the-record).
- **Book**: one unit's turns in order, each with its sequence number. A book has at most one open turn, and a turn's "Earlier turns" come from it.
- **Instructions**: the stored text a turn's agent reads with `{{command}} instructions <intent> --turn <id>`: the procedure, the judgment, the brief, the guidance and the earlier turns. They are rendered once, when the turn is created. See [Instructions](/concepts/turns/#instructions).
- **Guidance**: your free text for one round, carried verbatim in its instructions. Guidance for a round that does not go out is kept for the next one.
- **Question** and **answer**: a turn that records `asked` stops on a question, an Inbox message; your answer names that message, and the session gets a notice it reads the answer from with `inbox_read`, or a resume turn carries it. See [Answers](/concepts/turns/#answers).
- **Resume turn**: a new turn of the same intent, in a new session, that carries the stopped turn's question, draft, notes and your answer. **Continue** sends one without an answer.
- **Endorsement**: whether you stood behind the verdict a turn published, by publishing it by hand or approving it. An automatic follow-up approves only after an endorsed verdict.
- **Headless turn**: a turn run as one read-only call of the agent, with no session and no workspace, that proposes up to 3 drafts. See [Headless turns](/concepts/turns/#headless-turns).
- **Intake**: the numbered wait of a first turn for its brief. `{{command}} intake <n>` follows it, and `{{command}} intake without <n>` starts it without the brief.
- **Dossier**: the working memory of a task, or of a unit no task holds, a local git repository under `<state>/dossiers/` that the daemon commits after every turn. See [Dossiers](/concepts/turns/#dossiers).

## Profiles and intents

- **Intent**: a kind of turn: `review`, `address`, `nudge`, `act`, the `probe`, or one your profile adds. Each is an `intent.toml` manifest plus an instructions template, and becomes a command, a skill and an action on rows. See [Intents](/concepts/profiles/#intents).
- **Procedure**: the mechanics of a turn: reading the unit, the ambiguity gate, publishing, the record. It lives in the intent's instructions; the bundled intents' are defaults your profile overrides file by file. The shared partials, such as the record protocol, are the profile's too (`partials/`). The binary keeps facts, mechanisms and gates (see [Profile first](/architecture/profile-first/)).
- **Judgment**: the part of a turn's instructions that profile blocks add: what a review looks for, what blocks, the voice of a reply. It comes from your review profile, on top of the bundled guides.
- **Review profile**: one directory of blocks that holds your judgment and your own intents and workflows. The configuration's `profile` names it. See [The review profile](/concepts/profiles/#the-review-profile).
- **Block**: a Markdown file of the review profile whose path says when it applies, such as `organization=acme/repo=api/intent=address.md`. Every matching block goes into the turn, later blocks winning. See [Profile blocks](/reference/profile-blocks/).
- **Bundled guide**: {{name}}'s own `intent=review.md`, `intent=address.md` and `intent=nudge.md`, the layer below your blocks.
- **Brief**: a short preview of a pull request that the agent writes: goal, kind, size, risk, open questions and tags. Rows, filters and profile blocks read it. See [The brief](/concepts/brief/).
- **Tag**: a `key=value` pair of the brief under one of nine keys, such as `lang=go` or `concern=security`. A block under `tags/` applies only where the brief has the tag.
- **Summary**: one line saying what an intent, a workflow or a built-in action does, required on each. It is the action's tooltip, the command's help line and the plan item's description.
- **Shortcut**: the one uppercase letter of an intent's action in the cockpit, the terminal client and the menu bar, its manifest's `shortcut`; it may not be a built-in action's.
- **Skill**: an instruction file `install` puts in each agent home, such as `/{{name}}-review <url>`, that runs an intent or drives the CLI from a session. See [The skills](/concepts/profiles/#the-skills).

## Automation

- **Workflow**: a manifest, `workflow.toml`, that names a provider, a condition over its units' facts and the steps to run. Everything {{name}} does without being asked is a workflow. See [The sweep and workflows](/concepts/workflows/).
- **Workflow provider**: the provider whose units a workflow looks at, its `provider`: `github` (pull requests), `session`, `workspace`, `entry` (the tasks neither done nor in the trash) or `slack` (the Slack units a live task is attached to).
- **Fact**: a named, typed value a provider computes for a unit from the store, such as `pr.first.due` or `session.idle_for`. Predicates read facts and never GitHub. See [Facts and steps](/reference/facts/).
- **Slot**: the set of facts a predicate may read: an intent's action slot (the row) or turn slot (the turn being built), and a workflow's provider slot plus `trigger.*`, `config.*` and `now`.
- **Predicate**: a CEL expression over the facts of a slot that yields a bool, such as a workflow's `when`. See [Predicates](/reference/intent-manifest/#predicates).
- **Trigger**: what makes a workflow evaluate: every sweep (no `[trigger]`), an event kind (`on`), or a cron schedule (`schedule`).
- **Step**: one action of a closed vocabulary that a workflow runs on a matching unit, such as `turn.start` or `workspace.remove`. See [Workflow steps](/reference/facts/#workflow-steps).
- **Guard**: a check that refuses a step until it passes, such as a workspace that holds unpushed commits. A refused step stays in the plan as `blocked`. See [Cleanup guards](/concepts/workflows/#cleanup-guards).
- **Sweep**: the evaluation of every workflow, right after each fast job. See [Jobs](/concepts/daemon/#jobs).
- **Job**: a recurring task of the daemon: `fast` (every `sweep.fast`), `slow` (every `sweep.slow`), `brief`, and the plugins' ingestion and extraction jobs.
- **Mode**: the outcome of an approval table, and nothing else: a run approval's `mode` is `run`, `delay` or `wait`, a draft approval's `send`, `delay`, `wait` or `dry_run`.
- **Observe**: a workflow with `observe = true` plans its items pending and never runs them by itself; you apply one by hand with `{{command}} plan apply`. `workflows.<name>.observe` switches it.
- **Dry run**: the draft approval mode `dry_run`: the draft is stored, discarded, as what would have been sent, and nobody can release it.
- **Plan**: what the next sweep would do, as plan items. `{{command}} plan` prints it without writing anything. See [The plan](/concepts/workflows/#the-plan).
- **Plan item**: one workflow matched on one unit, with a decision: `pending`, `blocked`, `run` or `skipped`.
- **Key**: the facts that identify one run of a workflow, by default the unit's `fingerprint`. A key that ran is not run again, and skipping or dismissing an item is final for its key.
- **Mark**: what a unit's trigger already handled, such as the head whose conflict an address round covered, kept per unit and name until the unit ends. A string mark is a version and a time or an int a watermark; a workflow records its own with `[marks]` and reads any with `handled()`. See [the workflow manifest](/reference/workflow-manifest/).
- **Approval**: a `[gates.approval]` table, which decides how the gates a manifest opens are resolved: `mode = "run"` or `"send"` releases a gate at once, `"delay"` when its `window` ends, `"wait"` keeps it until you resolve it, `"dry_run"` stores a draft unsent; `[[gates.approval.rule]]` entries tried first pick another mode where their `when` holds, each for the kinds of gate it pins `gate.kind` to. What no table decides, `gates.toml`'s `[approval]` does. See [What waits for you](/concepts/approval/#gates).
- **Approval window**: the countdown during which a pending automatic round can be skipped, paused, steered or run now. See [The approval window](/concepts/approval/#the-approval-window).
- **Ambiguity gate**: the rule inside a round's procedure that records `asked` with the full draft before publishing anything unsure, risky or contradicting another reviewer. See [The ambiguity gate](/concepts/approval/#the-ambiguity-gate).
- **Pause**: `{{command}} pause` stops every workflow but the observe ones, and the board moves, everywhere or on one repository or pull request. A paused pull request has its rounds **held**.
- **Cap**: a bound on automatic turns: `turns.per_unit_per_day` on a unit whose plugin keeps a per-unit count (a pull request), `turns.per_day` on the others, spent by every automated turn and by no turn by hand, or a workflow's `[limits]`. A round the cap holds back shows as "held (cap)".
- **Mute**: takes a pull request out of the pending views, for a while or until whom it waits on changes. Its rounds still run at their deadline.

## Pull requests in scope

- **Scope**: the people, teams, repositories and watched logins whose pull requests the daemon follows. See [Scope](/concepts/daemon/#scope).
- **Inventory**: the set of pull requests the daemon follows: yours, those that request your review, those in scope, those a workspace stands for and those you track.
- **Track**: `{{command}} track <url>` adds a pull request out of scope to the inventory, until it merges or closes.
- **Situation**: what a pull request waits on and why, which the daemon derives from its history and which gives the row its kind.
- **Waits on**: whom a pull request waits on: the author, the requested reviewers, you, the loop or nobody, and since when. Past the `stalled` thresholds it reads as **stalled**. See [Who a pull request waits on](/concepts/daemon/#who-a-pull-request-waits-on).
- **Take over**: making someone else's pull request yours to develop, with an author workspace.

## Tasks and views

- **Task**: your record of something to act on, wait for, read later or decide about. It points at the items and units it comes from, and has one state at a time. See [Tasks and views](/concepts/tasks/).
- **List**: a task's state: `inbox`, `next`, `waiting`, `scheduled`, `someday`, `reference`, `done` or `trash`. Each view shows one or more states. See [Lists are states](/concepts/tasks/#lists-are-states).
- **Inbox**: the tasks not clarified yet, and the messages that need you to decide.
- **Message**: something in the Inbox that needs a decision: an agent's question, a post an agent makes with `inbox_post`, or a proposal to change a confirmed item. See [Messages](/concepts/tasks/#messages).
- **Outbox**: the drafts that wait for their approval policy before they go out. See [The Outbox](/concepts/approval/#the-outbox).
- **Draft**: an outbound post an agent or a proposal rule proposes, such as a reply, a nudge or a Slack reaction. Besides its kind it carries a purpose (what it is for, such as `nudge` or `close`), a delivery (`message`, `reaction` or `comment`) and the effects sending it applies once (`task.nudged`, `unit.closed`, `pr.nudged`). Its draft gate holds the decision; the draft keeps its delivery, from `held` to `queued` once its gate approves it, `sending` and `sent`, or ends `dropped` or `failed`.
- **Draft approval policy**: when a draft may go out: `wait` (when you approve it), `delay` (when its window ends), `send` (at once) or `dry_run` (never; stored as what would have been sent), given by the first `[gates.approval]` rule of a draft gate whose `when` matches the draft, else by its `mode`, else by `gates.toml`'s default. Every bundled intent, workflow and rule uses `wait`. See [The Outbox](/concepts/approval/#the-outbox).
- **Proposal rule**: a plugin's own rule that adds drafts on its own, hourly and on changes: GitHub's `github-nudge`. The Slack nudges, closures and owner questions come from the default workflows `slack-stalled-nudge`, `slack-closure` and `slack-ask-owner`.
- **Focus**: the star on a task, and the view that lists starred tasks. A stale waiting task and a task past its deadline get it on their own.
- **Context**: a label on a task such as `@decide`, from `tasks.contexts`.
- **Area**: a part of your life or work, from `tasks.areas`. Switching area filters every view but the Inbox.
- **Snooze**: scheduling a task until a date, until activity on one of its units, or both.
- **Changed**: the mark on a task that new activity by someone else brought back to the Inbox. Opening or moving the task clears it.
- **Project**: an outcome that takes more than one action, parallel or sequential, with a review interval and a dossier. See [Projects](/concepts/tasks/#projects).
- **The review**: the periodic walk through six filtered views, from Inbox to zero to pruning Focus. A review round is a turn of the `review` intent. See [The review](/concepts/tasks/#the-review).
- **Logbook**: the view of `reference` and `done` tasks.

## Surfaces

- **Daemon**: `{{command}} serve`, the one process that owns the SQLite store, runs the jobs and serves every client. See [The daemon](/concepts/daemon/).
- **Store**: {{name}}'s SQLite database, the truth for turns, questions, answers, custody and history. Managers and boards only show it.
- **MCP server**: the daemon's endpoint on `127.0.0.1:47800` (`mcp.port`) that agent sessions call to record turns and read or draft Slack.
- **Cockpit**: the web page the daemon serves on `127.0.0.1` behind a token, opened with `{{command}} open`. See [The cockpit](/concepts/surfaces/#the-cockpit).
- **Terminal client**: `{{command}} tui`, the same views and actions in a terminal. See [The terminal client](/concepts/surfaces/#the-terminal-client).
- **Menu bar**: `{{command}} app` on macOS, which counts what waits on you and offers the actions of the "In loop" rows. See [The menu bar](/concepts/surfaces/#the-menu-bar-macos).
- **View**: a filtered list a surface shows, such as Needs me, Stalled, Inbox or Waiting. Every client shows the views the daemon computes.
- **Row**: one unit in a view, with the actions the daemon computes for it. A client renders them and decides nothing.
- **Action catalog**: the actions the daemon offers on a row: the built-in ones, such as Run now, Skip and Take over, and each intent's `[action]`. Each says what it does in its `summary`; the built-in ones' words are the profile's `actions.toml`. See [the intent manifest](/reference/intent-manifest/).
- **Card**: what a plugin shows of one unit, in a row or a task's detail. In Orca it is also the unit's workspace on the board.
- **Board column**: the column of an Orca card, which {{name}} writes from the turn's state and never reads back. `views.orca.columns` maps each state to a column of your board. See [Workspaces and board columns](/concepts/board/).
- **Sink**: a destination that receives the projection of every unit's view, such as Orca's board. A sink fails alone and is reconciled on start.
- **Placement**: the rule that says where a unit's work runs: the repository, the checkout, the role and whether the workspace is shared. `placement` in the configuration lists the rules. A card's placement is the column you see it in. See [the configuration](/reference/configuration/).
- **Notification**: a macOS alert for a pending round, a held round, a question or a blocked first round, with its actions. The rest goes in the daily digest at `notifications.digest_at`.
