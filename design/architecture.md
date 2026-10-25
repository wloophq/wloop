# Information architecture

The cockpit grew one view at a time: GTD lists, then pull request views, a plan, a dashboard, explore, recent, then Slack as a second plugin. Each made sense alone. Together the rail holds 22 entries of three unlabeled kinds (lists of entries, views of units, tools), and each kind renders rows its own way. This section names the model underneath and the organization every surface follows. Entries and sources has the GTD layer in detail; Plugins and profiles has the contract that keeps it open to new plugins, intents and workflows.

## The objects

| Object | Layer | What it is |
|---|---|---|
| **Unit** | source | a thing work happens on, owned by a provider: a pull request, a Slack thread, a session, whatever the next plugin brings |
| **Card** | seam | the generic projection of a unit every surface reads |
| **Item** | seam | one ask inside a unit: request, question, decision or todo, with who asked and who acts next |
| **Entry** | GTD | your commitment about one or more items or units, in one list |
| **Project** | GTD | an outcome of several entries, parallel or sequential |
| **Message** | GTD | an Inbox entry that needs a decision: an agent's question, a post, a proposal |
| **Draft** | GTD | an outbound post waiting in the Outbox for its approval policy |
| **Turn** | behavior | one intent from the profile run on one unit |
| **Plan item** | behavior | one workflow from the profile matched on one unit |

Every object answers the same three things, in this order: **whom it waits on**, **since when**, **what you can do**.

## The rail

GTD first, because entries are the user's own system; sources below, because they are the evidence; the loop last, because it is the machinery.

| Section | Views | Notes |
|---|---|---|
| *(top)* | Area switcher, Review | the area filters every view but the Inbox; Review starts the weekly walk |
| *(no heading)* | Focus | the landing view |
| **Decide** | Inbox, Outbox | what only you can move; counts in `ink` |
| **Lists** | Next, Waiting, Scheduled, Someday, Projects | one state each; Scheduled is the day-grouped agenda |
| **Archive** | Reference, Logbook | no counts |
| **One section per plugin**, titled with its name | the plugin's perspectives in its catalog order | collapsed past three; GitHub: Needs me, My PRs, In loop, Stalled, Approved, All, Backlog, Explore; Slack: Conversations |
| **Loop** | Plan, Recent | the profile's workflows and what they did |

"Waiting on" people leaves the rail and becomes a facet on the toolbar beside the legend.

Counts follow one rule: a count shows when it asks for attention (Decide, Focus, a plugin's attention perspective such as Stalled) and is hidden or `muted` when it is inventory (All, Logbook).

## One row grammar

Every list (entry, unit row, board card, draft, plan item) is built from the same slots, left to right:

1. **Strip**: whom it waits on.
2. **Glyph**: the entry's state glyph; a unit row has none, a draft has `↗`, a plan item its decision word.
3. **Title**, with the changed dot before it when someone else moved.
4. **Tags**: contexts on entries, the plugin's tags on units; two, then `+n`.
5. **Source chip** on entries, drafts and plan items; a unit row is its own source, so its ref goes on the meta line.
6. **Date or age**, `late` and `err` by deadline or stall.
7. **One action** or the focus star at the trailing edge.

Clicking a row opens its inspector. Facets filter only from the inspector or with a modifier, so a click meant to open never changes the filter.

## The inspector

The right panel is the same frame for every object: head, state or situation, fields, evidence, actions, history.

| Object | State | Fields | Evidence | Actions |
|---|---|---|---|---|
| Entry | state control | project, contexts, area, deadline, snooze, waiting on | items, unit cards | by slot |
| Unit (from a perspective) | situation | the plugin's facts | the plugin's page: brief, timeline, messages | by group |
| Draft | policy and status | recipient, kind | the unit card | approve, edit, discard |
| Project | active, on hold, done | sequential, review interval | its entries in order | mark reviewed |

Agent prose (questions, drafts, reasons) is `body` sans; mono stays for identifiers. While the inspector is open the list hides its secondary columns.

## Across surfaces

- The **terminal client** keeps the rail sections as tab groups and the row grammar as columns, with the state glyphs as Unicode (`◌ ○ ◐ ◉ ⊘`).
- The **menu bar** lists Focus, Inbox and Outbox first, then each plugin's menu section, then the loop.
- **Notifications** use the waits-on hues: a question waits on you (`me`), a pending round on the loop (`loop`), held is `late`, blocked is `err`.
- The **docs** use `me` as their accent and describe views in rail order.
