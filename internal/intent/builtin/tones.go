package builtin

// TokenTones are the design's color tokens a profile may draw a thing in, by token name (design/tokens.json).
var TokenTones = []Value{
	{Name: "me", Doc: "What waits on the user; the accent."},
	{Name: "loop", Doc: "The loop's doing, or a good outcome."},
	{Name: "late", Doc: "Time and medium risk: amber."},
	{Name: "err", Doc: "A failure, a block or high risk."},
	{Name: "muted", Doc: "Secondary, nothing to signal."},
}

// DefaultTone is a decision kind's tone when decisions.toml sets none.
const DefaultTone = "me"

func tokenTones() []Value { return TokenTones }
