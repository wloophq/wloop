---
title: Plugins
description: How a source of units plugs into the core, what it contributes, and the boundary that keeps the core free of it.
---

The core of {{name}} is a task system over opaque units: tasks, the Inbox, the Outbox, projects, the review, the turns, the workflows and the read model work on `unit.Unit{Provider, Key}` and never on a pull request or a Slack thread. Where units come from is a plugin's business. The bundled plugins, `github` and `slack`, are compiled in for now; the daemon also runs plugins as their own processes, through the plugin host below.

## A plugin

A plugin is a `source.Plugin{ID, Name, Codec, Build}` listed in `internal/cli/sources.go`, the one file that names a source. Its codec mints and parses the keys of its units and stays registered when the plugin is disabled, so stored units still render. `Build` returns the plugin's `source.Module` from the configuration and the shared handles (`source.Deps`).

A module must have its codec, an ingestion job with an interval and its rows; a module that derives items must have a card. A module that fails to build, or whose keys clash with the core's, is left out with its error reported, and the daemon runs without it. Everything else is optional, and the core does without what a plugin leaves out:

| A plugin contributes | Through `Module` | What the core does with it |
|---|---|---|
| Units and their changes | `Ingest` (an `ingest.Job` over its `port.Source` and `port.SourceStore`), `Slow`, `Run` (a long-running stream, restarted after 30 s), `Jobs` | runs them as daemon jobs; a source never writes, ingest saves through the source's store |
| Rows | `Rows`, a read model provider | serves them to every surface with the rest of the read model |
| Items and cards | `Items` (deterministic, no I/O), `Card`, `Identity`, `UnitEvents` | turns items into tasks; renders the card on every surface; resolves linked items from unit events |
| Workflow facts | `Facts`, an `automation.Units` | the engine evaluates workflows over the plugin's units; a fact that names a signal, such as `pr.head`, is what a workflow's mark records; `<slot>.ended` (`builtin.EndedFact`), such as `pr.ended` or `slack.ended`, is the plugin's word that a unit has ended, which the tasks it is attached to read as `task.link_ended` |
| Marks | its planner's own mark names, written through the store | stores them per unit, retains them with the unit, and lets `handled()` read them |
| What a turn reads about a unit | `Payload`, a `turn.PayloadReader` (GitHub's: `ghsource.TurnPayloads`, which also completes a pull request a caller hands the turn and names a probe's) | renders it into the turn's instructions: the argument, the words, the facts and the contents the plugin answers |
| Where its work runs | `PlacementDefaults`, `PlacementHints` | the placement service decides the repository and checkout |
| Publishing | `Publisher`, a `port.Effects` | sends an approved draft once, with no automatic retry; `Verify` settles a draft a crash left in `sending` |
| Following units | `Track`, `Untrack` | `{{command}} track` and untrack go through it; GitHub's admit a pull request while fewer than 50 are tracked and it is open |
| UI | `UI` (facets, saved views, notification templates, keys, menu bar sections, terminal views), `WebAssets` served under `/plugins/<id>/`, `CLI` | renders the declarative parts on every surface; mounts the plugin's own web module and terminal views |
| Health | `Health` | shows it with the daemon's health |
| Its checkouts | `Checkouts` | a session on its unit runs in the unit's own checkout, with no placer asked |
| Its briefs | `Briefs`, a `brief.BriefSource`: a source plugin's `Briefer` calls | the brief queue reads the unit's revision and staleness through it; the revision's `Attempt` names what an attempt is made from, and `attempt_of` stores the version and that attempt (`brief.AttemptKey`), as version 6 stored it. A brief another job writes reaches the queue through the broker's `PutBrief`, which keeps it as the unit's brief and wakes the turn waiting on it; a compiled-in module's job tells the queue in process |
| Links to other plugins' units | `Links`, a `LinkResolver` | resolves a link of its units to the unit another plugin names |
| A detail page | `Detail` | a client opens the unit's detail in the cockpit, in place of its link |
| Addresses a person types | `Resolve` | the daemon answers `GET /api/resolve` with the unit an address lands in, such as a Slack message ref or permalink in its direct message segment or thread; a command line's target asks it |

`Module.Capabilities()` names what a plugin can do (`checkouts`, `briefs`, `publishes`, `reacts`, `items`, `detail`), served in `api.Plugin.Capabilities`. The daemon and the clients ask a plugin what it can do, never which one it is (`api.PluginCan`). A unit's codec says the rest: `Equal` compares two keys of its provider, such as GitHub's without case, for units and person keys alike (`unit.Registry.Same`, `items.Identity.Codecs`); `URL` and `KeyOf` name a unit by its web address, so a unit with one is typed by its address (`unit.Registry.ParseURL`); `Target` is how a command line names one of its units, such as `<url>`, in an intent's usage line (`unit.Registry.Target`).

The UI comes in three tiers: the card, which every surface renders the same way and which is kept as a stored snapshot; the declarative contributions the core renders; and the plugin's own views. Keys follow one map: the core owns every lowercase letter and digit, and a plugin or an intent gets one uppercase letter (`internal/keys`).

## A disabled plugin

`[plugins.<id>] enabled = false` (`plugins.github.enabled`, `plugins.slack.enabled`) leaves the module unbuilt: no ingestion, rows, jobs, facts, items, actions, notifications, MCP tools or publisher. Its codec stays, cards render from the stored snapshot, and every write to one of its units answers `409 {"error": "<id> is disabled"}` before the handler runs. On a task, Dismiss still works: it detaches the disabled plugin's units and their items, and an item's link to such a unit is stored and left out while the plugin is off ([Tasks and views](/concepts/tasks/)). The core works with no plugin enabled.

A disabled plugin's vocabulary still merges and its codec still parses its keys, so a configuration or a profile that names its facts, keys or units loads as it does while it is on; its profile layer does not load and its process does not run.

## The vocabulary

`internal/intent/builtin` holds the core's rows only: no fact, effect, role, step provider, slot, payload root, event kind, row kind, delivery or task kind of a plugin (`TestBuiltinHasNoProviderRows`), and it imports no plugin's domain. Each plugin's rows come from the `[source]` table of its `plugin.toml`: `builtin.Merge` maps every source table onto the tables, refusing one that takes a core slot, name or provider, or whose role names a mechanism the host does not have, and `builtin.Install` makes the result the tables. The bundled plugins' manifests live in `plugins/<id>/plugin.toml` and `internal/plugins/bundled` installs their vocabulary when it initializes; the command's `main` installs the bundled and the found plugins' vocabulary once, before the configuration and the profile load. A fact of the manifest is a workflow fact of the plugin's slot and, with `slots`, an intent fact its intents alone read. The task kind values a manifest declares join `tasks.toml`'s `kind`. `make docs-ref` reads the bundled manifests.

The codec of every plugin comes from its manifest (`internal/plugins/codec`), for an enabled plugin and a disabled one alike, and `Unit.Label` asks it how a person reads a key.

## The plugin host

`internal/plugins/host` runs plugin processes of any kind, and one adapter per kind connects a started process to the daemon (`internal/plugins/source` for sources):

- **Discovery.** A plugin is a directory with its `plugin.toml`, its binary (`<command>-plugin-<id>`) and its profile layer (`profile/`), in `<config>/plugins/<id>/` or in the bundled directory beside the daemon's binary, `plugins/<id>/`; a user's directory overrides a bundled one of the same id.
- **Trust.** A bundled plugin runs only with the SHA-256 its install recorded (`<state>/plugins/checksums`, which go-plugin checks); a bundled plugin with no record is a development build, run by its path and logged as such. Any other binary runs only with `[plugins.<id>] trust = "path"`, which only you write; the daemon's log names the path of each plugin it runs and how it is trusted.
- **Start.** `serve` starts every enabled plugin it trusts, each the leader of a process group the daemon lists (`procgroup`); `serve --no-jobs` starts none. The source adapter serves the broker to the plugin and starts its kind; until it runs, its units keep their last snapshot, marked stale.
- **Loading.** A plugin the daemon starts is loading until its `Health` first says it serves: the host asks it every second after it starts. Until then `GET /api/rows` and `GET /api/tasks` name it in `loading`, with the reason its health gives (or `starting` before it runs). A later crash does not make it loading again.
- **Restart.** A plugin whose process ends is restarted after a backoff that doubles from a second to five minutes, and starts again from a second after a run longer than that; while it is down its units are stale: their rows and cards render from the last snapshot, marked `stale`.
- **Notification.** The host tells each running plugin of the changes to the host topics its manifest's `notify` lists (`Notify(units, topics)`), one change-log note at a time: with the plugin's own units the note names, with none for a note of every unit or of no unit, and not at all for a note naming only other providers' units. Each call is cut after ten seconds, so a plugin that hangs delays the others by no more.
- **Shutdown.** When the daemon stops, each plugin's kinds get `Stop`, then its process is killed and go-plugin's clients cleaned up. There is no reattach.

## Snapshots and overlays

A source plugin reports each unit as a snapshot (`Reporter.Report`), which replaces the unit's last one whole. The host keeps it in the store, as JSON in `unit_snapshots` beside the plugin's `rev` for it, and drops it when the plugin reports the unit gone. The host reads snapshots only; on the hot path it never calls a plugin:

- **Rows.** `serve` serves the units of every plugin it runs and no built module serves through `readmodel.Reported`, with the views, facets and situations its manifest declares. `readmodel.Reported` derives a unit's row from its snapshot (its situation, card, views, facets, items and data) under the host's own state, which `internal/readmodel/overlay` lays over it and which names no source: the run of the newest turn whose role drives the row (the loop working, waiting on you or stopped, with its question), your mute (by the situation's kind and who it waits on, or until a time), and a pending round. An open intake shows in the row's line. The row offers the intents' actions, then each action the snapshot offers. The variant without the mute is an overlay too (`readmodel.Unmuted`). A unit's card is its plugin's, and when an overlay set the row's situation the card says why and on whom as the row does. A turn that ends, a mute or a round changes the row with no new report.
- **Changes.** `Report` notes the unit data of the units it wrote in the change log. `Changed(units, topics)` notes the plugin's own units as changed in ways no snapshot carries, as unit data and under the host topics behind `topics`, so the read model and the sweep read them again.
- **Facts.** `psource.UnitFacts` serves the automation engine each unit's reported facts and its rev. A fact the plugin reports as a temporal value (`Since`, `At`, `After`, in calendar or working days) is a `workflow.Temporal`, which the engine resolves with its clock whenever it reads it; one whose time is zero resolves to its type's zero. The engine's `Due` is the earliest deadline among the temporal facts its last sweep read, an `After` in working days counting forward with `workday.Add`. The engine reads a unit's rev before its facts and drops a run whose unit was reported again before it ran (`automation.Revisioner`). The GitHub module, still built into the daemon, gives each pull request a rev that grows with every change to its stored data the change log notes, and asks GitHub nothing before a run.

Who the viewer is in a source, and their teams, is what its plugin's `Presenter.Identity` says; the read model's base holds both per provider (`readmodel.Base.Identify`), and the daemon serves the forge's from its `Deps.Identity`. The unit a checkout holds, which a turn by hand runs on when it names none, is what a source's `Ingester.Locate` says of the directory: the built-in modules' locators first, then each source plugin's.

A plugin's profile layer loads while the plugin is enabled: the plugin's `profile/`, after the bundled profile, then your `plugins/<id>/` beside your profile's own files, after it (`profile.Chain`).

`pkg/plugin/store` is a plugin's own SQLite store in its state directory, `<state>/plugins/<id>/`: it applies the plugin's migrations and imports the `import.db` the host exported for it once.

## The boundary

The core never imports a plugin package, and no plugin imports another. The core here is `internal/core`, `internal/domain`, `internal/readmodel`, `internal/api`, `internal/web`, `internal/client`, `internal/catalog`, `internal/keys`, `internal/daemon`, `internal/turn` and `internal/brief`; each plugin owns its packages:

| Plugin | Packages |
|---|---|
| `github` | `internal/adapter/source/github`, `internal/adapter/facts/github`, `internal/readmodel/prrow`, `internal/situation`, `internal/domain/github`, `internal/domain/pr`, `internal/domain/inventory`, `internal/github` |
| `slack` | `internal/adapter/source/slack`, `internal/domain/slack`, `internal/slack` |

Test imports count too, except a plugin's own test fake: a core test builds its units as `unit.Unit` values, and a test that exercises a plugin's behavior lives with the plugin, such as the read model's tests over GitHub rows in `internal/readmodel/prrow`. No client names a plugin either: the cockpit's scripts and page quote no plugin's id, and the terminal and the menu bar spell no plugin's id or provider constant (`TestClientsNameNoPlugin`); they read the provider and its capabilities from the data the daemon serves. Outside the core, a provider id appears only in its plugin's packages, the vocabulary tables of `internal/intent/builtin`, `internal/domain/unit`, the codecs of the providers the core keeps, `internal/cli/root.go`, `sources.go` and `slack*.go`, and an adapter spells only its own provider's id. The places that still name one are listed in `coreProviderAllowlist` (`TestTheCoreNamesNoProvider`) by file, declaration and literal, each with how many times it spells it and its reason, so a new spelling, even in a listed declaration, fails the rule; the list only shrinks. Code that tells a pull request from another unit by asking the GitHub plugin's package (`pr.RefOf`, `pr.Codec{}.Provider()`) is listed the same way in `pluginIdentityAllowlist` (`TestProviderIdentityThroughAPluginPackageOnlyShrinks`). An edge that breaks the rule is listed in `pluginDebt` in `internal/archtest/rules_plugins_test.go`, and a test debt lets only the importer's tests pass: the daemon's tests serve the GitHub plugin's rows and presenter in process, and the brief queue still briefs GitHub's pull requests in process. An entry that no longer matches an import fails the test (`TestCoreImportsNoPluginDomain`), and the daemon's own code imports no plugin package under any debt (`TestDaemonImportsNoPluginDomain`). Loop code is keyed by `unit.Unit` and names no pull request (`TestLoopCodeImportsNoPR`).

`session`, `workspace` and `entry` are providers the core keeps for its own units. They have no plugin: their facts come from the turn registry and the workspace managers, through `internal/adapter/facts/session` and `internal/adapter/facts/workspace`.

## Actions and views

The daemon reaches a plugin's source logic through its `Presenter`: `Action` and `View`, a running plugin's over the protocol, a compiled-in module's in process (`daemon.Deps.Presenters`, built with what the daemon serves it beside the broker: its read model, row settings, open intakes, turn books and viewer).

- **Actions.** A unit action the daemon does not run itself and no intent names is its plugin's, when the catalog declares it or the unit's row offers it: the row's offer and basis are checked as for any action, then `Action` runs with `api.PluginActionArgs` (the request and the unit's row without the user's mute and pending round) and answers `api.PluginActionResult` (the lines to report). GitHub's `mute` and `unmute` are such actions: they write the host's mute overrides through the broker's `SetOverride`, compare-and-set over what `RoundState` read, and record the event. A refusal is an `api.PluginError` with its status in process, and over the protocol `ErrUnfit` answers 400, `ErrNotFound` 404 and `ErrUnavailable` 503.
- **Prepares.** A role's `prepare` names an action of its plugin, `action:<name>` (GitHub's nudge: `action:nudge.prepare`); its result's inputs, normalized to the intent's, fill the turn's, or its refusal refuses the turn.
- **Views.** `GET` and `POST /api/plugins/<id>/views/<view>` serve a plugin's view, its query the request's parameters or body (GitHub's `explore` and `explore.recent`). The daemon asks a unit's plugin for the views it serves of the unit: `unit.row` for a unit the read model does not list (a lapsed pull request, or one GitHub reads for a track; `kept` reads nothing from the source), `unit.detail` for the timeline and the brief of the detail, `unit.checks` for the failing checks' steps, and `timings` for the forge's timings report. A disabled plugin's unit views still answer, read only; its actions and its own views do not.
- **Bookkeeping.** A compiled-in presenter that is a `daemon.Keeper` runs on each new snapshot of the rows and after each sweep: GitHub's records the situations that changed and clears the mutes whose situation changed.
- **Hold.** The user's pause of a unit's rounds is the host's `hold` override; a plugin reads it, with the repository and global pauses, as `RoundState.Paused`, and never sets it.

## Plugin storage

The plugins' tables still sit in the core store's file, but no query names a plugin's table and a core table, and no transaction does (`TestNoQueryJoinsPluginAndCoreTables`, which reads a function literal passed to `Write` and a function taking a `*Tx` with the functions it hands the transaction to). A plugin's store reaches the host's data through the broker and writes its own tables apart, and a reader that needs both reads each apart.

| Owner | Tables |
|---|---|
| core | `round_state`, `round_days`, `round_day_prs`, `round_day_notes`, `events`, `unit_marks`, `view_applied`, `turn_instructions`, and every other table but the plugins'; of `overrides`, the mute columns (`muted_until`, `mute_until_state`, `muted_kind`, `muted_waiting_on`), `next_guidance` and the user's `hold`, which the host reads and writes |
| plugins | `pull_requests`, `pr_seen`, `pr_details`, `fetches`, the GitHub `meta` rows and the other columns of `overrides` (`taken_over`, `tracked`) for GitHub; `slack_*`, `segment_map` and `extraction_*` for Slack |

The workspace link of a pull request (its `ws_*` columns) is host data: the host writes it on the pull request's row through `inventory.SetWorkspace` until it derives the link from the workspaces' units, which the plugin reads through `Reader.Workspaces`.

- **Events and marks are host data.** The GitHub plugin's store (`inventory.Store`) records the events it observes through the broker's `Recorder.RecordEvents` before its own write, derived from a read of its rows, so a write that fails afterwards leaves the rows that derive the same events on the next save, and an ingest refetches a pull request whose row the failed write left old: the timeline kinds `opened`, `push`, `review_requested`, `review_request_removed`, `review`, `comment`, `check`, `ready`, `draft`, `merged` and `closed`, and the `followed` and `unfollowed` of a scope flip. It reads back through `History.Events` the `push` and `merged` events a fresh snapshot is compared with, the last `followed` event that dates an unfollow, and the kind of the last flip, so a flip the host already took from a write that then failed is not recorded twice. The host keeps a keyed event once per unit, kind and key, and an unkeyed one once per unit, kind and time, filling the actor and data a stored one lacks; it notes `TopicSourceEvents`.
- **Snapshot reads.** The read model's pull requests and their overrides, the brief queue's open pull requests and their brief states, and the activity feed's events and their pull requests are separate reads.
- **The extraction** writes a pass's briefs in a host transaction, then its cursors and hints in its own, so a crash between them reads the same messages again.
- **Segmenting a direct message** asks the host to move the drafts on the thread-shaped unit of each stored message the new segments hold onto its segment, through `Gates.RehomeDrafts`.
- **Retention** reads the ended pull requests, the stored ones' keys and the extraction cursors first, deletes the core's rows in one transaction, then the plugins' in another.

The built-in modules reach the broker as a plugin process does: `serve` gives the GitHub store and the Slack segment map the broker of their provider (`source.Deps.Host`); a store opened outside `serve` reaches the same host methods in its database.

## The plugin protocol

`pkg/plugin` is the SDK of plugins that run as their own processes, through `hashicorp/go-plugin` over gRPC; the daemon does not run any yet. It holds what every plugin shares, with its protocol under `proto/`:

- the handshake and the protocol versions (`VersionedPlugins`, protocol 1), negotiated when the host starts a plugin;
- the lifecycle every kind answers: `Health`, `Start`, `Stop`, and `Notify(units, topics)`, which tells a kind that host state it reads changed, each topic one of the names `plugin.Topic` declares (`rounds`, `gates`, `marks`, ...);
- `Serve`, which serves one implementation per kind from one binary, the kind being go-plugin's plugin name;
- the logger, which writes JSON lines to stderr for the host to relay; a plugin writes nothing to stdout, which carries the handshake (`TestPluginWritesNothingToStdout`);
- the exit when the parent dies: the host passes its pid (`plugin.ParentEnv`), and a plugin polls its parent process id and exits once it differs, even when the host died before the plugin served, with no reattach.

A plugin's directory holds a `plugin.toml` (`pkg/plugin/manifest`) with its `id`, the `kinds` its binary serves, the `protocol` it speaks, and one table per kind, which the kind's SDK package decodes. `pkg/plugin/plugintest` serves kinds in-process, for tests on either side. Nothing under `pkg/` imports `internal/` (`TestTheSDKImportsNoInternal`), and the host passes its product name through the environment (`plugin.BrandEnv`).

### The source kind

`pkg/plugin/source` is the SDK of the `source` kind, with its protocol in `proto/wloop/source/v1`. Its `SourceService` is the union of capability interfaces of at most five methods each (`TestEveryCapabilityHasAtMostFiveMethods`); a plugin implements `source.Source` and the ones it has, `source.New` adapts it, and every other call answers `ErrUnsupported`:

| Interface | Methods |
|---|---|
| `Ingester` | `Ingest` (one run of a manifest job), `Track` (a read, then a follow once the host admits it), `Untrack`, `Resolve`, `Locate` (the unit a checkout holds) |
| `Presenter` | `Action`, `View`, `Identity` |
| `Publisher` | `Validate`, `Where`, `Place`, `Publish`, `Verify` |
| `Lander` | `Landed`, `Checkout`, `WriteState` |
| `Briefer` | `BriefInput`, `BriefRevision`, `BriefDescribe`, `BriefStale` |
| `Planner` | `Plan(unit, roundState)`, which the host calls when it launches or skips a round and which names the rounds with the marks to write when one is sent or skipped; `Payload`; `ApplyEffect`; `Proposals`; `Placement` |
| `Tooler` | `CallTool(tool, args, session, turn)`, which names one of the tool's manifest answers; the host words it from the layered `tools.toml` |
| `CLIRunner` | `RunCLI`, which streams the command's stdout and stderr to the host as it writes them, then its exit code |

The host serves the plugin's calls back on the connection's broker (`source.ServeHost`, whose id goes in `StartRequest.HostBroker`), through `HostService`, split the same way: `Reporter` (`Report`, `Changed`), `Ledger` (`ApplyItems`, `QueryItems`), `Reader` (`Tasks`, `Books`, `RoundState`, `Workspaces`, `Session`), `Gates` (`OpenGate`, `RecordSent`, `RehomeDrafts`, `SetOverride`), `Recorder` (`RecordEvents`, `SetMarks`, `PutBrief`), `Wakes` (`SetWake`, `ClearWake`), `Services` (`Ask`, `Meter`, `Config`, `Profile`) and `History` (`Events`). No broker method takes the loop lock, which the host may hold while it calls the plugin.

A plugin proposes drafts through `OpenGate`; the host's approval policy holds or releases them. With an MCP session, the host sets the draft's origin, task and default items from the session's book and refuses effects the session's intent does not allow, and it derives every draft's delivery from its kind through the manifest. `RecordSent` takes only a live session's post through a tool the manifest marks `posts = "now"`. `SetOverride` is a compare-and-set over the unit's host overrides, which `RoundState` carries back.

In CLI mode the host runs the plugin binary for one `RunCLI` without `Start`: the plugin gets its configuration in the request, has no broker and opens no store.

A plugin reports a unit as a `Snapshot`: its data as JSON the plugin's profile layer reads, its situation (kind, who it waits on, since when, why, and when it stalls), its card, items, link signals, who it owes, offered actions, extraction health, the contexts and due date it writes, the links its source holds (which the host resolves with every manifest's codec), its facet values and views, its facts and a `rev`. The host overlays the situation and the card with its own state: a run on the unit, a mute, a pending round. A fact is a literal or a temporal value the host resolves with its clock when it reads it: `Since(t)` is a duration, `At(t)` is true from `t` on, and `After(t, threshold, unit)` is true once more than `threshold` has passed since `t`, counted in calendar time or working days. A temporal value of a zero time resolves to its type's zero and has no deadline.

Nothing the host's context carries crosses in a context: the call's priority and meter scope travel as a `Call` field of every request (`source.WithCall` on the host, `source.CallOf` in the plugin), a tool call carries its MCP session and turn, `Ask` its tier, cost cap and meter scope, `Reader.Session` gives the session's draft rules, and a payload request carries the last verdict, the planned round, the agent, why the brief is missing, the items and the task. `pkg/plugin/source/sourcetest` runs a source in-process against a host.

A source's `plugin.toml` has a `[source]` table: the codec (key and address templates, so a disabled plugin's keys still parse), the vocabulary it adds to the host's tables (slot, facts with types, effects, event kinds and the link event each resolves, roles naming host mechanisms, brief subject, row kinds, deliveries and whether each posts to people, draft kinds, link signals, task kinds, ranked situations, the core steps its units take), the jobs the host runs through `Ingest` (with an interval read from the configuration), the configuration keys it reads, the host topics it is notified of, the CLI commands, the MCP tools with their argument schemas and answers, and its views, facets, menu bar, keys and web assets. [Plugin manifest](/reference/plugin-manifest/) lists every key.

## Adding a source

1. A package under `internal/adapter/source/<id>` with its codec, its `port.Source`, its store and its rows; its domain values under `internal/domain/<id>`; a client for the service if it needs one.
2. One entry in `plugins` in `internal/cli/sources.go`.
3. Its packages under its id in `pluginRoots` in `internal/archtest/rules_plugins_test.go`.
4. Its tables in `internal/adapter/store/sqlite/schema.sql`.
5. A configuration section and its documentation.

`internal/plugins/source/fakesource`, a plugin's reports of chat threads that nothing registers, shows the minimum a reporting plugin's rows need.
