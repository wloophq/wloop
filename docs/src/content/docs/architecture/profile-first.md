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
| Workflows: subject, trigger, predicate, steps, approval, limits (`workflow.toml`) | `profiles/default/workflows/<name>/` | adds workflows under `workflows/<name>/`, and overrides a bundled one file by file; `workflows.<name>` in the configuration sets a bundled one's mode, approval and switch |
| Judgment: what a review looks for, the voice of a reply | the bundled guides, `profiles/default/intent=<name>.md` | blocks whose path says when they apply, such as `repo=api/intent=address.md` |
| Partials: the text every intent's instructions share, such as the record protocol, the history and the brief | `profiles/default/partials/<name>.md.tmpl` | replaces a partial for every intent under `partials/<name>.md.tmpl`, and adds partials its intents call |
| Prompts of {{name}}'s own calls, such as the headless turn's wrapper | `profiles/default/prompts/` | replaces a prompt under `prompts/` |
| The headings of a block's conditions in a turn's judgment | `profiles/default/labels.toml` | a `labels.toml` whose keys replace the bundled ones |

A file of yours replaces the bundled file of the same path; a manifest of yours replaces the bundled manifest whole. An intent becomes a command, a skill and an action on rows; a workflow is everything {{name}} does without being asked. A broken user intent or workflow disables only itself (a broken override leaves the bundled one in force), and evaluating the plan writes nothing. See [Profiles](/concepts/profiles/) and [The sweep and workflows](/concepts/workflows/).

## What Go provides

Manifests name Go through a closed vocabulary, listed in `internal/intent/builtin` and checked when the profile loads:

- **Facts**: named, typed values a provider computes for a unit, in `builtin.Facts` (`internal/intent/builtin/facts.go`). Predicates are CEL over the facts of a slot, compiled at load by `internal/intent/expr`; an evaluation error reads as false. A fact flagged `Dim` is also a condition of a judgment block's path; today's dimension names (`organization`, `reviewer`, `kind`, ...) are aliases of facts in `internal/profile/dims.go`, and an enum field of the brief intent's `extra` is one too.
- **Steps**: the actions a workflow runs, in `builtin.Steps`, each with its reach and its guards. A profile cannot lower a guard or force a step.
- **Builtins by slot**: what an intent's `prepare`, `workspace`, `on_sent` and the other slots may name, in `builtin.Names`.
- **Marks**: what a unit's triggers handled. A workflow records its own with `[marks]` and reads any with `handled()` in a predicate; the core stores and compares them (`internal/domain/mark`), and a plugin exposes the signal a mark holds as a fact.
- **Gates**: the approval of plan items and drafts, the Outbox, the record a turn must end with, the caps and budgets, the cleanup guards.

Some decisions stay in Go as mechanism and are exposed as facts for the profile to act on:

- which GTD entries exist (`gtd.Reconcile`, the close-entry condition in `internal/domain/gtd/rules.go`);
- working-day arithmetic and who owes an item;
- the GitHub round planners' due logic (`pr.first.due`, `pr.review.action`, `pr.address.due`);
- injection markers, JSON schemas and limits;
- the guard that never sends a GitHub comment under your name without approval.

## The rules that keep the line

- Go never enumerates intents and binds none by name. It finds the intents it starts itself by their manifest's `role` (`builtin.Roles`, `Registry.ByRole`), reads a turn's history by the role stored with it, and reads how a round behaves from the manifest's `round_kind`, `cap`, `reason_fact`, `refuse_when` and `reason`; the files still naming an intent are listed with their reasons (`TestNoIntentEnumerations`).
- A builtin's name is spelled only in its tables: `internal/intent/builtin/builtin.go`, `internal/cli/builtins.go`, `internal/daemon/builtins.go`.
- Only `internal/catalog` builds action specs and evaluates an intent's predicates.
- The automation engine and the rest of the core name no provider.
- Prose is embedded only from `profiles/`; what Go still embeds is listed with its reason, and the list of what moves only shrinks (`TestProseIsEmbeddedOnlyFromProfiles`).
- The engine plans every automatic round; the round gate it replaced stays gone (`TestNothingImportsTheGate`).

## What Go still owns today

These texts and decisions are still in Go, and are moving to the profile:

- the Slack extraction prompt (`internal/adapter/source/slack/extract/prompt.md`), and the brief's prompt and taxonomy (`internal/brief`);
- the Slack proposal rules and their texts (`internal/core/proposals`), and the GitHub plugin's `github-nudge` text;
- what sending a draft does, which follows its kind and not its declared effects: a sent nudge stamps the entries it served (`internal/core/proposals`), and a nudge on a pull request records the event the next nudge's refusal reads (the GitHub plugin's publisher);
- the Outbox's verdict, shown beside each draft, of whether it would send the draft on its own, which holds a question to an owner and a comment by their kind (`internal/core/outbox`);
- the workflow subjects, pull requests, sessions, workspaces, GTD entries and Slack units (`builtin.Slots`);
- notification titles, the digest and the menu bar's copy.

## Direction

Drafts already carry a purpose, a delivery and the effects of sending them, apart from their kind, but nothing reads those fields yet. Not yet true:

- the proposal rules become default workflows over GTD entries and Slack units, with their texts in the profile;
- a sent draft does what its declared effects say, and an agent's draft declares only the effects its intent's manifest allows;
- the Slack extraction prompt and the brief's prompt and taxonomy move to `profiles/default/prompts/`.
