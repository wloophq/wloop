---
title: Development
description: Build and test from source, work on the procedures, and check a release on macOS.
---

## Build and test

```sh
make build        # the binary in ./bin
make test         # go test -race ./...
make lint         # gofmt and go vet
make fuzz         # fuzz the predicate compilers for 60 s each
make test-brand   # the tests, built with a test product name
make scan-brand   # check that nothing the program writes names the default product name
```

## The documentation site

The site lives in `docs/`, built with Starlight. The product name is written once, in `docs/site.config.mjs`; pages write placeholders (the product's name, command and app between double braces) that `docs/src/plugins/remark-product.mjs` fills in text, code blocks and links at build time.

```sh
make docs-ref     # regenerate the commands and facts reference pages from the binary
make docs         # npm ci and build into docs/dist
cd docs && npm run dev
```

`make docs-ref` must run after changing a command's help or a fact; `go test` fails when the facts page drifts from the binary.

## Working on the procedures

`LOOP_INSTRUCTIONS_DIR` points at a directory of `record.md.tmpl`, `history.md.tmpl`, `resume.md.tmpl`, `brief.md.tmpl`, `judgment.md.tmpl` and `intents/<name>/instructions.md.tmpl`, each replacing the embedded template of the same path.

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
