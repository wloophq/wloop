---
title: Plugin manifest
description: Every field of plugin.toml, the manifest beside a plugin's binary.
---

A plugin lives in `<config>/plugins/<id>/`: its binary, its `plugin.toml` and its profile layer. The host reads the manifest before it starts the binary, so a disabled plugin's unit keys still parse. The loader refuses any key this page does not list. A source plugin's manifest:

```toml
id = "pulls"
kinds = ["source"]
protocol = 1

[source]
name = "Pulls"
slot = "pull"

[source.codec]
keys = ["{owner}/{repo}#{number}"]
urls = ["https://pulls.example/{owner}/{repo}/pull/{number}"]

[source.codec.fields]
number = '[1-9][0-9]*'

[[source.facts]]
name = "pull.state"
type = "string"
doc = "The pull request's state."

[[source.jobs]]
name = "ingest"
every = "5m"
```

## Top-level keys

| Key | Kind | Meaning |
|---|---|---|
| `id` | string | The plugin's id, a lowercase name of at most 32 letters, digits, - and _; a source plugin's units carry it as their provider. Required. |
| `kinds` | list of `source` | The kinds the plugin's binary serves; each has a table of its name. Required. |
| `protocol` | integer | The plugin protocol version the binary speaks. Required. |
| `source` | table | What a source plugin declares: its codec, its vocabulary, its jobs, commands and tools. |

## `[source]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | How a person reads the plugin's service, such as "GitHub". Required. |
| `codec` | table | How the plugin's unit keys and addresses are spelled. Required. |
| `slot` | string | The workflow fact slot of the plugin's units, such as "pr"; every fact name starts with it. Required. |
| `brief_subject` | bool | The plugin's units may have a brief, whose facts their workflows read. |
| `rounds` | bool | The plugin's units count automatic turns per unit, against turns.per_unit_per_day. |
| `payload` | string | The template root of the plugin's turn payload, such as "PR", which only intents of the plugin read. |
| `facts` | array of tables | The workflow facts the plugin reports. |
| `effects` | array of tables | The turn and draft effects the plugin applies through ApplyEffect. |
| `events` | array of tables | The event kinds only the plugin's units record. |
| `roles` | array of tables | The roles an intent of the plugin may take, each naming host mechanisms. |
| `row_kinds` | list of strings | The values of row.kind on the plugin's rows. |
| `deliveries` | array of tables | How the plugin posts drafts. |
| `draft_kinds` | array of tables | The kinds of the plugin's drafts; the host derives a draft's delivery from its kind. |
| `default_delivery` | string | The delivery of a draft whose kind names none; one of deliveries. |
| `signals` | array of tables | The link signals the plugin reports, read as task.link_signal.<name>. |
| `marks` | array of tables | The marks the plugin's rounds write that a workflow may read with handled(); a name has no slash, space or control character. |
| `kinds` | list of strings | The task kind values the plugin's units give their tasks. |
| `situations` | array of tables | The situations the plugin derives, in the order the cockpit ranks them. |
| `steps` | list of strings | The core workflow steps that run on the plugin's units, such as "turn.dismiss". |
| `jobs` | array of tables | The jobs the host runs on the plugin through Ingest, by name. |
| `config_reads` | list of strings | The configuration keys outside its own table the plugin reads, such as "sweep" or "items.heat"; Config returns them, checked against the configuration's keys. |
| `notify` | list of `rounds`, `gates`, `marks`, `tasks`, `turns`, `sessions`, `overrides`, `viewer`, `config`, `events`, `workspaces`, `agents` | The host topics whose changes the host notifies the plugin of. |
| `ui` | table | What clients show of the plugin; every label lives in its profile layer. |
| `commands` | array of tables | The commands the plugin binary serves in CLI mode, which run without the daemon. |
| `tools` | array of tables | The MCP tools the plugin serves through CallTool. |

## `[source.codec]`

| Key | Kind | Meaning |
|---|---|---|
| `keys` | list of strings | The templates of a unit key, such as "{owner}/{repo}#{number}"; a key is valid when it matches one. Required. |
| `urls` | list of strings | The templates of a unit's web address; the first whose fields a key has is its address, and an address matching one names the key whose template has the same fields. |
| `label` | string | The template of the unit as a person reads it; empty, or a field the key lacks, reads the key. |
| `fields` | table of strings | The pattern of a field, by name, as a Go regular expression. |
| `fold_case` | bool | Two keys that differ only in case name the same unit. |
| `target` | string | How a command line names a unit, such as "<url>". |
| `targets` | string | The pattern a draft target this plugin posts to matches, as a Go regular expression; empty takes none. |

## `[[source.facts]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The fact's name, <slot>.<name>. Required. |
| `type` | one of `bool`, `int`, `string`, `list`, `duration`, `timestamp` | The fact's type. Required. |
| `doc` | string | What the fact says, for the reference pages. Required. |
| `slots` | list of `action`, `turn`, `name` | Where the fact is read besides the workflows. |
| `dim` | bool | The fact is a profile dimension. |
| `enum` | list of strings | The values the fact takes; empty takes any. |
| `untrusted` | bool | A third party's text sets the fact, so no approval rule reads it. |
| `time_varying` | bool | The fact changes with time alone: the plugin reports it as a temporal value. |

## `[[source.effects]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The effect's name, <slot>.<name>. Required. |
| `applies` | list of `turn`, `draft` | What may carry it. Required. |
| `doc` | string | What the effect does. Required. |

## `[[source.events]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The event kind. Required. |
| `doc` | string | What happened. Required. |
| `resolves` | one of `review`, `merged`, `closed` | The link event the host's resolver reads it as. |
| `answers_asked_turn` | bool | The event answers the turn that asked for it, which my_review reconciles with. |
| `verdicts` | table of strings | The record verdict of each state an answering event's data starts with (APPROVE, REQUEST_CHANGES or COMMENT); a state it lacks answers nothing. |

## `[[source.roles]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The role's name. Required. |
| `doc` | string | What the role's turns do. Required. |
| `round_kind` | string | The sweep round its turns are, such as "review". |
| `reconcile` | string | The host reconcile its turns run, such as "my_review". |
| `prepare` | string | The host prepare its turns run, such as "action:nudge.prepare". |
| `launch` | string | The host launch its turns run, such as "probe". |
| `workspace` | string | The workspace builtin its turns run in; empty is any. |
| `context` | string | What its turns are about. |
| `policy` | string | The agent policy of its sessions. |
| `drives_row` | bool | Its turns drive the row of the workspace they run in. |

## `[[source.deliveries]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The delivery's name, such as "comment". Required. |
| `posts` | bool | A draft with this delivery reaches people, so it waits for approval. |

## `[[source.draft_kinds]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The kind's name, such as "nudge". Required. |
| `purpose` | string | What a draft of the kind is for; empty is its name. |
| `delivery` | string | How it is posted, one of deliveries; empty is default_delivery. |

## `[[source.signals]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The name. Required. |
| `doc` | string | What it means. Required. |

## `[[source.marks]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The name. Required. |
| `doc` | string | What it means. Required. |

## `[[source.situations]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The situation's kind. Required. |
| `doc` | string | What the unit waits on. Required. |
| `rank` | integer | Its order among the plugin's situations, lowest first; 0 is its place among the declarations. |

## `[[source.jobs]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The job's name, which Ingest receives. Required. |
| `every` | duration | How often the job runs, as a Go duration, unless every_from is set. Required. |
| `every_from` | string | A duration key of the configuration the interval comes from, such as "sweep.fast", one of config_reads or under them; every is its default. |
| `timeout` | duration | A run's limit, as a Go duration; empty is the host's default. |

## `[source.ui]`

| Key | Kind | Meaning |
|---|---|---|
| `views` | array of tables | The plugin's views. |
| `facets` | array of tables | The facets of the plugin's rows, whose values a snapshot reports. |
| `menubar` | array of tables | The menu bar's sections of the plugin. |
| `keys` | array of tables | The plugin's key bindings. |
| `web` | string | A directory of the plugin the host serves under /plugins/<id>/, with no call to the plugin. |

## `[[source.ui.views]]`

| Key | Kind | Meaning |
|---|---|---|
| `id` | string | The view's id, unique across plugins. Required. |
| `rows` | bool | A list of rows filtered by a snapshot's views; otherwise a custom view served by View. |
| `order` | integer | Its order among the views. |
| `surfaces` | list of `web`, `tui`, `menubar` | Where it shows. |
| `unit_sort` | string | The facet the unit column sorts by. |

## `[[source.ui.facets]]`

| Key | Kind | Meaning |
|---|---|---|
| `id` | string | The facet's id. Required. |
| `field` | string | What it reads: row.*, item.* or <provider>.*. |
| `group` | bool | Rows may be grouped by it. |
| `sort` | one of `rank`, `text`, `time` | How its values sort. |
| `rank` | list of strings | Its values in order, with sort = rank. |
| `buckets` | array of tables | The age buckets of a time facet, youngest first; only with sort = time. |
| `match` | one of `exact`, `suffix`, `prefix`, `team` | How a filter matches a value; empty is exact. |
| `menu` | bool | It shows in the filter menu. |

## `[[source.ui.facets.buckets]]`

| Key | Kind | Meaning |
|---|---|---|
| `id` | string | The bucket's id. Required. |
| `within` | duration | The age its values are younger than, as a Go duration. Required. |

## `[[source.ui.menubar]]`

| Key | Kind | Meaning |
|---|---|---|
| `id` | string | The section's id. Required. |
| `view` | string | The view it shows. Required. |

## `[[source.ui.keys]]`

| Key | Kind | Meaning |
|---|---|---|
| `key` | string | The key. Required. |
| `action` | string | The action it runs. Required. |
| `surface` | one of `web`, `tui`, `menubar` | Where it binds; empty is every surface. |

## `[[source.commands]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The command's name, after the plugin's id on the command line. Required. |
| `usage` | string | The command's arguments, as its help shows them. |

## `[[source.tools]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The tool's name; its description lives in the profile's tools.toml. Required. |
| `doc` | string | What the tool does, for the reference pages. Required. |
| `input` | string | The JSON schema of the tool's arguments, an object. Required. |
| `posts` | one of `now` | The tool posts at once, recorded as a sent draft; empty posts nothing. |
| `answers` | array of tables | The answers the tool returns, which tools.toml words. |

## `[[source.tools.answers]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The answer's name. Required. |
| `doc` | string | When the tool answers it. Required. |
| `fields` | array of tables | The fields its wording reads. |

## `[[source.tools.answers.fields]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The field's name, as the template reads it. Required. |
| `number` | bool | The field is a number; otherwise text. |

This page is generated from `pkg/plugin/manifest` by `make docs-ref`.
