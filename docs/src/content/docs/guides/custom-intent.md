---
title: Add your own intent
description: Add a kind of turn to your profile, with its own command, action and key.
---

An intent of yours lives in your profile and needs no rebuild: the daemon and the CLI read it again when a file under the profile changes. This one asks the author of someone else's pull request one question, after you approve it, and publishes nothing.

## 1. The manifest

`<profile>/intents/ask-author/intent.toml`:

```toml
#:schema ../intent.schema.json

label = "Ask author"
summary = "Ask the author one question"
workspace = "review"
session = { strategy = "new", title = "ask" }
key = "Q"
start_line = "asking the author"

[inputs.topic]
type = "text"
flag = "--topic"
help = "what to ask about"
max = 200
persist = true

[action]
surfaces = ["row", "detail", "tui", "cli"]
visible = "row.open && !pr.mine"
hint = "Ask {{.pr.author}} one question"

[headline]
resumed = "ask round on {{.PR.Slug}}"
[[headline.new]]
text = "Ask the author of {{.PR.Slug}} one question."

[record]
states = ["done", "asked", "failed"]
```

The directory name is the intent's name: a lower-case letter, then 1 to 23 lower-case letters, digits or `-`. It may not repeat a CLI command; a bundled intent's name overrides that intent, as [Change a bundled intent](#change-a-bundled-intent) shows.

## 2. The instructions

`<profile>/intents/ask-author/instructions.md.tmpl` is a Go template over the turn:

```md
# {{.Headline}}

Read {{.PR.URL}} and write the one question its author most needs to answer.
{{- with .Inputs.topic}}
About: {{.}}{{end}}

Publish nothing: no comment, no review, no reply, no push. The user decides
whether the question goes out; you ask the user.
{{if .Resume}}{{template "resume" .}}{{end}}
{{- if .Guidance}}
## Guidance

{{.Guidance}}
{{end}}
## Steps

1. Read the pull request: `gh pr view {{.PR.URL}}` and `gh pr diff {{.PR.URL}}`.
2. Write one question for the author: short, specific, and not already
   answered in the description or the threads.
3. Record `asked` with a `free` question whose `text` is the question you
   would send, and whose `recommend` says why it matters.
4. When the user answers, record `done` with the question as they approved
   or rewrote it as the whole `summary`.

{{template "history" .}}
{{template "record" .}}
```

Include `{{template "history" .}}` and `{{template "record" .}}` in any intent that records, and `{{if .Resume}}{{template "resume" .}}{{end}}` in any that can be answered.

## 3. Check it

```sh
{{command}} profile check
{{command}} --help | grep ask-author
{{command}} profile show acme/api --show
```

`profile check` loads the manifest and renders the templates against sample turns, so a template that would fail at run time fails here, as `file:line:col: field: message`. `--help` now lists `ask-author <url> [--topic <topic>] [guidance...]`, and `profile show --show` renders its instructions for a placeholder pull request.

## 4. Run it

```sh
{{command}} ask-author https://github.com/acme/api/pull/4610 --topic 'the retry cap'
```

The turn goes to a new session of the pull request's review workspace. The cockpit and the terminal client offer it as `Q` on open pull requests that are not yours. The agent records `asked` with its question; your answer reaches its session, or a resume turn that keeps `--topic`.

A user intent runs only when you ask. To start it on its own, write a [workflow](/reference/workflow-manifest/) whose `turn.start` step names it.

## Editor support

```sh
{{command}} profile schema > <profile>/intents/intent.schema.json
```

With the `#:schema ../intent.schema.json` line at the top of each manifest, editors with Taplo (the "Even Better TOML" extension in VS Code, `taplo lsp` elsewhere) complete keys, show each field's description and flag unknown keys as you type. `profile check` remains the full check.

A broken intent of yours disables only itself: its command exits 1 with its problems, its action stays disabled with the reason, and every other intent keeps working. The [intent manifest](/reference/intent-manifest/) lists every field.

## Change a bundled intent

A directory of your profile named as a bundled intent overrides it file by file. To rewrite what an address round says and keep the rest of `address`:

```sh
{{command}} profile show acme/api --show            # the bundled instructions, rendered
mkdir -p <profile>/intents/address
# write <profile>/intents/address/instructions.md.tmpl
{{command}} profile check
```

Each file you write replaces the bundled file of the same name; the files you leave out are the bundled ones. An `intent.toml` of yours replaces the bundled manifest whole, with no merge of keys, so start from a copy of the bundled one. Files whose name starts with `.` are ignored. `profile check` lists the intent as `bundled, overridden: instructions.md.tmpl`. When your files do not load, cannot be read, or break another bundled intent, the bundled intent stays in force, `profile check` reports the problem against your file, and `{{command}} doctor` lists the intent as running as bundled. A problem your manifest causes in a bundled file you did not override, such as a bundled template reading an input your manifest dropped, is reported against your manifest. The judgment blocks that only your override made valid, such as `intent=<name>.md` for a `profile` it added or a `round=` condition for `dims` it added, are skipped until it loads again.

Your override yields to your own intents: when it claims a key, a flag or a row-driving workspace one of your intents already holds, the override is the one that does not load.

Judgment blocks (`intent=address.md`) remain the way to add to what a turn looks for; override the instructions when you want to change what the turn is.
