---
title: The lists are the profile's views
description: Go names no view and no task property; it reads views.toml's conditions and a property's role.
---

- **Context**: the lists of tasks were Go predicates over Go-named views (Next, Focus, Outbox) and Go-named properties (`focus`, `area`, `contexts`, `due`), so a profile could word a list but not change what it held, and every new list was a Go change.
- **Decision**: a view is a row of `views.toml`: a `when` over a task's or a gate's facts, evaluated only in `internal/catalog`, with its grouping, sort, key and whether it hides a disabled plugin's tasks; the review's steps and the badge name views. The tasks service pages and counts the views it is given and never names one. A mechanism that acts on a property (the area and contexts filters, the star, the deadline) reads the property `tasks.toml` gives its `role`.
- **Consequence**: a user adds, changes or removes a list with a `views.toml`; a view reading more than a task's own fields is served whole, since gates, wakes and items change without moving the task.
- **Enforced by**: `TestGoNamesNoView`, `TestGoNamesNoBundledProperty`, `TestOnlyTheCatalogEvaluatesIntentPredicates`, `TestAViewIsTheProfilesPredicate`, `TestAUserViewLoads`. See [Tasks and views](/concepts/tasks/#views).
