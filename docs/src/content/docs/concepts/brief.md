---
title: The brief
description: A short preview of every pull request, written by the agent, that rows, filters and profile blocks read.
---

With briefs on, the daemon writes a short preview of every open pull request in scope:

- its **goal** and the context needed to follow it;
- its **kind**: `feature`, `fix`, `refactor`, `chore` or `infra`;
- its **size** (`xs`, `s`, `m`, `l`, `xl`) and its **risk** (`low`, `medium`, `high`), with half a sentence of why;
- up to three **open questions**;
- **tags** under nine keys, at most 3 values per key and 16 in all:

| Key | Values |
|---|---|
| `lang`, `framework`, `tool`, `domain` | open: lower-case words such as `go`, `react`, `terraform`; spellings are normalized (`golang` becomes `go`) |
| `area` | `api`, `ui`, `cli`, `data-model`, `migration`, `ci`, `build`, `deps`, `docs`, `tests`, `config`, `infra`, `observability`, `auth`, `storage`, `messaging`, `jobs` |
| `change` | `feature`, `fix`, `refac`, `perf`, `docs`, `test`, `deps`, `config`, `chore`, `revert`, `rename`, `removal`, `migration`, `security` |
| `concern` | `security`, `privacy`, `compliance`, `performance`, `data-loss`, `concurrency`, `backward-compat`, `accessibility`, `i18n`, `cost`, `reliability` |
| `breaking` | `api`, `schema`, `config`, `behavior`, `cli` |
| `audience` | `end-user`, `internal`, `developer`, `operator` |

The cockpit shows the brief in the detail panel, and size and risk as pills on each row. The `/` filter searches the goal, and the `type:`, `size:`, `risk:` and `tag:` qualifiers read it. Profile blocks can depend on it: a block under `kind=fix/` or `tags/lang=go/` applies only to pull requests whose brief says so.

## Turn it on

```json
"brief": { "enabled": true, "tier": "fast", "max_diff_bytes": 60000 }
```

The daemon reads `brief` when it starts; restart it after turning briefs on or off (on macOS, `launchctl kickstart -k gui/$(id -u)/io.github.{{org}}.{{name}}`).

Each brief is one headless call of the agent, billed to your account with it. For Claude Code it is `claude -p` on the model tier `brief.tier` (`fast` maps to `haiku` unless `agent_models` pins another model), with no tools, no MCP servers, none of your settings or hooks, and no saved session.

## When briefs run

A `brief` job runs after each fast sweep and briefs up to 5 pull requests per run, 2 at a time. Each reads the title, the description, the first linked issue, the file list and the diff cut to `brief.max_diff_bytes` (at most 200000). A pull request is briefed again when its description is edited, when a new head adds more than 5 files, or when a new release changes the prompt. While the GitHub budget is low, briefs wait.

The **first turn** of a pull request, whatever its intent, waits for its brief. `{{command}} review` prints `briefing` while it waits. `--no-brief`, `{{command}} intake without <n>`, or "Start without brief" in the cockpit and the menu bar start without it. Later turns take the brief there is and never wait.

## When briefs fail

When the agent is missing, out of credit or failing, or `gh` is logged out, briefs stop for 15 minutes, the cockpit and the menu bar say why, and first turns start without a brief. A failure that belongs to one pull request (an answer that breaks the schema, a call over its budget, a prompt too long, a call past 3 minutes) stops only that one; it is retried with a growing delay, up to a day, and at once when the pull request changes. `doctor` says whether briefs are on and how many are ready, pending or failed.

```sh
{{command}} brief refresh https://github.com/acme/api/pull/7   # brief it again now
{{command}} timings --tags                                       # tag values and the spellings dropped
```

The brief is a preview a model wrote from the pull request's text, so its author can influence the tags and therefore which profile blocks match. Each turn's instructions present it as something to verify.
