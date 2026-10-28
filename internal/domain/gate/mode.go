package gate

// Mode is how an approval decides a gate it opens: wait holds it for a
// person, send and run release it at once, delay releases it to its
// Default when its window ends unless a person resolves it first, and
// dry_run, on a draft gate only, rejects it so the draft is stored and
// never sent.
type Mode string

const (
	ModeWait   Mode = "wait"
	ModeSend   Mode = "send"
	ModeRun    Mode = "run"
	ModeDelay  Mode = "delay"
	ModeDryRun Mode = "dry_run"
)

// Modes are every mode.
var Modes = []Mode{ModeWait, ModeDelay, ModeSend, ModeDryRun, ModeRun}

// Releases says whether m resolves a gate at once, with no person.
func (m Mode) Releases() bool { return m == ModeSend || m == ModeRun }

// AppliesTo says whether m means anything on a gate of kind k: dry_run is
// a draft's only.
func (m Mode) AppliesTo(k Kind) bool { return m != ModeDryRun || k == KindDraft }
