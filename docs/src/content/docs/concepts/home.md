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

The fact `task.decisions` lists the kinds a task waits on, the oldest first, and `task.decision_since` says since when the oldest waits. Each kind names a view of `views.toml` (`decide-answer`, `decide-last-step`, ...) that lists its tasks; those views, and the home's own, set `rail = false`, so the rail leaves them out. A view may not be named `home`, the cockpit's home route.

## The briefing

`home.toml` lists the counters in order. A counter counts a decision kind's view, or a view of its own with its noun and clause:

```toml
[[counters]]
kind = "answer"

[[counters]]
id     = "loop"
view   = "in-loop"
noun   = { one = "task", other = "tasks" }
clause = { other = "the loop handles {counter}" }
```

Each counter with something to count is a clause, `{counter}` where its count goes; the clauses are joined into sentences of at most `per_sentence`, with `join`, `last_join` and `end`. A count reads as `count` (`{n} {noun}`). When every task a kind counts is one plugin's units, the count uses the plugin's noun and clause, from its `ui.toml` (`noun`, `[decisions.<kind>] clause`); when several plugins share it, each plugin's count is listed and the kind's own clause stands. When nothing counts, the briefing says `empty`.

## Sections and capture

Each `[[sections]]` shows the first `limit` rows of a view, titled by the view's label, each with the time its `time` names (`owed` or `moved`); its link opens the whole view. The bundled sections are `home-blocked`, the tasks that wait on you the longest owed first, and `home-stalled`, the open tasks that have not moved for the longest.

## Changing it

A user profile's `home.toml` replaces the keys it sets, and a list of counters or sections the whole list; `decisions.toml` and a plugin's `plugins/<id>/decisions.toml` replace the mappings they name. The words are localized like every profile text: `locales/<tag>/home.toml` and `locales/<tag>/decisions.toml` translate them, a plural text form by form.
