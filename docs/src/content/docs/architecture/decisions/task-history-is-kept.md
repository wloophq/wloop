---
title: A task's history is kept
description: Retention never deletes the events, turns, records, answers or plan items of a task or of a unit a task holds.
---

- **Context**: retention pruned events, plan items and turns by age, unit by unit, so a task that lived for months lost the start of its timeline, and a pull request or a thread a task holds lost what its task page shows.
- **Decision**: every event, turn, record, answer and plan item of a task, or of a unit linked to a task, is kept forever: the retention job prunes only what belongs to units no task holds. A step that merges or moves tasks rekeys their history and never deletes it.
- **Consequence**: a task's timeline is complete for as long as the task exists; the store grows with the tasks.
- **Enforced by**: `TestTaskEventsAreNeverRetained`. See [The timeline and the records](/concepts/tasks/#the-timeline-and-the-records).
