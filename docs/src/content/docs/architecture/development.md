---
title: Development
description: Build and test from source, work on the procedures, and check a release on macOS.
---

## Build and test

```sh
make build        # the binary in ./bin
make test         # go test -race ./...
make lint         # gofmt and golangci-lint
make vet-tags     # compile the contract and live tests without running them
make fuzz         # fuzz the predicate compilers for 60 s each
make test-brand   # the tests, built with a test product name (SHARD=<name> for one CI shard)
make scan-brand   # check that nothing the program writes names the default product name
```

## Running your checkout

`make dev-install` builds the checkout and makes that build the installed one: it links `~/.local/bin/{{command}}` to `bin/{{command}}`, then runs `install` and `app install` with it, so the agent homes, the app and the LaunchAgents run your build. After pulling or changing the code, run it again; it restarts the daemon on the new binary, then runs `store migrate`, which asks before migrating the store to a schema your build adds. A release does not read a store migrated past it, so switch back with `{{command}} store restore`.

Uninstall the Homebrew cask first (`brew uninstall --cask {{command}}`), or put `~/.local/bin` ahead of the Homebrew directory in your `PATH`, so the shell runs the same binary as the daemon. To go back to a release, install the cask and run `{{command}} install` and `{{command}} app install` with it.

## Changing the store's schema

`internal/adapter/store/sqlite/schema.sql` is the current schema, which a new store runs; `migrations` in `migrate.go` take an existing store there, one version each. To change the schema:

1. Edit `schema.sql` to the end state. A column `ALTER TABLE ... ADD COLUMN` adds goes last in its table, where SQLite puts it.
2. Append a `migration` with `to` set to the next version and an `up` that takes a store from the previous one there, in the transaction it is given. A change `ALTER TABLE` cannot make is the [12-step rebuild](https://www.sqlite.org/lang_altertable.html#otheralter), with `foreignKeysOff` set: the runner turns enforcement off around the transaction and fails a rebuild that leaves more foreign key violations than it found. Set `compatible` to an earlier version when releases at that version keep working on the result, such as after a column they ignore.
3. Bump `SchemaVersion` and copy `schema.sql` to `testdata/schema-v<version>.sql`.
4. When the migration moves or derives data, add a test that writes rows at the previous version, migrates and reads them back.

The tests build a store from each frozen schema, migrate it and compare it with a new store from `schema.sql`: tables, columns, keys, indexes and their SQL. They also fail when `schema.sql` differs from the frozen copy of the current version, so a schema change without a migration does not pass. A migration in a release is never edited; a fix is a new migration.

## The documentation site

The site lives in `docs/`, built with Starlight. The product name is written once, in `docs/site.config.mjs`; pages write placeholders (the product's name, command and app between double braces) that `docs/src/plugins/remark-product.mjs` fills in text, code blocks and links at build time.

```sh
make docs-ref     # regenerate the reference pages, the profile's schemas and the decision log's index
make docs         # npm ci and build into docs/dist
cd docs && npm run dev
```

`make docs-ref` must run after changing a command's help, a fact, an architecture rule, a reserved key or a decision-log entry; `go test` fails when a generated page drifts from its source.

## Working on the procedures

`LOOP_INSTRUCTIONS_DIR` points at a profile directory, such as a checkout's `profiles/default`: its `partials/<name>.md.tmpl` and `intents/<name>/instructions.md.tmpl` replace the bundled files of the same path when a turn renders, with no rebuild. The user profile's partials do not apply while it is set.

`{{command}} help dev` lists the developer tools, which the main help leaves out. `{{command}} eval-instructions [--scenario <glob>]...` (`make eval-instructions`) judges the rendered turn instructions of every scenario in `internal/turn` and writes the report under the state directory's `eval/`.

## macOS smoke test

The native layer has contract tests only. Before a release, on a Mac with a logged-in user:

To try a local build, replace the installed binary with a new file (copy it next to the old one, then `mv` it over): `cp` onto the old file keeps its inode, and macOS kills the new binary on a stale signature.

1. `{{command}} app install`. Expect `~/Applications/{{app}}.app`, the LaunchAgents reloaded, and the menu bar item `PR <n>` within a few seconds.
2. On the first install macOS asks to allow notifications. Allow. `{{command}} doctor` shows notifications as authorized.
3. Set `"workflows": {"review-follow-up": {"approval": {"window": "5m"}}}` (and the same for `review-first-round`), restart the daemon (`launchctl kickstart -k gui/$(id -u)/io.github.{{org}}.{{name}}`) and make a review round due. Expect a notification "Round in ... · owner/repo #n".
4. Choose Skip on it. Expect the row to leave "round soon" in the cockpit, a `round_skipped` event in its timeline, and no new notification for that round.
5. On the next pending round, choose Guide, type a sentence and send. Expect the round to start at once, carrying the sentence.
6. On the next one, choose Pause. Expect the row paused and no round sent.
7. Click the body of a notification. Expect the cockpit on that row.
8. Open the menu: up to five entries under Focus, Inbox and Outbox, the In loop section with its actions, Open cockpit, Sweep now, Pause automatic rounds (then Resume), the sync line, Quit.
9. Quit. Expect the item gone and the daemon still running; the menu bar starts again at the next login or with `launchctl kickstart`.
10. Turn notifications off in System Settings. Expect `doctor` to report it within a minute and the cockpit to show the warning.
