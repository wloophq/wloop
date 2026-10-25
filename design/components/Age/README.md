# Age

How long a unit has waited on its current waiter, in the `age` style.

Two largest units, compact: `2h 56m`, `1d 7h`. Past the stalled threshold the row gets `.late` and the age becomes `late` on `late-bg` with `radius-sm`. In the detail panel's situation block it grows to `age-lg`.

## Consumer provides
The duration string; the `late` decision comes from the daemon.
