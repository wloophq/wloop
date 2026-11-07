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

A `section.d-draft` on `raise`: its purpose and state as the heading; where it posts and the language its text is written in (by name, `Intl.DisplayNames`); the text as it will be posted (markdown); why it was drafted; what sending it does besides posting, one sentence per effect from `gates.toml`'s `[effects]`; the window line when its gate has one; its actions; links to its task and to its unit at the source.

On a draft's own task its actions are the task's verbs (approve, edit, discard, retry); on any other task, its gate's intents. **Close without a message** and **Don't nudge this conversation** show only when the draft serves them among its actions, off with their reason when refused.

## Consumer provides
The gate as the daemon serves it: kind, text, subject, delivery, deadline and default, and its intents with their labels, in the order of the profile's `gates.toml`. The draft as `GET /api/drafts` serves it, with its why, language, effects, gate, intents and actions; the catalog's `effects`.

## Do / don't
- Do take the intents, their labels and their order from the gate; never name a gate kind or an intent in the client.
- Do keep one `primary`, the first intent.
- Don't mark an option the agent did not recommend, and don't draw a consequence line for an option without one.
- Don't approve from the card's own text: only a button press resolves a gate.
