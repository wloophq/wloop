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
| `signals` | array of tables | The link signals the plugin reports, read as task.link_signal.<name>. |
| `kinds` | list of strings | The task kind values the plugin's units give their tasks. |
| `situations` | array of tables | The situations the plugin derives items from. |
| `jobs` | array of tables | The jobs the host runs on the plugin through Ingest, by name. |
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
| `name` | string | The name. Required. |
| `doc` | string | What it means. Required. |

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

## `[[source.signals]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The name. Required. |
| `doc` | string | What it means. Required. |

## `[[source.situations]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The name. Required. |
| `doc` | string | What it means. Required. |

## `[[source.jobs]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The job's name, which Ingest receives. Required. |
| `every` | duration | How often the job runs, as a Go duration. Required. |
| `timeout` | duration | A run's limit, as a Go duration; empty is the host's default. |

## `[[source.commands]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The command's name, after the plugin's id on the command line. Required. |
| `usage` | string | The command's arguments, as its help shows them. |

## `[[source.tools]]`

| Key | Kind | Meaning |
|---|---|---|
| `name` | string | The tool's name; its description lives in the profile's tools.toml. Required. |
| `input` | string | The JSON schema of the tool's arguments, an object. Required. |

This page is generated from `pkg/plugin/manifest` by `make docs-ref`.
