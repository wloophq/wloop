# GateCard

The one decision card: an open gate on a task (a plan step, a question, a post, a proposal) or a draft, with the actions it offers. `static/gate.js` draws it for the detail panel, and the Outbox and the queues call the same functions (`gateCard`, `draftCard`).

## Gate

An `article.ask.d-gate` on `raise`, `rule-strong` border, `radius-lg`:

- a head line: the `me` dot, the gate's text, the message's time (`fmtDate`, `mono`, `faint`);
- the agent's message as safe markdown (`renderMarkdown`), folded after its first four blocks behind **Read all**; raw HTML stays text and only `https://` links are links;
- the delivery and the deadline on `meta` lines; a deadline with a default reads "Approve at 14:00 unless you pick another";
- for a question whose answer intent takes text and whose message offers options, one `button.opt` per option, radio-like (`aria-pressed`): the label, **recommended** on the one the agent recommends, its consequence below when it has one. The recommended option comes first and starts picked; with none recommended, none is;
- the answer box when an intent takes an answer; `⌘↵` (or Ctrl+Enter) in it runs that intent;
- one button per intent in the gate's order. The first intent is `primary`; each of the first nine carries its digit in a `kbd`, and that digit picks it while the card has the focus. The answer intent sends the option picked, then the text typed.

## Draft

A `section.d-draft` on `raise`: its purpose and, once it left your hands, its state as the heading, in the words of `gates.toml`'s `[drafts.purposes]` and `[drafts.states]` (a purpose with no word shows as named); in the Outbox, what it answers (the [OutboxGroup](../OutboxGroup/README.md) context block); where it posts and the language its text is written in (by name, `Intl.DisplayNames`); who wrote its current content, in `gates.toml`'s `[drafts.authors]` words; the summary line its kind's preview template renders (`d-preview`, from the plugin's profile layer, none when the kind has no template); the text as it will be posted (markdown); every value of its payload, as a `dl.d-payload` of the plugin's words (`ui.toml`'s `[drafts.<kind>.fields]`) over the value, a worded value keeping its raw one in its title, an object's or a list's values nested one step in on a `rule` border, multi-line values whole; the profile's notes on how it stands (`d-note`: stale in `err`, held from rules, posted by your rule); why it was drafted; what sending it does besides posting, one sentence per effect from `gates.toml`'s `[effects]`; the window line when its gate has one; its actions; links to its task and to its unit at the source.

While a gate's or a draft's intent is on its way, its card dims (`.resolving`, `aria-busy`, over `motion-fast`) and takes no clicks; the next redraw drops it, and a failure brings it back.

While a draft is being edited, each field the daemon marks editable is a `select` over its schema's options in the plugin's words; a field with no options stays a value. The text is a textarea unless the draft's kind locks it (`text_locked`), and a draft with nothing editable offers no Edit. Approving sends the edits with the rev the card showed. When the draft is revised while it is being edited, the card says so in a note with who wrote it and its new text, and drops the edits of its fields. A stale draft's approval (and retry) is off, titled with the stale note; edit and discard stay.

On a draft's own task its actions are the task's verbs (approve, edit, discard, retry); on any other task, its gate's intents. **Close without a message** and **Don't nudge this conversation** show only when the draft serves them among its actions, off with their reason when refused.

## Consumer provides
The gate as the daemon serves it: kind, text, subject, delivery, deadline and default, and its intents with their labels, in the order of the profile's `gates.toml`. The draft as `GET /api/drafts` serves it, with its why, language, effects, gate, intents and actions; the catalog's `effects`, `draft_purposes` and `draft_states`.

## Do / don't
- Do take the intents, their labels and their order from the gate; never name a gate kind or an intent in the client.
- Do keep one `primary`, the first intent.
- Don't mark an option the agent did not recommend, and don't draw a consequence line for an option without one.
- Don't approve from the card's own text: only a button press resolves a gate.
