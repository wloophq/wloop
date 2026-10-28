---
title: A verb a person runs is an event
description: Every verb a person runs on an entry records one verb event, and a verb a workflow runs records none.
---

- **Context**: a workflow reacts to a GTD verb through its `verb_<verb>` event; a path that runs a verb and records nothing hides it from the profile, and a verb a workflow runs that records one lets workflows trigger each other.
- **Decision**: a verb that changes a stored entry records its event on the entry's unit in the transaction that changes it, or, when another service runs it (a message's, the bridge to a unit's action, reply), once that succeeded; a verb that changes nothing records nothing, and neither does `seen`. A request marked `Automated`, as the `entry.verb` step's, records none.
- **Consequence**: a new path that runs a verb goes through `Service.Do` or `RecordVerb`; a reaction to a verb served elsewhere is lost if the daemon stops between the verb and its record.
- **Enforced by**: `TestAVerbRecordsItsEvent`, `TestAHandledVerbRecordsItsEvent`, `TestRecordVerbRecordsAVerbAnotherServiceRan`, `TestAVerbThatChangesNothingRecordsNothing`, `TestARefusedOrSeenVerbRecordsNothing`, `TestAnAutomatedVerbRecordsNoEvent`, `TestTaskVerbMovesTheTaskAndRecordsNoVerbEvent`, `TestTaskAnswerRouteMatchesTheUnitAnswer`, `TestTheBridgeDelegatesActHeadlessWithTheTasksItems`, `TestReplySavesAUserDraftToTheOutbox`.
