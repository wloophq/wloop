# ProjectRow

*Proposed.* A project in a list of projects: a task that is the parent of others.

A TaskRow whose glyph is the project's own state category, with its children counted: the title, sequential or parallel, the next child to do (or "no next action" in `err`, which the review flags), progress as a `loop` bar with "n of m" children in a `done` category, and "review due" in `late` past the review interval.

## Consumer provides
The project task with its children's states and categories, its `sequential` property and its review date. What makes a task a project is the profile's (a property such as `kind`); the row reads it from the view, never from a name in the client.

## Do / don't
- Do count children by the category of their state, so a profile's own states count.
- Don't give a project a glyph of its own: it is a task, and its state says where it stands.
