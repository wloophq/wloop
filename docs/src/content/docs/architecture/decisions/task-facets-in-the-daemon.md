---
title: A task's facets are the daemon's
description: The daemon computes a task's facets from its facts, brief and units' rows, and a unit's move re-sends the tasks holding it.
---

- **Context**: a task view lists tasks that mix Slack conversations and pull requests, and the fields to filter, group and sort by live in the task's own facts, its brief and the rows of its units. A client that joined them would hold the merge rules, and a change to a row, a brief or a link moved no task topic, so a view could keep a stale facet or membership.
- **Decision**: the daemon computes a task's facets when it serves the task, from the three layers and the profile's `[facets.<id>]` declarations, and clients do no join. A change event carries the units that moved, and the next page of tasks holds every task holding one of them (`units` on `GET /api/tasks`), those that left the view in its removed; a change too large to list, or a reconnection, makes the client read its list whole.
- **Consequence**: a new source of a task's value is a facet declaration or a unit row facet, never client code; a write that changes what a task shows must publish the units it touched.
- **Enforced by**: `TestARowChangeResendsTheTasksHoldingItsUnit`, `TestABriefWriteResendsItsTask`. See [Facets](/concepts/tasks/#facets) and [The daemon and its clients](/architecture/daemon-and-clients/).
