---
title: Testing
description: Fakes and contracts, goldens, budgets, the brand build and the architecture tests that keep the boundaries.
---

`make test` runs `go test -race ./...`; `make lint` runs gofmt and golangci-lint. Both pass before a pull request. CI splits the tests into shards that `scripts/test-shard.sh` names; a new package falls in the `rest` shard, and `scripts/test-shard.sh check` fails when the test and brand matrices of `ci.yml` and `cache.yml` drift from the script.

## Fakes and contracts

The core is tested without a network, a process or a real tool: the architecture tests keep `os/exec`, `net/http` and `database/sql` out of it. Each port's fake lives in the core package's `<name>test` package (`turnstest`, `gtdtest`, `messagestest`, `viewstest`, `workspacestest`, `automationtest`, `intaketest`, `ingesttest`) beside the contract suite the fake and the real adapter both pass. A real adapter runs its contract behind the `contract` build tag. CI compiles the `contract` and `live` tests on every pull request with `make vet-tags`, without running them.

Test-only adapters prove that each seam holds a second implementation: the `echo` agent (`TestEchoAgentRunsWithoutClaudeOrOrca` runs install, a review, the hooks and `turn end` with no Claude or Orca token anywhere), the `testsink` view sink, the fake workspace manager and the `fakethread` provider.

## Time

Tests never read the wall clock. Code with timers, deadlines or goroutines runs in `synctest.Test` and asserts after `synctest.Wait`, on exact durations; real processes, sockets and files stay outside the bubble, behind a condition poll whose deadline only guards a hang. A test waits on the state it asserts, and a fake answers by state rather than by call count.

A test that asserts something did not happen while a caller waits checks it once the caller is parked, never after a sleep. The loop mutex and the brief queue's state lock are `internal/core/lock` mutexes, whose waiters block on a channel, so `synctest.Wait` returns only once the caller is parked on them. A wait no bubble can see shows itself instead: a Slack token refresh waiting on another process's file lock logs `waiting for another refresh`, and a backfill waiting for the inbox reports the objects it waits on in the Slack health's `backfill.draining`.

Every goroutine a test starts, or makes the code under test start, is cancelled and awaited before the test's stores and directories go away. The code that starts one gives its owner a way to wait for it: the daemon's `Run` waits for the work it drives, and `serve` waits for its jobs, which run on the `Env`'s `background`, before it closes the store. The tests of `internal/cli` start those jobs through the harness's `testEnv.serveDeps`, whose cleanup cancels and waits for them; `TestServeRunsThroughTheHarness` keeps them there, and `TestStopServingWaitsForServesJobs` fails on a job `serve` starts without tracking it.

## Goldens

- The wire goldens in `internal/daemon/testdata/wire` (rows, actions, specs, detail, health) are read by the daemon's tests and by each client's, so server and clients agree on the bytes.
- The rendered turn instructions of every scenario in `internal/turn` and the terminal views in `internal/cli/testdata` are goldens.
- Goldens that spell the product name go through `internal/brand/brandtest`, so they hold under any brand.

## Budgets and the change log

`internal/meter/metertest` asserts what a unit of work cost (queries, spawns, GitHub calls), and `internal/adapter/store/storetest` seeds a store the size of a live one. `TestEveryWriteNotes` and `TestEveryExportedMethodIsReadOrNoted` keep every store write noted in the change log.

## The world outside

Tests never touch your home, your agent homes, your workspaces or GitHub: they run on temporary directories and fakes. Live tests (`internal/e2e`) run behind the `live` build tag, only against a sandbox repository and in isolated daemons. The repository is public, so tests, fixtures and pull request bodies carry invented names; `make scan-real-data SCAN_BASE=origin/main` checks a branch against your own deny list before you push.

## The brand

Every user-visible name comes from `internal/brand`. `make test-brand` runs the suite built with a test product name, without `-race`, which `make test` already covers; `SHARD=<name>` limits it to one shard, as CI runs it. `make scan-brand` builds with the test name and fails when anything the program writes spells the default name.

## The architecture tests

`internal/archtest` holds the rules of [Layers](/architecture/layers/), [Ports](/architecture/ports/), [Plugins](/architecture/plugins/) and [Profile first](/architecture/profile-first/). Each rule has a self-test that plants a violation and expects it caught. An allowlist entry names its file and its reason, and an entry that no longer matches fails the test, so allowlists only shrink.

A rule test is a test that does not call the `flagged` helper, which every self-test calls. Its doc comment states its rule as `// TestX checks that <rule>.`, and `TestEveryRuleTestStatesItsRule` fails on a rule test without one. [Architecture rules](/reference/architecture-rules/) lists every rule with its sentence, and each allowlist with its entries, their reasons and their triage; `make docs-ref` generates it from the sources of `internal/archtest`, and `TestRulesDocIsCurrent` fails when it drifts.

Outside `internal/archtest`, `TestEveryBuiltinNameHasAnImplementation` (`internal/cli`) checks that every name the vocabulary lists is wired, and `TestFactsDocIsCurrent` that the facts reference matches the binary (`make docs-ref` regenerates it). `TestEveryDecisionNamesATestThatExists` checks that every entry of the [decision log](/architecture/decisions/) names a test that exists, and `TestDecisionsDocIsCurrent` (`internal/archtest/cmd/decisionsdoc`) that the log's index lists its entries.

A reason that starts with `fix: ` marks an exception that does not hold and whose fix is mechanical, and `debt: ` one whose fix is a refactor; the page shows them as 🔧 and 🧾, the rest as ✅.

A new rule comes with its self-test and its doc comment; `make docs-ref` adds it to the reference page.
