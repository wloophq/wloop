---
title: Go reads categories, never states
description: The profile names a task's states; Go and the store's SQL read only the closed set of categories.
---

- **Context**: the lists, the Inbox rules and the store's queries compared a task's state with the bundled names (`inbox`, `next`, `waiting`, `done`), so a profile that renamed or added a state broke them without a load error.
- **Decision**: `tasks.toml` declares the states, each with one category from the closed set `inbox`, `ready`, `active`, `waiting`, `deferred`, `done`, `dropped`. Go reads a task's category and never names a state: no Go literal is converted to, assigned to or compared with a state or a category outside the profile's loader and the version 7 migration, which carries the states of version 6 as history, but the category constants that declare the closed set; no SQL under the store compares the state column with a bundled state, and a category is bound as a parameter.
- **Consequence**: a user profile renames, adds or removes states freely; a new behavior that depends on where a task stands asks for its category, or for a view's condition in the profile.
- **Enforced by**: `TestGoNamesNoState`. See [Lists are categories](/concepts/tasks/#lists-are-categories).
