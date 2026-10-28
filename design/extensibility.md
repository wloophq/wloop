# Plugins and profiles

Two things in wloop are open-ended: sources and behavior. Plugins bring new kinds of units (GitHub and Slack today, more soon), and the user's profile replaces and adds intents and workflows. The interface therefore never names a provider, an intent or a workflow. It defines **frames and slots**, fills them from what the daemon serves, and gives every plugin and every profile the same treatment the bundled ones get.

## Two layers, one seam

```
  plugin  ──  rich model: its own views, facets, situations, custom pages
     │
     ▼   projection (the seam)
  unit + card + items         the generic shape every surface reads
     │
     ▼
  tasks  ──  lists, projects, Outbox, review
     ▲
  profile  ──  intents and workflows: actions with a verb, plan items
```

- **Above the seam a plugin is free.** Its perspectives can show pull request situations, Slack participants, anything its own data model knows, with its own facets and columns.
- **At the seam it speaks one shape**: the unit, its card and its items. The task layer, the source chip, the unit card, the menu bar and notifications read only that shape, so a new plugin appears in them with no client change.
- **Below the seam everything is a task** in a list, whatever produced it.

## What the interface reads at the seam

| Field | From | Renders as |
|---|---|---|
| provider id | the unit (`<provider>:<key>`) | the source chip's mark, the rail section |
| unit kind | the plugin's view `Unit` label | the chip's kind word, the column head |
| `card.label` | the plugin | the chip's key, in `code` |
| `card.title` | the plugin | the unit card's title |
| `card.state`, `card.outcome` | the plugin's own word; `done` or `moot` | a `pill`; an outcome greys the card |
| `card.signals[]` with `tone` | the plugin | key and value on the card, colored by tone (below) |
| `card.waits_on`, `card.since` | the plugin | the waits-on strip and the age |
| `card.why` | the plugin | the situation sentence |
| `card.last_human` | the plugin | "lee replied 2h ago" with a link |
| `card.link` | the plugin | the chip's link out |
| `stale` | the store, when the plugin is off | dashed chip and card, "stale" flag |
| items: kind, state, waiting on | the plugin's derivation or extraction | the item list in the inspector |

**Tones map to tokens**, once, for every plugin: `good` → `loop-text`, `warn` → `late`, `bad` → `err`, `info` → `me-text`, `neutral` → `muted`. A plugin chooses a tone; it never chooses a color.

## The source chip, for any provider

The chip is built from the seam only: a `hover` tile with the provider's mark, the unit kind, and `card.label`.

- The mark is two uppercase letters from the provider id (`github` → `GH`, `slack` → `SL`, a future `linear` → `LI`), unless the plugin declares its own two letters. Never a logo and never a hue: a mark identifies, the waits-on strip ranks.
- The kind word is the plugin's view `Unit` label in sentence case, shortened by the plugin if it declares one (`Pull request` → `PR`).
- A unit of a core provider (`session`, `workspace`) uses the same chip.

## Perspectives a plugin ships

A plugin's views go under its own section of the rail, titled with the plugin's name, in the order its catalog gives, collapsed to the first three when there are more.

- **Row views** (`rows: true`) use the shared row grammar. The plugin supplies the unit column's label, the facets the toolbar offers for grouping and sorting, the situations the Why column ranks, and the qualifiers the filter box accepts. The client draws them all the same way.
- **Custom views** (`rows: false`, such as Backlog or Explore) render inside the view frame: the view heading, the toolbar slot, the content area and the empty state. The frame keeps the gutter, the type scale and the tokens; the content is the plugin's.
- A plugin's view may carry badge counts in the rail only for attention (see Information architecture).

## Actions from the profile

Every action (built-in, a bundled intent, an intent the profile overrides or adds) arrives from the daemon with an id, a label, a group, the surfaces it appears on, a scope (`unit`, `item`, `draft`), an input, and optionally the **verb** it fulfills and the plugin it belongs to. The client places actions by those fields, never by id.

| Slot | Takes | Where |
|---|---|---|
| **State control** | verbs `next`, `waiting`, `snooze`, `done`, `dismiss` | the inspector's segmented control and the row's state glyph; an action with that verb runs instead of the bare state change |
| **Primary** | the daemon's recommended action, any verb | the one `primary` button of a row, an inspector or a question |
| **Delegate** | verb `delegate` | the inspector's Delegate block, with the action's input (guidance) |
| **Approve** | verb `approve` | the question or draft block |
| **Draft** | scope `draft`, verbs `edit`, `retry` | the draft row and its inspector |
| **By group** | everything else: `round`, `turn`, `control`, `workspace`, `brief`, `intake` | the actions bar, one cluster per group, in catalog order |

Labels are the manifest's own words. A profile that renames `review` changes the button text; one that adds an intent adds a button to its group's cluster on the surfaces it names. An input of kind `text`, `duration` or `answer` gets the matching control (textarea, snooze picker, option buttons). An action the daemon offers but marks unavailable stays visible as the `off` button with its reason in the tooltip.

## Workflows from the profile

The plan lists workflow items generically: the workflow's name from its manifest in 600 weight, the subject unit as a source chip, the mode (`observe` in `late`), the decision (`pending` in `loop-text`, `blocked` in `err`), the reason, and the Apply and Dismiss actions. A user workflow is indistinguishable from a bundled one except for a `profile` flag when its manifest comes from the profile, so the user can tell what they changed.

## Rules for anyone adding a surface

1. Read only the seam's fields; if a field you need is missing, it belongs in the card, not in a provider check on the client.
2. No `if provider ==`, no `if intent ==`, no hard-coded label.
3. Colors come from waits-on, time and tone. A plugin gets a mark and a name, never a color.
4. Every new view sits in the view frame; every new action sits in a slot.
