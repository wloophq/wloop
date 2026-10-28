# Inspector

The task's page in the detail panel: everything about one task, in one column, in this order.

1. **Header**: the title (`heading`) with the focus star, the state as a `pill` carrying its category (`data-category`), the changed flag, the properties the profile declares as a `d-props` list (name in `muted`, value in `code`), the links (each unit's label opens it in the cockpit, the service's name beside it opens it at the source, `ext`), and the task's verbs and intents as buttons.
2. **Asks**: an AskList.
3. **Brief**: goal, risk and the brief's record.
4. **Waiting on you**: one GateCard per open gate.
5. **Runs**: one RunCard per run.
6. **Timeline**: TimelineRows as a chat, oldest first, with the kind chips.
7. **Composer**: a box with **Send** (`primary`, to the task's agent) and **Save as draft** beside it (a reply held in the Outbox).
8. **Children**: the child tasks, each with its state pill.

A section with nothing to show is left out, so a fresh task reads as a header and a composer.

## Consumer provides
The task with its properties, links, asks, brief, gates, runs and children; the catalog's states and properties to word them; the timeline's events.

## Do / don't
- Do word states, properties, verbs and intents from the catalog; the client names none of them.
- Don't add a section per kind of gate or per intent: gates and runs each have one card.
