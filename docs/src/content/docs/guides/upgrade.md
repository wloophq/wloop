---
title: Upgrade
description: Upgrade the binary and refresh what it installed.
---

```sh
brew upgrade {{command}}
{{command}} install            # the hooks, permissions, MCP registration and skills of every agent home
{{command}} app install        # macOS: rebuilds {{app}}.app and reloads both LaunchAgents
{{command}} doctor
```

Run them in this order. `install` rewrites what each agent home needs from the new release; `app install` rebuilds the app bundle from the new binary and restarts the daemon and the menu bar on it. On a Mac without the menu bar, use `{{command}} app install --no-menu-bar`. On Linux, restart your `{{command}} serve` service.

An open cockpit tab notices the restarted daemon serves another build and reloads itself. While it holds text you have not sent (a reply, a guidance, an answer, a draft being edited) or an open dialog, it shows "A new version is running." with a **Reload** button instead.

## The store

A release that changes the store's schema migrates it when its daemon starts, which happens on its own about 20 seconds after `brew upgrade` replaces the binary. First it copies the store to `<state>/backups/store-v<version>-<time>.db`, and the plugins' stores beside it; then it runs each migration in its own transaction and checks the result. `serve.err.log` lists each step, and `{{command}} doctor` shows the schema version and the newest backup. The three newest pre-migration backups stay.

A migration that fails leaves the store at the last version it reached, usually the one it started from, and the daemon serves only its health until you act: the menu bar shows `PR !` with the reason and the command, the cockpit and `doctor` show the same message. Run the command in a terminal to retry with each step shown:

```sh
{{command}} store migrate --dry-run   # the migrations, the backup and the space it needs
{{command}} store migrate
```

A build that is not a release, such as `make dev-install` from a checkout, never migrates on its own; `make dev-install` runs `store migrate`, which asks first.

`doctor` reports a daemon older than the CLI, and a client and a daemon of different API major versions refuse each other and name the one to restart. To restart the daemon by hand on macOS:

```sh
launchctl kickstart -k gui/$(id -u)/io.github.{{org}}.{{name}}
```

Schema version 4 keys briefs and tags by unit; the Slack conversations keep the summary they had as the goal of their brief. A workflow's `classify` schema must use the keywords the brief's extra fields use (see [the brief](/concepts/brief/#fields-of-your-own)); one with another keyword fails the workflow's load, naming the step and the keyword. Releases before it refuse a store at version 4.

Schema version 6 links each answer to the Inbox message it answers and records when the agent read it. An answer given before it counts as read unless it was still waiting for its session to stop; one no message matches keeps reading through its turn. Releases at version 4 or 5 keep working on a version 6 store.

Schema version 7 records each decision waiting on you as a gate, open or decided: every draft, with a delayed draft's send time; a round waiting for approval, with the window after which it runs when it has one; a round held until you trust its repository; every question, agent post and item proposal. A question, post or proposal no longer has a state of its own, nor a task of kind message: its gate holds the decision. A message task or a close task from before the upgrade gives its gates, turns, events and marks to the task that holds its thread or pull request, an open one first, and goes; when no open task holds it while it is still open, it stays as that unit's task, and a message about no unit stays as a capture named after it. A task that waited on someone for several items becomes one task per item: the oldest item's keeps the task's id, and each task keeps its star, snooze, last nudge (`last_nudge`) and check date (`check_on`); a wait you typed by hand names whom it waits on as its `owner`. An item that waited on several people stays with one of their tasks, and the others give it their gates and go. An id that went keeps leading to the task that took it over, so a link or a notification naming it still opens that task; a split id stays its oldest item's task. An open draft or decision whose task had ended moves to an open task of its pull request or thread, or to a new one in the Inbox. The `answer`, `continue` and `dismiss` tables of `actions.toml` are the intents of `[gates.question]` in `gates.toml` now, and a user `actions.toml` that sets them fails to load naming them. A turn that fails or ends without its record opens the missing gate its intent names, `missing-record` in the bundled intents, in place of a question. A draft keeps only its delivery: a draft that waited for you is `held`, one approved and not yet sent `queued`, one discarded or withdrawn `dropped`, and its approval mode and what auto-send would have done are its gate's. An answer names the gate of the question it answers, so a question open before the upgrade still reaches its session. It keeps each task's category beside its state, from the bundled states (`next` is `ready`, `scheduled` and `someday` are `deferred`, `reference` and `done` are `done`, `trash` is `dropped`). A task's area, focus star, deadline and contexts become its properties `area`, `focus`, `due` and `contexts`, each with who wrote it: the person, a workflow for a star the loop set, the plugin for the deadline and contexts a source derived. Each task gets the property `kind` from where it came from (`pull-request` or `thread` for a task on a pull request or a thread, `start` or `capture` for a capture, `project`). An item's proposed `initial` reads `ready` where it read `next`, and every stored time the loop compares as text has nine fraction digits. A project's actions name it as their parent, and its review interval moves onto the project's task. Every pull request the loop followed becomes one task: the tasks whose first unit it was, or that held one of its items, merge into the oldest open one, whose id the others lead to; an open one the loop follows (its scope reasons, a review workspace, tracked or adopted) with no task gets one, waiting, and a merged or closed one, or one nobody follows, gets none; the pull request is the task's first unit, and its task in the Inbox moves to Next. The last time a search saw a pull request stays the GitHub plugin's own; a merged task shows changed when one of the tasks it took in did. Every dossier becomes its task's: a project's moves from `dossiers/project/<slug>#` to `dossiers/task/<id>#`, and a pull request's or a thread's to the task that holds it. A dossier that meets one its task already has goes inside it under `merged/`. The daemon moves the directories at its next start. The dossier of a task that ended before the upgrade is resolved at the task's last update, with no closing call.

Version 7 also makes each snooze a wake on its task: until its date, on activity, or both, returning the task where it was. A pull request's mute becomes a wake on the open tasks that hold it: one for a while keeps its end, and one until the state changed waits on activity on the pull request. A mute of a pull request no open task holds is dropped. The facts `pr.muted` and `pr.mute.refusal` are gone; a workflow reads the task's wake with `task.wake_until` and `task.wake_when`, and a nudge on a muted pull request is refused through `pr.nudge.refusal`.

## Your configuration and profile

A key or a value the new release does not know, in `config.toml` or in a manifest of your profile, fails at load with the key's path, and a close name when one exists; no old spelling is read, and a key that moved gets the same error as a typo. `{{command}} doctor` and `{{command}} profile check` list each one. The configuration is `config.toml`; a configuration directory that holds only the former `config.json` fails at load, naming `config.toml` and the page with the script that converts one. The [profile language](/reference/language/) lists every reserved word, and the [configuration](/reference/configuration/) every key.

`slack_react` and `slack_edit` take the message as `message`, a ref (`<conversation>/<ts>`) or a permalink, where they took `permalink`: an agent's call with `permalink` is refused as a bad argument. Their answer for a missing or unreadable message is `problem_message`, where it was `problem_permalink`, so a profile whose `tools.toml` words `problem_permalink` fails at load until it is renamed.

The words for tasks have new names in the profile and the configuration. Each old name fails at load with a problem naming the new one:

- `gtd.toml` is not read: its `[verbs]` go in `tasks.toml`, its `[views]` and `[review]` in `views.toml`;
- the `[gtd]` table of `config.toml` is `[tasks]` (`tasks.stale_waiting_days`, `tasks.areas`, `tasks.contexts`);
- the facts `entry.*` are `task.*`, but `entry.units_ended`, `entry.unit_started_by_me` and `entry.unit_closure_posted`, which are `task.link_ended`, `task.link_started_by_me` and `task.link_closure_posted`; `slack.live_entry` is `slack.live_task` and `config.gtd.*` is `config.tasks.*`;
- the step `entry.verb` is `task.verb`, and the effect `entry.nudged` is `task.nudged`;
- the workflow `entry-results-ended` is `task-results-ended`: rename an override directory of the old name in your profile, and a `[workflows.entry-results-ended]` block of `config.toml`;
- `profile schema --gtd` is `--tasks`, `--views` or `--gates`.

The lists are the views of `views.toml`, each with its own condition (`when`). Next is gone: your next actions are in Needs me, beside the open gates of your tasks, and a sequential project's next action comes first there. A `[views.next]` of your `views.toml` declares a view of your own, which lists nothing until you give it a `when`. The review's projects step walks the view `review-projects`: a `[review.projects]` fails to load, naming it. The badge counts Inbox and Needs me, where it counted Inbox and the Outbox. The bundled `tasks.toml` gives the properties `area`, `contexts`, `focus` and `due` the roles `area`, `contexts`, `star` and `deadline`, and its `dismiss`, `done` and `reference` verbs the handlers `drop` and `finish`.

Each plugin's settings live in its `[plugins.<id>]` table, and each old key fails at load naming its new one:

- `[github]` is `[plugins.github]` and `[slack]` is `[plugins.slack]`, key for key;
- `github.drafts.approval` is `plugins.github.gates.approval`, and `slack.drafts.approval` is `plugins.slack.gates.approval`;
- the facts `config.github.*` are `config.plugins.github.*`;
- the effects `guidance.handed` and `pr.guidance_handed` are `turn.guidance_handed`, and `pr.round_sent` is `turn.round_sent`;
- the events of a pull request and its rounds (`opened`, `merged`, `round_sent`, `review_ended` and the others) are the GitHub plugin's, so a workflow whose trigger names one needs `provider = "github"`, and an intent reads `pr.adopted`, `pr.held`, `pr.nudge.*` and `pr.last_review.*` only on GitHub units: a manifest of another provider that names one, which never fired or read an empty value, fails at load naming the provider.

Every approval is a `[gates.approval]` table, and each old one fails at load naming it:

- a manifest's `[drafts.approval]` and a workflow's `[approval]` are `[gates.approval]`; in `config.toml`, `workflows.<name>.approval` and `workflows.<name>.drafts.approval` are `workflows.<name>.gates.approval`;
- one table decides every gate its manifest opens: a mode set in it applies to the workflow's runs and its drafts alike, so to set one kind only, write a rule whose `when` pins it, such as `gate.kind == "plan"` for a workflow's runs, as the bundled review and address workflows do for their `60s` window; a rule that reads a draft's facts applies to drafts alone;
- what no table decides, `gates.toml`'s `[approval]` does: a run starts at once, every other decision waits for you;
- a `wait` rule that reads an untrusted fact, such as `workspace.pendency`, must pin `gate.kind` to the kinds it holds, such as `gate.kind == "plan"`: one that would reach draft gates fails to load, since no rule of a draft gate reads such a fact and leaving it out would let a later rule decide the draft;
- a draft whose delivery posts, as its plugin's manifest marks it, waits for you unless a rule that names its delivery releases it: a Slack message now needs `draft.delivery == "message"` in a rule that sends it, as a GitHub comment needed `draft.delivery == "comment"`.

A draft is decided through its gate. The routes `GET /api/drafts/{id}` and `POST /api/drafts/{id}/{action}` are gone: `GET /api/drafts?id=<id>` reads a draft, with its gate, `POST /api/gates/{id}/{intent}` approves or discards it, and a draft's task verbs edit and retry it. A task's `draft` field is gone too: a draft task carries its draft as the subject of its draft gate. The actions `draft.approve` and `draft.discard` left `actions.toml`, as the draft gate's intents in `gates.toml` word them; a user `actions.toml` that words them fails at load. `outbox discard --reason` records its reason only on a draft its gate approved.

A pending round, and any workflow item that waits for you or for its window, waits on a `plan` gate; a round its start block holds waits on a `trust` gate. The actions `run_now`, `skip`, `guide`, `trust_run` and `unblock` left `actions.toml`: they are the intents of those gates in `gates.toml`, whose `note` and `confirm` word them on a row, and a user `actions.toml` that words them fails at load. A window ends at its gate's deadline, which the gates' tick resolves, so the daemon's `due` job is gone. A step that runs asynchronously leaves its item `deferred` rather than `pending`.

## Going back to an earlier release

A release can declare that earlier releases keep reading the store after its migration, when it only adds what they ignore; such a downgrade needs nothing more. Otherwise the earlier release's daemon refuses the newer store and serves only its health, naming `{{command}} store restore`, which puts back the newest backup it can read, with the plugins' stores taken with it, and keeps the replaced store in `<state>/backups` as a pre-restore copy. What changed since that backup is lost: sessions, notes, ledger entries and drafts; pull requests come back from GitHub on the next sync. Restoring a backup below version 7 moves the tasks' dossiers back to `dossiers/entry/`, but not the dossiers version 7 gave to a task (a project's, a pull request's or a thread's): they stay under `dossiers/task/`, where the earlier release does not look; copy them back from a backup of the state directory.

```sh
{{command}} store restore --list   # the backups, their schema versions and dates
{{command}} store restore          # the newest one this release reads
```

Release v0.1.0 has no `store restore` and refuses a store at any version but 1; to go back to it, run `{{command}} store restore` with the newer binary first, choosing a backup at the earlier release's version.

Run `{{command}} uninstall` first, while the newer binary is still installed: it takes the LaunchAgents, the app and the MCP registration, the hooks and the permissions of every agent home out, which an earlier release would otherwise leave calling a command it may not have; the config and the data stay. Then install the earlier release and run `install` and `app install` again.

## Rotating the MCP key

The daemon's MCP endpoint requires a per-install secret, `<state>/mcp.key`, which `install` writes into every home's registration as the `X-Loop-Key` header. To rotate it, delete the file and run `{{command}} install` again. Sessions started before keep the old header until they restart.

The agent tools `inbox_post` and `inbox_read` are `task_post` and `task_read`: a post names the gate it waits on, and `task_read` also reads the task, its brief, its open gates and its chat. `{{command}} turn answer` takes `--gate` only; `--message` is gone. The fact `session.unread_answers` is `session.unread_gates`, counting gates, and a user workflow or `tools.toml` that names an old one fails to load naming the new one. A reply that names no `send` takes its verb's `send` in `tasks.toml`, which the bundled profile sets to `now`: set `send = "draft"` under `[verbs.reply]` in your `tasks.toml` to keep replies held for approval by default.
