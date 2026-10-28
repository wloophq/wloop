---
title: Profile first
description: Behavior and text belong to the profile; Go provides facts, mechanisms and gates. Where the line runs today.
---

{{name}} is meant to be customized. A behavior decision (when something happens, on which units, where it runs, what it instructs, which effects it asks for) is an intent or a workflow of the profile, and every text an agent or a person reads belongs there too. Go provides facts, mechanisms and gates: it reads units, launches agents, publishes, and holds the approval, the Outbox, the records, the budgets and the guards. It does not decide what to do and does not write prose.

The embedded `profiles/default/` is the lowest layer of every profile. Your profile, the directory the configuration's `profile` names, sits above it.

## What lives in the profile

| What | Bundled in | Your profile |
|---|---|---|
| Intents: a manifest (`intent.toml`) and its instructions (`instructions.md.tmpl`) | `profiles/default/intents/<name>/` | adds intents under `intents/<name>/`, and overrides a bundled one file by file under its name |
| Workflows: provider, trigger, predicate, steps, approval, limits (`workflow.toml`) | `profiles/default/workflows/<name>/` | adds workflows under `workflows/<name>/`, and overrides a bundled one file by file; `workflows.<name>` in the configuration sets a bundled one's `observe`, approval and switch |
| The words of the built-in actions (Run now, Skip, Take over, ...): `label`, `summary`, `note`, `warning`, `confirm`, `why` and an input's `label` and `help` | `profiles/default/actions.toml` | an `actions.toml` whose tables replace the bundled ones |
| Judgment: what a review looks for, the voice of a reply | the bundled guides, `profiles/default/intent=<name>.md` | blocks whose path says when they apply, such as `repo=api/intent=address.md` |
| Partials: the text every intent's instructions share, such as the record protocol, the history and the brief | `profiles/default/partials/<name>.md.tmpl` | replaces a partial for every intent under `partials/<name>.md.tmpl`, and adds partials its intents call |
| Prompts of {{name}}'s own calls, such as the headless turn's wrapper | `profiles/default/prompts/` | replaces a prompt under `prompts/` |
| The headings of a block's conditions in a turn's judgment | `profiles/default/labels.toml` | a `labels.toml` whose keys replace the bundled ones |
| What an agent reads about each MCP tool: its `description`, its parameters' (`params`, by path) and what it answers (`answers`, by name) | `profiles/default/tools.toml` | a `tools.toml` whose keys replace the bundled ones |
| The task states and their categories, the task properties with their types and writers, the verbs' words (`label`, `summary`, the button's tooltip, `input`, the question a verb that asks for a value puts first) and where a task offers each (`offer`) | `profiles/default/tasks.toml` | a `tasks.toml` whose keys replace the bundled ones and whose new states and properties come after the bundled ones |
| The gates: each one's kind, words and the intents it is resolved with | `profiles/default/gates.toml` | a `gates.toml` whose keys replace the bundled ones and whose new gates and intents come after the bundled ones |
| The labels of the views and of the review's steps | `profiles/default/views.toml` | a `views.toml` whose keys replace the bundled ones |
| The help line of each core key, by context, then action | `profiles/default/keys.toml` | a `keys.toml` whose keys replace the bundled ones |
| What the loop tells a session: the notice that the user answered an Inbox message, and the reason a stop is blocked while the turn owes a record | `profiles/default/notices.toml` | a `notices.toml` whose keys replace the bundled ones |

A file of yours replaces the bundled file of the same path; a manifest of yours replaces the bundled manifest whole. An intent becomes a command, a skill and an action on rows; a workflow is everything {{name}} does without being asked. A broken user intent or workflow disables only itself (a broken override leaves the bundled one in force), and evaluating the plan writes nothing. See [Profiles](/concepts/profiles/) and [The sweep and workflows](/concepts/workflows/).

## What Go provides

Manifests name Go through a closed vocabulary, listed in `internal/intent/builtin` and checked when the profile loads. Every reserved key of a manifest, of `actions.toml`, `tasks.toml`, `views.toml`, `gates.toml`, `keys.toml` and of the configuration is a row of one table, `builtin.Grammar` (`internal/intent/builtin/grammar*.go`), with its kind, values, default and doc; every loader runs `builtin.Check` against it, and the [profile language](/reference/language/) lists it:

- **Facts**: named, typed values a provider computes for a unit, in `builtin.Facts` (`internal/intent/builtin/facts.go`). Predicates are CEL over the facts of a slot, compiled at load by `internal/intent/expr`; an evaluation error reads as false. A fact flagged `Dim` is also a condition of a judgment block's path; today's dimension names (`organization`, `reviewer`, `kind`, ...) are aliases of facts in `internal/profile/dims.go`, and an enum field of the brief intent's `extra` is one too.
- **Steps**: the actions a workflow runs, in `builtin.Steps`, each with its reach and its guards. A profile cannot lower a guard or force a step.
- **Roles**: what binds an intent to a plugin's mechanism, in `builtin.Roles` (`internal/intent/builtin/roles.go`): `pr.review`, `pr.address`, `pr.nudge`, `probe` and `headless`. A manifest names its `role`; Go derives what the role implies (see below).
- **Effects**: what sending a turn or a draft applies, in `builtin.Effects` (`internal/intent/builtin/effects.go`), each applying to a turn, a draft or both: an intent's `effects` when its turn is sent, a `draft.propose` step's `effects` and an intent's `[drafts] allowed_effects` when a draft is sent.
- **Builtins by slot**: what an intent's `workspace` and an input's `before` may name, in `builtin.Names`.
- **Marks**: what a unit's triggers handled. A workflow records its own with `[marks]` and reads any with `handled()` in a predicate; the core stores and compares them (`internal/domain/mark`), and a plugin exposes the signal a mark holds as a fact.
- **Gates**: the approval of plan items and drafts, the Outbox, the record a turn must end with, the caps and budgets, the cleanup guards.

Some decisions stay in Go as mechanism and are exposed as facts for the profile to act on:

- which tasks exist (`task.Reconcile`, the close-task condition in `internal/domain/task/rules.go`);
- when a plugin's unit has ended (`<slot>.ended`, such as `pr.ended` and `slack.ended`), which `task.link_ended` reads; whether a turn attaches its results to its task is the intent's (`record.attach_url`, `drafts.attach_posts`), and closing the task is the `task-results-ended` workflow's;
- working-day arithmetic and who owes an item;
- the GitHub round planners' due logic (`pr.first.due`, `pr.review.action`, `pr.address.due`);
- injection markers, JSON schemas and limits;
- the guard on draft approval: a draft's delivery is what its plugin posts it as (every GitHub draft but a reaction or an edit is a comment), a comment waits for your approval unless a rule of yours that names comment delivery releases it, and no agent or thread text sets an approval (no draft rule, nor a run rule that runs, reads an untrusted fact; a workflow's `when` may, and a run it starts releases a draft only through that draft's approval).

## The rules that keep the line

- Go never enumerates intents and binds none by name. It finds the intents it starts itself by their manifest's `role` (`builtin.Roles`, `Registry.ByRole`), and reads a turn's history by the role stored with it; the files still naming an intent are listed with their reasons (`TestNoIntentEnumerations`).
- The `role` binds an intent to a plugin's mechanism, and Go derives what the role implies from `builtin.Roles`: the round kind, the reconcile with the review you published, the prepare that fills a nudge's inputs, the context and launch of a probe, the agent policy of its sessions, and whether its turns drive their workspace's row. A manifest does not write those. Which board a turn moves follows from its `workspace` (`review` or `author`, else none), and every automated `turn.start` on a unit spends its daily budget, whatever its intent declares: `turns.per_unit_per_day` on a unit whose plugin keeps a per-unit count, `turns.per_day` on the others. A user intent that takes a bundled role takes all of it over; the loader still refuses two intents that drive one workspace's row.
- What a turn says about itself is the profile's: its `why`, its `[[refuse]]` conditions with their `why`, and its action's `note`, `warning`, `confirm` and `[[action.refuse]]`, templates over facts that Go computes (`pr.review.reason`, `pr.nudge.refusal`, `pr.mute.refusal`).
- A builtin's name is spelled only in its tables: `internal/intent/builtin/builtin.go`, `internal/cli/builtins.go`, `internal/daemon/builtins.go`.
- Every key a profile or the configuration may write is a row of `builtin.Grammar`; any other key, a removed one included, fails at load, naming a close key when one exists, and no old spelling is read. The JSON schemas and the reference pages of the manifests, the configuration and the language are generated from it (`make docs-ref`).
- Only `internal/catalog` builds action specs and evaluates an intent's predicates.
- The automation engine and the rest of the core name no provider.
- Go never names a draft's kind, compares its purpose with a value, or reads its delivery outside the publishers and the Outbox (`TestNoDraftKindComparisons`).
- A draft's text comes from a profile template or from whoever wrote it, and only `draft.propose` and the plugin rules its allowlist names propose drafts on their own (`TestDraftTextComesFromTheProfile`, `TestOnlyTheDraftStepProposes`).
- Prose is embedded only from `profiles/`; what Go still embeds is listed with its reason, and the list of what moves only shrinks (`TestProseIsEmbeddedOnlyFromProfiles`).
- The engine plans every automatic round; the round gate it replaced stays gone (`TestNothingImportsTheGate`).

## What Go still owns today

These texts and decisions are still in Go, and are moving to the profile:

- the GitHub plugin's `github-nudge` rule and its text (`internal/adapter/source/github/proposals.go`);
- the workflow providers, pull requests, sessions, workspaces, tasks and Slack units (`builtin.Slots`);
- notification titles, the digest and the menu bar's copy;
- the cockpit's and the terminal's chrome: the hints a prompt gives on its keys (Tab, Ctrl+E), the titles and empty states of the panes, the modes of the delegate and reply prompts (draft for me, hand to an agent, send now), and the reason shown when a task does not offer a verb;
- the renderings of Slack messages that `slack_read` and `slack_search` return (`internal/adapter/source/slack`), the JSON-RPC errors of the MCP endpoint, and the error texts a mechanism returns through a tool's `failed` answer.

## Direction

A draft is what its proposer declares: a purpose, a delivery and the effects sending it applies, each once, through the effect table and a plugin's own (`TestNoDraftKindComparisons`). An agent's draft declares only the effects its intent lists in `[drafts] allowed_effects`, and a workflow proposes, spaces out (`repeat_after`, `repeat_after_sent`) and withdraws (`[drafts.withdraw]`) its drafts with `draft.propose`, its text from the profile.

