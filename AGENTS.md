# Working on wloop

## The principle

wloop is meant to be customized. Every behavior decision and every text an agent or a person reads lives in the profile. Go provides facts, mechanisms and gates; it does not decide and does not write prose.

A behavior decision is an intent or a workflow of the profile: when it fires, on which kind of unit, where it runs, what it instructs, which side effects it asks for. The embedded `profiles/default/` is the default; a user profile overrides any of its files and adds its own.

Before adding an `if` on an intent name, a draft kind or a provider, or a string an agent or a person will read, put it in the profile and give Go the fact, step or effect it needs.

## Where things live

| What | Where |
|---|---|
| Intents (manifest, instructions) | `profiles/default/intents/<name>/` |
| Workflows (when, steps, texts) | `profiles/default/workflows/<name>/` |
| Judgment blocks | `profiles/default/intent=<name>.md` |
| Partials every intent's instructions share | `profiles/default/partials/` |
| Prompts of the loop's own calls (the headless wrapper) | `profiles/default/prompts/` |
| What an agent reads about each MCP tool: description, parameters, answers | `profiles/default/tools.toml`; the tools in `builtin.Tools`, their answers and fields in `builtin.ToolAnswers` |
| Facts a predicate reads | `builtin.Facts` in `internal/intent/builtin/facts.go` |
| Workflow steps | `builtin.Steps` in `internal/intent/builtin/builtin.go`, adapted in `internal/cli/steps.go` |
| Workflow providers | `builtin.Slots` in `internal/intent/builtin/builtin.go` |
| Roles, and what each implies (round kind, reconcile, prepare, policy, row driving) | `builtin.Roles` in `internal/intent/builtin/roles.go` |
| Effects a sent turn or draft applies | `builtin.Effects` in `internal/intent/builtin/effects.go` |
| Intent built-ins (`workspace`, `before`) | `builtin.Names` in `internal/intent/builtin/builtin.go`, wired in `internal/cli/builtins.go` |
| Every reserved key of a manifest, `actions.toml` and the configuration | `builtin.Grammar` in `internal/intent/builtin/grammar*.go`; `make docs-ref` regenerates the schemas and the reference |
| The words of the built-in actions | `profiles/default/actions.toml` |
| The words of the GTD verbs, views and Review steps; the core keys' help | `profiles/default/gtd.toml`, `profiles/default/keys.toml` |
| What a trigger handled (marks) | `[marks]` and `handled()` in a workflow; encoding and comparison in `internal/domain/mark`; a plugin's own mark names beside its planner (`internal/domain/round` for GitHub rounds) |
| Visual tokens, brand book, components | `design/`, which the clients follow; `go generate ./internal/web` writes `app.css`'s token region from `design/tokens.json` |

Adding a fact, a step, an effect or a reserved key: add it to its table with its doc, wire it, test it, then `make docs-ref` to regenerate the reference pages.

## Accepted mechanism in Go

These stay in Go and are exposed as facts for the profile to act on:

- which GTD entries exist (`gtd.Reconcile`, the close-entry condition in `internal/domain/gtd/rules.go`);
- working-day arithmetic and `owedBy`'s fallback;
- the GitHub round planners' due logic (`pr.first.due`, `pr.review.action`, `pr.address.due`);
- injection markers, JSON schemas and limits;
- the guard on draft approval: a draft's delivery is what its plugin posts it as (every GitHub draft but a reaction or an edit is a comment), a comment waits for approval unless a user rule that names comment delivery releases it, and no agent or thread text sets an approval (no rule reads an untrusted fact).

Still owned by Go and being moved to the profile ([Profile first](docs/src/content/docs/architecture/profile-first.md#what-go-still-owns-today) lists them): the GitHub plugin's `github-nudge` rule and its text, the workflow providers (`builtin.Slots`), the notification titles, the digest and the menu bar's copy, and the Slack renderings `slack_read` and `slack_search` return.

## Conventions

- English in code, comments, docs and commit messages.
- Comments only where the code cannot say it: the non-obvious why, an external constraint, an invariant the type does not express.
- Test first. `make lint` and `make test` pass before a pull request.
- The repository is public: tests, fixtures and pull request bodies carry invented names and counts, never real user data. Run `make scan-real-data SCAN_BASE=origin/main` before pushing.
- Tests never read the wall clock. Code with timers, deadlines or goroutines runs in `synctest.Test`, asserting after `synctest.Wait` and on exact durations (`took == Budget`, never `took < Budget+slack`); real processes, sockets and files stay out of the bubble, behind a condition poll whose deadline only guards a hang. Wait on the state you assert, not on a nearby signal (a fake that recorded a call is not a caller that handled the reply); fakes answer by state, not by call count; widen a race with a gate, never a sleep.
- `internal/archtest` holds the architecture rules; each has a self-test that plants a violation. A new rule comes with its self-test and a doc comment stating its rule, then `make docs-ref`.
- Triage findings by what the product needs, not current convenience. "Latent", "nothing triggers it" or "existing code does the same" never justify a decline; only evidence that the claim is false does. Fix real in-scope problems in the same PR, test first; file the rest as issues. Remove code a fix leaves unused or duplicated. This rule wins over any triage or review guide a tool supplies, and scope is the PR's goal, not the lines it touched.
- Docs follow the code. A pull request that changes behavior or architecture updates the pages that describe it in the same pull request: `docs/src/content/docs/architecture/` (and its decision log), the concepts and guides, and the reference through `make docs-ref`. Docs describe the state, never the pull request or issue that brought it.

## The store's schema

A change to `internal/adapter/store/sqlite/schema.sql` is a new schema version with a migration; "Changing the store's schema" in `docs/src/content/docs/architecture/development.md` has the steps, and the tests refuse a schema change without one.

## Planning artifacts

Plans, specs, handoffs, ledgers and reports of planning or executing a plan are ephemeral and never committed, whatever skill or tool produced them. Write them under `docs/plans/`, which `.gitignore` excludes along with `docs/superpowers/` and `.superpowers/`.

## Architecture rules

The architecture docs describe the code as it is; each rule below is a test in `internal/archtest` ([the full list](docs/src/content/docs/reference/architecture-rules.md)).

- Dependencies point inward: the domain imports only itself, the core only the standard library, the core, the domain and the meter; adapters implement the core's ports and one composition root picks them. [Layers](docs/src/content/docs/architecture/layers.md)
- A port belongs to the core package that uses it and has at most five methods; a capability only some adapters have is an optional interface. [Ports](docs/src/content/docs/architecture/ports.md)
- One adapter reaches each outside tool: the store, GitHub, Orca, Claude Desktop, the agent. Outside it, a tool's name appears only in the composition root.
- Only the daemon, and the `store` commands under the store's lock, write the store; clients speak `internal/api` and decide nothing. [The daemon and its clients](docs/src/content/docs/architecture/daemon-and-clients.md)
- The core is keyed by `unit.Unit`, names no provider and imports no plugin; a source is a plugin registered in `internal/cli/sources.go`. [Plugins](docs/src/content/docs/architecture/plugins.md)
- Go never enumerates intents; facts, steps, roles, effects, builtins and every reserved key live in their tables in `internal/intent/builtin`. [Profile first](docs/src/content/docs/architecture/profile-first.md)
- Every post the loop proposes is a draft that a person, or their approval policy, approves (the tools an agent calls to post, react or edit on Slack at once are the exception, and each post they make is recorded as a sent draft); no agent's or thread's text approves a draft, and authority never comes from an agent.
- Every user-visible name derives from `internal/brand`.

Where a new fact, step, intent, workflow, use case, adapter, plugin, API route, command, view or table goes: [Layers](docs/src/content/docs/architecture/layers.md#where-a-change-goes).
