---
title: The home and decision queues
description: The cockpit's home, the decision kinds a task waits on, how the briefing words them, and how a profile or a plugin changes it.
---

The cockpit opens on the home: one or two sentences that count what waits on you, the first rows of a few views, and a field to capture what is on your mind. The daemon composes all of it from the profile (`GET /api/home`); the cockpit only draws it.

## Decision kinds

A decision is something a task waits on you for. Its kind is one of a closed list: `answer` (an agent's question or a post), `approve-plan`, `send-draft`, `review-step`, `last-step` (the work waits only for its last step at its source, such as a merge, which you take there), `pick-someone` (such as a reviewer) and `act` (a proposal or a trust request).

`decisions.toml` maps each gate kind to a decision kind; each plugin's layer maps its own situations and link signals in its own `decisions.toml`:

```
profiles/default/decisions.toml      [gates] question = "answer", plan = "approve-plan", ...
plugins/<id>/decisions.toml          [situations] merge = "last-step"   [signals] ready = "last-step"
```

The GitHub plugin maps `no_reviewer`, your own pull request with no reviewer asked, to `pick-someone`. In a repository where nobody reviews, its `reviewer_required` predicate says no reviewer is needed, and such a pull request is `merge`, a `last-step`, once its checks are green:

```toml
# plugins/github/predicates.toml of your profile
reviewer_required = "!(pr.owner in ['acme-solo'])"
```

The fact `task.decisions` lists the kinds a task waits on, the oldest first: those of its open gates and, while the task is open, those of its units, so a done or dropped task's threads and pull requests wait on nothing while a question still open on it does. `task.decision_since` says since when the oldest waits. Each kind names a view of `views.toml` (`decide-answer`, `decide-last-step`, ...) that lists its tasks; those views, and the home's own, set `rail = false`, so the rail leaves them out. A view may not be named `home`, the cockpit's home route.

## The briefing

`home.toml` lists the counters in order. A counter counts the decisions of a kind, one per decision as its queue lists them, or the tasks of a view of its own with its noun and clause:

```toml
[[counters]]
kind = "answer"

[[counters]]
id     = "loop"
view   = "in-loop"
noun   = { one = "task", other = "tasks" }
clause = { other = "the loop handles {counter}" }
```

Each counter with something to count is a clause, `{counter}` where its count goes; the clauses are joined into sentences of at most `per_sentence`, with `join`, `last_join` and `end`. A count reads as `count` (`{n} {noun}`). When every decision a kind counts is of one plugin's units, the count uses the plugin's noun and clause, from its `ui.toml` (`noun`, `[decisions.<kind>] clause`); when several plugins share it, each plugin's count is listed and the kind's own clause stands. When nothing counts, the briefing says `empty`.

## Sections and capture

Each `[[sections]]` shows the first `limit` rows of a view, titled by the view's label, each with the time its `time` names (`owed` or `moved`); its link opens the whole view. The bundled sections are `home-blocked`, the tasks that wait on you the longest owed first, and `home-stalled`, the open tasks no agent is working on, those that have not moved for the longest first.

## Decision queues

Each counter of a kind opens its queue (`#queue=<kind>`): the kind's decisions one at a time, the oldest first, one item per decision, so a task with two open questions is two items. An item is a task's open gate, offered with the gate's card, or a state of one of its units, offered with the unit's situation. Its primary action is, for a gate, the kind's `primary` intent when the gate offers it, else the gate's first; for a unit, the plugin layer's `[primary]` of the kind, a catalog action of its units or `open`, which opens the unit at its source. No queue merges: a unit that waits only for a merge opens where you merge it. The queue's title is the plugin's `[decisions.<kind>] queue` when every item is that plugin's, else the kind's. `GET /api/queues/<kind>?q=<lens query>` narrows a queue as the counters narrow, which the Outbox's groups use to review their drafts as a queue.

In the cockpit a queue's route keeps its kind (or its list of tasks) through every link the queue opens, so opening a task from a queue and closing it brings the queue back. The screen draws and runs only the queue its route names: while another one loads it shows the loading line, and a failed load shows why until a load succeeds. Enter runs the item's primary: a gate's intent as its card does, a draft's at the revision on screen (with the edited text while you edit it), a catalog action through the same path as the unit's detail, with its confirm, its input and its basis. `j` and `k` walk the items, `o` opens the unit at its source, Esc goes home. On a draft's card `e` edits, `x` discards, `m` closes without a message and `n` stops the conversation's nudges, each only when the card offers that action; a refused one says why, and a key the card does not offer goes on to its other binding. Each button shows its key. On a gate's card the digits pick its intents, focused or not.

## Search

`GET /api/search?q=<lens query>` finds every task the query matches in the rail's views, accents and case aside, and groups them by `omnibox.toml`'s `[[search.groups]]`: a result goes to the first group whose view holds it, else to the last group (`search.other`). A group shows its first `search.limit` results, those whose title or unit holds the query as whole words first, then the view's order, and counts them all; each result lists the rail views it appears in.

## Typed text

`POST /api/omnibox/resolve` classifies what you type and runs nothing:

```
one token a plugin claims (a pasted address)    -> unit: its tasks and the actions its row offers
first word one of a verb's words:
  each ref names one unit or task                -> verb: one operation per ref, to confirm
  a ref names more than one                       -> agent (never guessed)
  a ref names nothing                             -> search
  no ref, and the verb has an op                  -> verb: capture or start the rest of the text
anything else                                     -> search
```

`omnibox.toml`'s `[verbs.<id>]` list each verb's `words`, in every language in one list (vocabulary, not a translated text), and its target: a catalog `action` on each unit named, a task `verb` on each task named, and an `op` (`capture` or `start`) for text that names nothing. A verb whose task verb resolves a gate (approve, answer, edit, retry, dismiss), or whose action resolves a gate, posts or discards unpublished work (`draft.edit`, `draft.retry`, `draft.close-silently`, `draft.skip-workflow`, `plan_apply`, `plan_dismiss`, `remove_force`), does not load: a gate is resolved on the gate itself. A verb on an action the bundled vocabulary names that no enabled plugin offers leaves the omnibox quietly, in whichever layer it is declared; any other unknown action is a problem. A lone ref (a pasted address, a unit's label, a task id) opens its unit, or the task itself when it holds none. No operation runs, typed or proposed by the omnibox's agent, that no verb targets.

In the omnibox the arrows (or Ctrl+N and Ctrl+P) walk the choices: the operations to confirm, a pasted address's unit, the agent where only it would help, then every search result of every group; the first is selected when results arrive, a selection stays on its choice while the results redraw, and new text starts over. Enter runs the selection, Cmd+Enter asks the agent, Esc closes from anywhere in it, and Tab stays inside it. The floating panel shows an empty omnibox each time it comes back, in the daemon's current strings, and reloads when the daemon serves another build.

`POST /api/omnibox/run` runs the operations you confirmed, in order, each as one request to the handler a click reaches (`POST /api/units/<provider>/<key>/<action>`, `POST /api/tasks/<id>/<verb>`, `POST /api/tasks`), with your request's headers, so each is refused or gated exactly as that click; each result keeps the handler's status, refusal and output. An operation no verb targets is refused alone, and the others run. `start` captures a task briefed with the text, titled as the `start` intent titles a capture, then starts it in the repository named; the start is not tried when the capture fails.

## Changing it

A user profile's `home.toml` replaces the keys it sets, and a list of counters or sections the whole list; `decisions.toml` and a plugin's `plugins/<id>/decisions.toml` replace the mappings they name. The words are localized like every profile text: `locales/<tag>/home.toml` and `locales/<tag>/decisions.toml` translate them, a plural text form by form.
