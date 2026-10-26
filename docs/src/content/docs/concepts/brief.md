---
title: The brief
description: A short preview of every pull request and Slack conversation, written by the agent, that rows, filters, workflows and profile blocks read.
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

## Slack conversations

Each pass of the Slack extraction writes the brief of the thread or channel it read, in the same call: its goal and context, its **kind** (`question`, `request`, `incident`, `decision` or `announcement`), its risk, up to three open questions and tags under `domain`, `audience` and `concern`. A Slack brief has no size. Each topic of a direct message conversation gets its own brief. A pass that finds nothing new keeps the brief the conversation has; at most one brief is written per conversation a day, updated by later passes that day.

The first turn of a Slack unit without a brief asks the extraction to read it at its next pass, without waiting for the conversation to go quiet, and waits up to 2 minutes for it; when nothing of the conversation is left to read, or the pass does not come in time, the turn starts without a brief. An `act` turn shows the brief of its thread, and workflows over Slack units and GTD entries read the `brief.*` facts of their unit.

## Fields of your own

The brief is an intent of the profile, the one whose manifest says `builds = "brief"`. Its prompt is `prompts/brief.md` and its taxonomy `prompts/brief-taxonomy.md`; a file of your profile at either path replaces the bundled one, and briefs are written again at their next refresh. To add fields, give your profile its own brief intent with a `schema.json` beside its manifest:

```toml
# intents/brief/intent.toml
label    = "Brief"
summary  = "The brief, with how each change solves its goal"
builds   = "brief"
provider = ["github", "slack"]
prompt   = "prompts/brief.md"
schema   = "schema.json"
```

```json
{"type": "object", "properties": {
  "solution": {"type": "string", "maxLength": 300},
  "approach": {"type": "string", "enum": ["patch", "rewrite", "config"]}
}}
```

The schema is a flat object: strings (with or without an `enum`), integers, booleans and lists of strings, under the keywords `type`, `properties`, `required`, `additionalProperties`, `enum`, `minLength`, `maxLength`, `minItems`, `maxItems`, `items`, `pattern` and `description`. Any other keyword, a nested object, or a field of another type (a number, a null, a list of anything but strings) fails the profile's load and names the field. One profile layer holds at most one brief intent per provider; yours replaces the bundled one.

Each field becomes a fact, `brief.extra.solution`, that a workflow's `when` can read; naming a field the schema does not declare fails the workflow's load. An `enum` field is also a block dimension (`approach=rewrite/intent=review.md`) and a facet (`extra.approach:rewrite`). Partials read it as `.Brief.Extra.solution`.

Every field has its own version, a hash of its schema. A new or changed field is listed in `brief.missing` on the briefs written before it; it is filled at the brief's next refresh, or by a call that asks for the missing fields alone and keeps the rest of the brief. An answer whose fields break the schema keeps the brief without them. An intent that lists fields under `brief = { need = [...] }` makes its first turn wait for that call; a call that fails lets the turn start with the brief as it is. Slack briefs carry the fields from the extraction's next pass.

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
