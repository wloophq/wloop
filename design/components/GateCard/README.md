# GateCard

An open gate on a task: something that waits on you before the work goes on (a draft to send, a plan step, a question, a proposal), with the intents its gate offers.

An `article.d-gate` on `bg` with the `me` edge (`strip-row`): the gate's text as its heading, the subject's text in `body` when it has one (a draft's post), the delivery and the deadline on `meta` lines, an answer box when an intent takes an answer, then one button per intent in the gate's order. The first intent is `primary`; each of the first nine carries its digit in a `kbd` (`code`, `muted`), and that digit picks it while the card has the focus.

## Consumer provides
The gate as the daemon serves it: kind, text, subject, delivery, deadline and default, and its intents with their labels, in the order of the profile's `gates.toml`. A deadline with a default reads "Send at 14:00 unless you pick another".

## Do / don't
- Do take the intents, their labels and their order from the gate; never name a gate kind or an intent in the client.
- Do keep one `primary`, the first intent.
- Don't approve from the card's own text: only a button press resolves a gate.
