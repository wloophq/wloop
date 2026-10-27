---
title: Decision log
description: The architecture decisions in force, each with its context, its consequence and the test that keeps it.
---

One entry per decision in force. A pull request that changes one of them changes its entry.

## One daemon writes the store

- **Context**: several processes writing one SQLite file need two lock disciplines and still race, and a lock held across a network call stalls every other writer.
- **Decision**: only the daemon opens the store as its writer, and `store migrate` and `store restore` under the store's lock with the daemon stopped. Other processes go through the daemon's API, or read the store with `sqlite.OpenReadOnly` when the daemon is down.
- **Consequence**: one transaction is the unit of consistency; clients fail when the daemon is down instead of writing around it.
- **Enforced by**: `TestOnlyServeWritesTheStore`, `TestOnlyTheStoreTouchesSQL`. See [The daemon and its clients](/architecture/daemon-and-clients/).

## Clients are thin and speak one versioned API

- **Context**: a rule computed in a client drifts from the same rule in another.
- **Decision**: the daemon computes rows, actions, facets, counters and notifications; clients parse, call one operation and render. `internal/api` is the only schema, versioned by its major.
- **Consequence**: the CLI, the terminal client, the menu bar and the cockpit show the same thing; a new surface is rendering only.
- **Enforced by**: `TestArchitecture`, `TestCockpitScriptsNameNoPlugin`; the wire goldens.

## Ports and adapters, dependencies inward

- **Context**: use cases that shell out or speak HTTP cannot be tested without the tools, and every new tool edits them.
- **Decision**: the domain imports only itself; the core imports the domain, its ports and the meter; adapters implement the ports; one composition root picks them.
- **Consequence**: the core runs in tests with no network and no process; a new agent, manager or sink is an adapter and a registry entry.
- **Enforced by**: `TestArchitecture`, `TestCoreImports`, `TestDomainImports`, `TestDaemonImports`, `TestReadModelImports`. See [Layers](/architecture/layers/).

## Small ports owned by their consumer

- **Context**: a wide interface every consumer mocks whole couples them all to every capability.
- **Decision**: a port is declared by the core package that uses it, with at most five methods; a capability only some adapters have is an optional interface found by type assertion.
- **Consequence**: a manager without a board, or an agent without questions, plugs in without stubs.
- **Enforced by**: `TestArchitecture`. See [Ports and adapters](/architecture/ports/).

## One adapter reaches each outside tool

- **Context**: tool names and protocols scattered through the code make a second agent or manager a rewrite.
- **Decision**: only `internal/github` reaches GitHub, only the Orca and Claude Desktop managers reach their apps, only the store touches SQL, and provider names live in adapters and the root. GitHub is read in process with budgets; Orca through its socket.
- **Consequence**: a change of tool or protocol stays in one package.
- **Enforced by**: `TestProvidersStayInAdaptersAndTheRoot`, `TestOnlyOrcaAdapterReachesOrca`, `TestOnlyClaudeDesktopManagerReachesDesktop`, `TestOnlyGitHubReachesGitHub`, `TestOnlyTheStoreTouchesSQL`.

## Units generalize the pull request

- **Context**: a loop keyed by pull request cannot hold a Slack thread, a session or a workspace.
- **Decision**: work is keyed by `unit.Unit{Provider, Key}`; only the provider's codec mints and parses a key.
- **Consequence**: the turns, the workflows, the GTD core and the read model serve any provider.
- **Enforced by**: `TestLoopCodeImportsNoPR`, `TestTheCoreNamesNoProvider`.

## Sources are compiled-in plugins

- **Context**: the core must not change per source, and a plugin framework costs more than the two sources need.
- **Decision**: a source is a `source.Plugin` listed in `internal/cli/sources.go`, contributing through optional `source.Module` fields; the core imports no plugin and no plugin imports another.
- **Consequence**: a disabled plugin leaves the daemon whole; the remaining edges from the core to the GitHub plugin are listed as debt.
- **Enforced by**: `TestCoreImportsNoPlugin`, `TestOnlySourcesGoNamesASource`. See [Plugins](/architecture/plugins/).

## The store is the truth

- **Context**: workspaces and sessions are deleted and recycled; a board can be moved by hand.
- **Decision**: turn state, questions, answers and history live in the store. Workspace managers and boards are sync targets: they receive a projection, fail alone and are reconciled on start; {{name}} never reads a card's column back as state.
- **Consequence**: any workspace or session can be recreated from stored context.
- **Enforced by**: the views' contract suites.

## What the loop proposes to post is a draft

- **Context**: a post under your name cannot be taken back, and a retry after a crash can post twice.
- **Decision**: what {{name}} proposes to post is a draft in the Outbox; only an approved draft reaches the plugin's publisher, once, with no automatic retry, and `Verify` settles a draft a crash left in `sending`.
- **Consequence**: approval policies decide which drafts go out unattended; the loop never hands an agent a source's token.
- **Enforced by**: `internal/core/outbox` tests.

## A worktree belongs to the manager that created it

- **Context**: Orca lists every git worktree of a repository it knows, including the ones Claude Desktop made, so the first manager to list a worktree is not its owner.
- **Decision**: the owner of a worktree is the manager that created it, as the store's custody or the manager itself says; `workspace_managers` orders only managers that merely list it, and a round falls through to such a manager, with the reason logged, only when the owner cannot launch the session.
- **Consequence**: a round on a pull request continues in the app it was developed in, and a manager that only lists a worktree never becomes its owner. A session a round started in such a manager is kept by the next round while it lives. Removing the workspace of a worktree Claude Desktop made removes it through Claude Desktop.
- **Enforced by**: `TestSetListGivesAWorktreeToTheManagerThatCreatedIt`, `TestARoundContinuesInTheManagerThatCreatedTheWorktree`, `TestARoundFallsThroughWhenTheCreatorCannotHostIt`.

## Authority comes from you

- **Context**: an agent reads text written by others and can be steered by it.
- **Decision**: agents report facts; endorsing a verdict, approving, taking over and removing a workspace come only from your surfaces. A session is identified by a header its manager fills, never by a tool argument.
- **Consequence**: an agent cannot endorse its own work through its channel.
- **Enforced by**: the daemon's and MCP's tests. See [Security](/concepts/security/).

## Behavior lives in the profile

- **Context**: behavior and text in Go cannot be changed by the person who uses {{name}}, in their language or their way of working.
- **Decision**: intents, workflows and judgment blocks are profile files, and a user profile overrides any bundled one; Go provides a closed vocabulary of facts, steps, roles, effects, builtins and gates, and never enumerates intents.
- **Consequence**: a new behavior is a profile change; Go grows only by a fact, a step or a gate.
- **Enforced by**: `TestNoIntentEnumerations`, `TestProseIsEmbeddedOnlyFromProfiles`, the boundary rules, `TestBuiltinNamesLiveInTheirTables`. See [Profile first](/architecture/profile-first/).

## A block's conditions are facts

- **Context**: a judgment block's path could name only a closed list of pull request dimensions, matched by code that knew each one, with English headings in Go; a Slack intent or a field the user adds to the brief had no path.
- **Decision**: a path condition reads a fact. A fact flagged `Dim` in `builtin.Facts` is a dimension under its own name, an enum field of the brief intent's `extra` under its field's name, and today's names are aliases of facts (`organization` of `pr.owner`, `reviewer` of `pr.address.reviewers`, `kind` of `brief.kind`); a dimension whose fact belongs to a provider applies only under that provider's intents, and closed values come per provider. The headings of conditions are the profile's `labels.toml`.
- **Consequence**: a new dimension is a fact with its flag, and a turn matches its blocks against the values of the facts the dimensions read; today's paths, their order and their headings are unchanged.
- **Enforced by**: `internal/profile`'s `TestTodaysDimsResolveThroughFacts`, `TestAFactFlaggedDimIsABlockPathCondition`, `TestSegLabelsComeFromTheProfile`, `TestAnExtraEnumIsABlockPathAndAFacet`, and the rendered-turn goldens in `internal/turn/testdata`.

## The core owns what a trigger handled

- **Context**: each trigger that must fire once per signal (a review, a head's failing checks, a head's conflict) needed its own column and migration, and a source other than GitHub or a profile's own trigger had nowhere to keep it.
- **Decision**: what a trigger handled is a mark: a value per unit and name in `unit_marks`, keyed by `unit.Unit`, written in the transaction of the write that handled it, and deleted with an ended pull request, a pull request gone from the inventory, or a session no longer stored. The core owns the encoding and the comparison (`internal/domain/mark`: a string is a version, a time or an int a watermark); a plugin owns its signal's meaning, exposes it as a fact and names its own marks; a workflow records its marks with `[marks]`, under its name, and reads any with `handled()`.
- **Consequence**: a new trigger needs no schema change, and a profile can define one that fires once per signal; a signal that does not fit a version or a watermark stays in its plugin's planner.
- **Enforced by**: the `internal/domain/mark` tests, the ledger contract (`ADoneOrDismissedRunSetsTheItemsMarks`), the manifest goldens in `internal/intent/testdata/workflows`.

## One engine plans automatic work, and planning writes nothing

- **Context**: two automation mechanisms side by side disagree, and a plan that writes while it is read changes under the reader.
- **Decision**: workflows run by `internal/core/automation` are the only planner of automatic rounds and cleanup; evaluating the plan writes nothing, spawns nothing and reads no GitHub; irreversible steps ship in `observe` and automatic runs never force.
- **Consequence**: `{{command}} plan` is the dry run.
- **Enforced by**: `TestNothingImportsTheGate`, `TestTheEngineImportsNoProvider`, the engine's tests.

## The store migrates forward

- **Context**: a user's store holds work that cannot be recreated.
- **Decision**: every schema change is a new version with a migration, run after a backup, and a store newer than the binary is refused.
- **Consequence**: `{{command}} store restore` takes a store back to a backup; a migration in a release is never edited.
- **Enforced by**: the migration tests in `internal/adapter/store/sqlite`, which fail on a schema change without a migration.

## The read model lives in memory

- **Context**: deriving rows per request costs a query per row on every refresh.
- **Decision**: writes note an in-memory change log; the read model rebuilds rows from one store snapshot on change and serves deltas that clients pull after an event.
- **Consequence**: serving rows costs no SQL; a restart starts a new epoch and clients reload.
- **Enforced by**: `TestEveryWriteNotes`, `TestEveryExportedMethodIsReadOrNoted`, the projection tests.

## Every spawn is metered

- **Context**: spawning `gh` or `orca` on a hot path made the daemon slow and the cost invisible.
- **Decision**: a package that spawns a process imports `internal/meter`, which counts each boundary call by port; GitHub and Orca are reached in process or over a socket.
- **Consequence**: `{{command}} doctor --load` shows what each job and route costs; budget tests assert it.
- **Enforced by**: `TestSpawningPackagesImportMeter`, `TestMeterIsALeaf`.

## One brand package

- **Context**: the product's name must change in one place.
- **Decision**: every user-visible name derives from `internal/brand`; internal names are brand-neutral (`X-Loop-*`, `LOOP_*`); the docs write the name through placeholders.
- **Consequence**: a build with another name writes nothing that names the default one.
- **Enforced by**: `TestBrandLiteralOnlyInBrand`, `make test-brand`, `make scan-brand`.

## Architecture rules are tests

- **Context**: a rule only in prose is broken by the next change that does not read it.
- **Decision**: each rule is a test in `internal/archtest` with a self-test that plants a violation and a doc comment that states the rule; allowlists name their reason and fail when stale. The rules reference page is generated from those sources.
- **Consequence**: exceptions are visible and only shrink, and the list of rules cannot drift from the tests.
- **Enforced by**: the self-tests, `TestEveryRuleTestStatesItsRule`, `TestRulesDocIsCurrent`. See [Testing](/architecture/testing/#the-architecture-tests) and [Architecture rules](/reference/architecture-rules/).

## Text an agent reads resolves through the profile

- **Context**: a text fixed in Go can only be changed by a rebuild, and a user who wants the record step or the headless prompt in their own words has nowhere to write them.
- **Decision**: the turn partials live in `profiles/default/partials/` and the prompts of {{name}}'s own calls in `profiles/default/prompts/`; a file of the user's profile replaces the bundled file of its path. The descriptions of the MCP tools and their parameters, and what each tool answers, live in `profiles/default/tools.toml`, by tool, parameter path and answer name; Go hands an answer the fields `builtin.ToolAnswers` lists for it, and a user's `tools.toml` replaces the keys it sets. Go keeps the markers, the schemas and the limits around them, and checks that a record partial still states the record protocol.
- **Consequence**: a broken partial or wrapper of the user's falls back to the bundled one with a problem, so a profile edit never stops a turn from ending; the rendered turns of the bundled profile are unchanged.
- **Enforced by**: `TestProseIsEmbeddedOnlyFromProfiles`, `TestARecordPartialMissingTheProtocolLoadsTheBundledOne`, `TestTheBundledWrapperWritesTheOldPrompt`, `TestEveryToolIsDescribedByTheBundledProfile`, `TestAToolTextFailsAtLoad`, `TestAnswersComeFromTheProfile`, the scenario goldens. See [Profile first](/architecture/profile-first/).

## A draft is what its proposer declares

- **Context**: what sending a draft did, and whether it was held, followed its kind, so a closing message posted by a session closed its unit and a renamed purpose lost its behavior.
- **Decision**: a draft carries a purpose, a delivery and its effects; the Outbox applies the declared effects once each draft is sent, by name, a plugin's own first, and stores when (`drafts.effects_applied_at`), so a restart applies only what is pending. An agent declares only the effects its intent's `[drafts] allowed_effects` lists. A Slack unit's closure is its own column (`extraction_cursors.closure_marked`), which no extraction pass writes. Workflows space out their drafts from the drafts themselves (`repeat_after`, `repeat_after_sent`), per workflow, unit and person, and withdraw them by their `[drafts.withdraw] when`; a draft that `[drafts.withdraw] when` would withdraw at once is not proposed, so the pair never cycles. A draft's approval is its proposer's `[drafts.approval]`: ordered rules over the facts of the draft (`draft.delivery` as its unit's plugin posts it, `draft.proposer`, and `draft.purpose` and `draft.effects` only when a workflow or a plugin rule named them) and of its unit, then a mode; a rule applies only to a draft whose unit its facts describe, and never reads a fact the facts table marks untrusted, one a third party's text sets through the extraction or the proposing turn moves before its drafts are proposed; the configuration's `workflows.<name>.drafts.approval` overlays a workflow's table field by field (`mode` and `window` replace those fields, `rule` the rules), over a workflow that has none the intent's table for its turns' drafts and `wait` for its own; the configuration loads what holds alone, and the merged table is validated where the profile is loaded, one that does not merge holding every draft of the workflow and named by `doctor`. A comment draft waits for a person unless a rule whose `when` has `draft.delivery == "comment"` as a term of its top-level `&&` releases it, which the loader reads from the predicate's syntax tree, so a catch-all mode never posts a comment under the user's name: a plugin declares how it posts each delivery (GitHub posts every draft but a reaction or an edit as a comment, and refuses those two), and the Outbox stores and reads that delivery. An edit replaces the text of a message the user posted; `slack_edit` makes it at once and records it as a sent draft that keeps the text it replaced (`drafts.replaced`), it refuses a message someone else posted, and the Slack publisher refuses to publish an edit from the Outbox. A draft an agent aims outside its own unit waits whatever its policy, unless it is `dry_run`; a draft a turn proposes on another unit, such as a pull request's turn drafting in a Slack thread, names that turn's unit as its origin (`drafts.origin_provider`, `drafts.origin_unit_key`), where its policy and its pin read the turn, answers no item and declares no effect. A post an agent makes at once, without a draft, is recorded as a sent draft on the unit it landed in, tracked or not, with the turn that made it (in a direct message not segmented yet, on its message, which the segment that later covers it takes over in the transaction that writes the segment), so the Outbox holds everything posted in the user's name through the daemon.
- **Consequence**: the kind survives only as the input word of agents and the API, mapped once, and as what the API serves; the clients group drafts by purpose.
- **Enforced by**: `TestNoDraftKindComparisons`, `TestASentDraftAppliesItsEffectsOnce`, `TestEffectsAreAppliedOnceAcrossARestart`, `TestASubjectMatchingWhenAndWithdrawWhenIsProposedOnce`, `TestACommentDraftWaitsForAPersonWhateverItsMode`, `TestARuleThatNamesCommentDeliveryReleasesAComment`, `TestSlackDraftFromAPullRequestTurnDraftsInThePermalinksThread`, `TestADraftOfATurnOnAnotherUnitTakesThatTurnsPolicy`, `TestSessionSendsToUntrackedPlacesAreRecorded`, `TestFromAPullRequestTurnEditsAndReactionsPostAtOnceAndAreRecorded`, `TestAnEditIsNeverPublishedAsADraft`, `TestPostingOutsideATurn`, `TestAnAgentsPurposeNeverMatchesAPurposeRule`, `TestRequiresIsATermEveryMatchHolds`. See [The Outbox](/concepts/approval/#the-outbox).

## A draft's placement is decided when it is proposed

- **Context**: a Slack draft on a direct message segment with no target went out at the top level of the conversation, because the publisher turned an empty target into the conversation at send time; where a draft landed was invisible in the Outbox and to the approval rules until it was sent.
- **Decision**: when a draft is proposed with no target, the Outbox asks its unit's publisher to place it (`port.Placer`), stores the answer as the draft's target and validates that; an error refuses the proposal with its reason. The core names no provider: the plugin reads the evidence of the draft's items. Slack places a thread unit's draft in its thread (an empty target) and a segment's draft at the newest message its items anchor on, whose thread the reply joins or starts; a segment draft with neither target nor such items is refused. A placement stays inside the unit, so it never counts as a target outside it. A draft stored without a target before placement existed is placed when it is approved, retried or released by its policy. The Slack publisher refuses a segment draft with no target.
- **Consequence**: the Outbox shows where every draft posts before it is approved; the closure and owner questions of a direct message reply in the thread of the message they answer.
- **Enforced by**: `TestADraftWithNoTargetStoresItsPlacement`, `TestADraftThePluginCannotPlaceIsRefused`, `TestAPlacementIsInsideTheUnit`, `TestApprovingADraftProposedUnplacedPlacesIt`, `TestAReplyToATopLevelDMMessageStartsItsThread`, `TestItemsAcrossThreadsPlaceInTheNewestAnchorsThread`, `TestASegmentDraftWithNothingToAnswerIsRefused`, `TestAnUnplacedSegmentDraftIsNotPublished`. See [Slack](/concepts/slack/#the-agent-tools).

## Intents by role

- **Context**: Go started the review, address, nudge and probe intents and read their history by name, so a renamed or replaced intent lost every automatic behavior.
- **Decision**: a manifest's `role` (`pr.review`, `pr.address`, `pr.nudge`, `probe`, `headless`) binds an intent to a plugin's mechanism and is what Go resolves it by; one of the user's holding a bundled role takes it over. Each turn stores its intent's role, and history readers read by it. What a role implies is a row of `builtin.Roles`, which no manifest writes: its round kind, its reconcile, its prepare, its context and launch, its sessions' agent policy, and whether its turns drive their workspace's row. The board a turn moves follows from its `workspace`, and every automated `turn.start` on a unit spends its daily budget, whatever its intent declares: `turns.per_unit_per_day` on a unit whose plugin keeps a per-unit count, `turns.per_day` on the others. A turn by hand spends none. What a turn says about why it is due or refused is the manifest's: `why`, and `[[refuse]]` entries over facts Go computes (`pr.nudge.refusal`, `pr.mute.refusal`).
- **Consequence**: an intent `my-review` holding `pr.review` gets the sweep's review rounds, their history and their row; a manifest cannot pair a role with a round it does not run. The loader refuses two intents of one layer holding one role, and two intents driving one workspace's row. The turns stored before roles exist were given theirs by the store's migration from the bundled names.
- **Enforced by**: `TestNoIntentEnumerations`, `TestARoleImpliesItsRound`, `TestTheKeysARoleImpliesAreUnknown`, `TestDrivesRowMovesWithTheRole`, `TestTwoDriversInOneWorkspaceFail`, `TestTwoIntentsOfOneLayerCannotHoldARole`, `TestARenamedReviewKeepsTheVerdictTheBundledReviewRecorded`.

## turn.start on any unit, under a budget

- **Context**: a workflow could start turns only on pull requests, whose rounds have their own caps.
- **Decision**: `turn.start` starts any intent on any unit, headless or in a session, in the repository its manifest's workspace table names; outside the round providers each workflow needs `limits.per_day`, and `turns.per_day` caps all of them together per local day, on units whose plugin keeps no per-unit count. A repository named by an input is never taken by a workflow.
- **Consequence**: a request in a thread can start an implementing turn in a known repository; no thread text chooses the repository, and a runaway workflow stops at its budget.
- **Enforced by**: the loader (`limits.per_day`) and the step's guards.

## One brief per unit

- **Context**: the brief was keyed by pull request, so a Slack conversation had only a free-text summary that no fact, block or filter could read.
- **Decision**: briefs, their tags and their runs are keyed by unit. `brief_runs` is provider-neutral: what the unit was at is `revision`, in the provider's format, and its provider's parts of the input are `input`. The brief queue answers by unit: a provider's `BriefSource` says what a unit is at, reads its input, and decides when a brief is stale and what an attempt was made from; GitHub's rules are the ones the queue had. A source whose briefs another job writes is `OnDemand`: a first turn asks it and waits a bounded time, so the Slack extraction reads a wanted conversation at its next pass. The queue exists whenever briefs are on, whichever plugins are enabled; its own loop runs only when an enabled plugin's units are briefed by its calls and an agent answers, so a setup without one still briefs on-demand units. The Slack extraction writes its brief in the call it already makes, one run per unit a day. The brief's prompt, taxonomy and extra fields are the profile's brief intent of the unit's provider, found by `builds`: the queue, the facts and a source that asks the fields itself (`IntentUser`) each read the builder of the provider whose units they brief; Go owns the core schema, composes it with the intent's, and validates every structured answer (brief, extraction, `classify`) against the schema that asked for it, with an in-house JSON Schema subset.
- **Consequence**: `turns.brief_run` points at a run of any provider; a migrated store keeps every pull request brief at its version, so nothing is briefed again on upgrade; a new extra field is filled without briefing the core again.
- **Enforced by**: `TestTheGitHubBriefVersionIsPinned`, `TestGitHubSourceStaleIsTheQueuesRule`, `TestAMigratedPRBriefIsNotDueAgain`, `TestAModelAnswerOutsideTheComposedSchemaIsRefused`, `TestASlackOnlyDaemonBriefsSlackUnitsOnDemand`, `TestEachProviderReadsItsOwnBriefIntent`.

## The proposal rules are default workflows

- **Context**: a Go job proposed the Slack nudges, closures and owner questions, with its rules, holds, withdrawals and texts in Go, so none of it could be read in the plan, tuned per workflow or rewritten in the profile.
- **Decision**: three default workflows replace those rules: `slack-stalled-nudge` and `slack-closure` over GTD entries and `slack-ask-owner` over Slack units, each one `draft.propose` step with its text in `text.md.tmpl`. A step's `repeat_after` names a duration or a duration of the config (`config.drafts.repeat_after`); a run that leaves a person inside a `repeat_after` that ends with time stays pending, so that person's draft follows when it ends, as the job's did. The store's v4 migration gives every draft of the job's rules its workflow's identity, rule and workflow renamed and key kept, so the workflows space out and withdraw them. Their approval is `workflows.<name>.drafts.approval`. GitHub's nudge stays a plugin rule run by the proposals job, its approval `github.drafts.approval`.
- **Consequence**: the workflows' drafts equal the job's, text included, over a seeded fortnight; the engine's global pause now pauses them, which the job ignored; a question on a Slack unit no live entry tracks any more is withdrawn as moot, where the job kept it.
- **Enforced by**: `TestTheDefaultWorkflowsReproduceTheJobsDrafts`, `TestACutOverReplayProposesNothingNewOnDayOne`, `TestV4RewritesTheJobsDraftsToTheirWorkflows`, `TestDraftTextComesFromTheProfile`, `TestOnlyTheDraftStepProposes`. See [Tune workflows](/guides/workflows/#tune-the-slack-proposals).

## A provider is asked what it can do

- **Context**: a branch on a provider's id outside its plugin ties the daemon, the loader and the clients to that plugin, so a new plugin needs edits all over.
- **Decision**: a plugin's module declares its capabilities (`Checkouts`, `Briefs`, `Links`, `Detail`), served to clients as `api.Plugin.Capabilities`; a unit's codec compares keys (`Equal`) names a unit by its address (`URL`, `KeyOf`) and says how a command line names one (`Target`). The default provider of a manifest without one is the profile's `defaults.toml`, and every bundled intent names its own, so a layer's default fills only the manifests that name none. Which intents get a turn skill is the manifest's `skill`. A user intent taking a bundled intent's role takes its shortcut and everything the role implies.
- **Consequence**: a provider id appears only in its plugin, the vocabulary tables, the unit package, the core-kept providers' codecs and the composition root, and an adapter spells only its own provider's id; the rest is debt listed by declaration with its count and reason, and clients name no plugin. Asking the GitHub plugin's package whether a unit is a pull request (`pr.RefOf`, `pr.Codec{}.Provider()`, `port.PROf`, the daemon's `isPullRequest`) is listed the same way.
- **Enforced by**: `TestTheCoreNamesNoProvider`, `TestProviderIdentityThroughAPluginPackageOnlyShrinks`, `TestClientsNameNoPlugin`, `TestCockpitScriptsNameNoPlugin`.

## One grammar for the profile language

- **Context**: reserved words spread over decoders, per-key checks, hand-written JSON schemas and reference pages let one word mean different things in different places, let a key be documented and not read or read and not documented, and let a typo load silently.
- **Decision**: one Go table, `builtin.Grammar` (`internal/intent/builtin/grammar*.go`), is the only source of every reserved key of an intent and a workflow manifest, a step's arguments, `actions.toml`, `defaults.toml`, `labels.toml` and `config.json`: its path, kind, values, default and one-line doc. `builtin.Check`, run by every loader, judgment blocks' front matter included, validates kinds, enums and defaults against it; a decoder that checks a value itself takes the value set from the table (`builtin.ValuesOf`) and words the refusal as the table does. An unknown key or value is refused with a suggestion only when a close one exists (`expr.Closest`, the one similarity every surface uses), and never with a mode that sends or runs with no person approving. There are no aliases: a removed name is unknown like any other. `profile schema` writes the JSON schemas from it, and `make docs-ref` writes the reference pages, the [profile language](/reference/language/) among them, one line per reserved word. Core words are neutral and name no provider; a plugin's own words carry its slot's prefix (`pr.review`, `pr.reviewed`) and its configuration lives under its id (`github.*`, `slack.*`). Every intent, workflow and built-in action says what it does in a required `summary`, and the words of the built-in actions are the profile's `actions.toml`.
- **Consequence**: a word means one thing wherever it appears (`mode` only in an approval table, `observe` for a workflow that plans only, `dry_run` for a draft that is never sent, `runner` for how a turn runs, `stage` for the pass over a unit); a pull request fact has one name and one spelling in every slot (`pr.repo` is owner/repo, `pr.repo_name` the bare name, `pr.state` GitHub's `OPEN`, `CLOSED` or `MERGED`); a new key is a row with its doc, and its schema and reference follow from `make docs-ref`; an old configuration or manifest fails at load, naming each unknown key.
- **Enforced by**: `TestCheckRefusesAnUnknownKey`, `TestCheckRefusesAnUnknownValue`, `TestCheckReadsKinds`, `TestGrammarDefaultsAreValid`, `TestEveryConfigKeyIsInTheGrammar`, `TestTheGrammarRefusesTheOldUserConfig`, `TestBundledProfileLoadsClean`, `TestManifestKeysAreInTheGrammar`, `TestGrammarDocIsCurrent`, `TestTheDecodersCheckValuesInTheTablesWords`, `TestNoGuessReleasesADraftOrARun`, `TestClosestGuessesNothingForAnOldName`, `TestPRFactsAreSpelledAlikeInEverySlot`. See [Profile first](/architecture/profile-first/).

## An answer reaches the agent as a notice it pulls

- **Context**: an answer was stored by turn with no link to the Inbox message it answered, whether it answered a posted message was guessed from timing, and the agent got it only as text typed into its session, which Claude Desktop delivers as another program speaking for the user and its classifier may refuse. Two answered posts read the same, and nothing told the user whether the agent read an answer.
- **Decision**: the Inbox message is the conversation's unit. Every answer stores the message it answers (`turn_answers.message_id`) and when the agent read it (`read_at`); whether it answers a post is the message's origin. The session gets only a notice naming the message, typed when it is idle or as the reason of its blocked stop while it works, and the agent pulls the answer with `inbox_read` (or `turn answer --message`), scoped by its session, which marks it read. The notice's words are the profile's `notices.toml`. The v6 migration links each answered message to the newest answer of its unit and turn stored no later than it closed, and reads every answer but a queued one.
- **Consequence**: no answer text is typed into a session; the cockpit shows whether the agent read each answer, and `session.unread_answers` lets a workflow notify; an answer stored before v6 that no message matched keeps message 0 and, like every answer the loop had delivered, counts as read; `messages.answer` stays as the Inbox's line, since a proposal's answer has no turn.
- **Enforced by**: `TestVersion6LinksEachAnswerToItsMessage`, `TestAnAnswerWhileTheSessionWorksBlocksItsStopWithTheNotice`, `TestReadTellsTwoPostsAnsweredInReverseOrderApart`, `TestReadIsScopedByTheCallersSession`, `TestInboxReadReturnsTheUnreadAnswersAndMarksThemRead`, `TestTheProfileWordsEveryNoticeTheCoreSends`.
