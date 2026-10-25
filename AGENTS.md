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
| Facts a predicate reads | `builtin.Facts` in `internal/intent/builtin/facts.go` |
| Workflow steps | `builtin.Steps` in `internal/intent/builtin/builtin.go`, adapted in `internal/cli/steps.go` |
| Workflow subjects | `builtin.Slots` in `internal/intent/builtin/builtin.go` |
| Intent built-ins (`on_sent`, `prepare`, `workspace`, ...) | `builtin.Names` in `internal/intent/builtin/builtin.go`, wired in `internal/cli/builtins.go` |

Adding a fact, a step or an effect: add it to its table with its doc, wire it, test it, then `make docs-ref` to regenerate the reference pages.

## Accepted mechanism in Go

These stay in Go and are exposed as facts for the profile to act on:

- which GTD entries exist (`gtd.Reconcile`, the close-entry condition in `internal/domain/gtd/rules.go`);
- working-day arithmetic and `owedBy`'s fallback;
- the GitHub round planners' due logic (`pr.first.due`, `pr.review.action`, `pr.address.due`);
- injection markers, JSON schemas and limits;
- the guard that never sends a GitHub comment under the user's name without approval.

Still owned by Go and being moved to the profile (see `internal/archtest`'s shrinking allowlists): the turn partials, the headless wrapper, the Slack extraction prompt, the brief prompt and taxonomy, and the Slack proposal rules in `internal/core/proposals`.

## Conventions

- English in code, comments, docs and commit messages.
- Comments only where the code cannot say it: the non-obvious why, an external constraint, an invariant the type does not express.
- Test first. `make lint` and `make test` pass before a pull request.
- The repository is public: tests, fixtures and pull request bodies carry invented names and counts, never real user data. Run `make scan-real-data SCAN_BASE=origin/main` before pushing.
- Tests never read the wall clock. Code with timers, deadlines or goroutines runs in `synctest.Test`, asserting after `synctest.Wait` and on exact durations (`took == Budget`, never `took < Budget+slack`); real processes, sockets and files stay out of the bubble, behind a condition poll whose deadline only guards a hang. Wait on the state you assert, not on a nearby signal (a fake that recorded a call is not a caller that handled the reply); fakes answer by state, not by call count; widen a race with a gate, never a sleep.
- `internal/archtest` holds the architecture rules; each has a self-test that plants a violation. A new rule comes with its self-test.
- Triage findings by what the product needs, not current convenience. "Latent", "nothing triggers it" or "existing code does the same" never justify a decline; only evidence that the claim is false does. Fix real in-scope problems in the same PR, test first; file the rest as issues. Remove code a fix leaves unused or duplicated.

## The store's schema

A change to `internal/adapter/store/sqlite/schema.sql` is a new schema version with a migration; "Changing the store's schema" in `docs/src/content/docs/architecture/development.md` has the steps, and the tests refuse a schema change without one.

## Planning artifacts

Plans, specs, handoffs, ledgers and reports of planning or executing a plan are ephemeral and never committed, whatever skill or tool produced them. Write them under `docs/plans/`, which `.gitignore` excludes along with `docs/superpowers/` and `.superpowers/`.
