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

## Every outbound post is a draft

- **Context**: a post under your name cannot be taken back, and a retry after a crash can post twice.
- **Decision**: what {{name}} would post is a draft in the Outbox; only an approved draft reaches the plugin's publisher, once, with no automatic retry, and `Verify` settles a draft a crash left in `sending`.
- **Consequence**: approval policies decide what goes out unattended; agents never hold a source's token.
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
- **Decision**: intents, workflows and judgment blocks are profile files, and a user profile overrides any bundled one; Go provides a closed vocabulary of facts, steps, builtins and gates, and never enumerates intents.
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
- **Decision**: the turn partials live in `profiles/default/partials/` and the prompts of {{name}}'s own calls in `profiles/default/prompts/`; a file of the user's profile replaces the bundled file of its path. Go keeps the markers, the schemas and the limits around them, and checks that a record partial still states the record protocol.
- **Consequence**: a broken partial or wrapper of the user's falls back to the bundled one with a problem, so a profile edit never stops a turn from ending; the rendered turns of the bundled profile are unchanged.
- **Enforced by**: `TestProseIsEmbeddedOnlyFromProfiles`, `TestARecordPartialMissingTheProtocolLoadsTheBundledOne`, `TestTheBundledWrapperWritesTheOldPrompt`, the scenario goldens. See [Profile first](/architecture/profile-first/).

## A draft is what its proposer declares

- **Context**: what sending a draft did, and whether it was held, followed its kind, so a closing message posted by a session closed its unit and a renamed purpose lost its behavior.
- **Decision**: a draft carries a purpose, a delivery and its effects; the Outbox applies the declared effects once each draft is sent, by name, a plugin's own first, and stores when (`drafts.effects_applied_at`), so a restart applies only what is pending. An agent declares only the effects its intent's `draft_effects` lists. A Slack unit's closure is its own column (`extraction_cursors.closure_marked`), which no extraction pass writes. Workflows hold their drafts from the drafts themselves, per workflow, subject and person, and withdraw them by their `[withdraw] when`; a draft that `[withdraw] when` would withdraw at once is not proposed, so the pair never cycles.
- **Consequence**: the kind survives only as the input word of agents and the API, mapped once, and as what the API serves; the clients group drafts by purpose.
- **Enforced by**: `TestNoDraftKindComparisons`, `TestASentDraftAppliesItsEffectsOnce`, `TestEffectsAreAppliedOnceAcrossARestart`, `TestASubjectMatchingWhenAndWithdrawWhenIsProposedOnce`. See [The Outbox](/concepts/approval/#the-outbox).

## Intents by role

- **Context**: Go started the review, address, nudge and probe intents and read their history by name, so a renamed or replaced intent lost every automatic behavior.
- **Decision**: a manifest's `role` (`round.review`, `round.address`, `nudge`, `probe`, `headless.default`) is what Go resolves an intent by; one of the user's holding a bundled role takes it over. Each turn stores its intent's role, and history readers read by it. How a round behaves comes from manifest keys (`round_kind`, `cap`, `reason_fact`, `refuse_when`, `reason`).
- **Consequence**: an intent `my-review` holding `round.review` gets the sweep's review rounds and their history; the turns stored before roles exist were given theirs by the store's migration from the bundled names.
- **Enforced by**: `TestNoIntentEnumerations`.

## turn.start on any unit, under a budget

- **Context**: a workflow could start turns only on pull requests, whose rounds have their own caps.
- **Decision**: `turn.start` starts any intent on any unit, headless or in a session, in the repository its manifest's workspace table names; outside the round providers each workflow needs `limits.per_day`, and `turns.automated_per_day` caps all of them together per local day. A repository named by an input is never taken by a workflow.
- **Consequence**: a request in a thread can start an implementing turn in a known repository; no thread text chooses the repository, and a runaway workflow stops at its budget.
- **Enforced by**: the loader (`limits.per_day`) and the step's guards.

## One brief per unit

- **Context**: the brief was keyed by pull request, so a Slack conversation had only a free-text summary that no fact, block or filter could read.
- **Decision**: briefs, their tags and their runs are keyed by unit. `brief_runs` is provider-neutral: what the unit was at is `revision`, in the provider's format, and its provider's parts of the input are `input`. The brief queue answers by unit: a provider's `BriefSource` says what a unit is at, reads its input, and decides when a brief is stale and what an attempt was made from; GitHub's rules are the ones the queue had. A source whose briefs another job writes is `OnDemand`: a first turn asks it and waits a bounded time, so the Slack extraction reads a wanted conversation at its next pass. The Slack extraction writes its brief in the call it already makes, one run per unit a day. The brief's prompt, taxonomy and extra fields are the profile's brief intent, found by `builds`; Go owns the core schema, composes it with the intent's, and validates every structured answer (brief, extraction, `classify`) against the schema that asked for it, with an in-house JSON Schema subset.
- **Consequence**: `turns.brief_run` points at a run of any provider; a migrated store keeps every pull request brief at its version, so nothing is briefed again on upgrade; a new extra field is filled without briefing the core again.
- **Enforced by**: `TestTheGitHubBriefVersionIsPinned`, `TestGitHubSourceStaleIsTheQueuesRule`, `TestAMigratedPRBriefIsNotDueAgain`, `TestAModelAnswerOutsideTheComposedSchemaIsRefused`.
