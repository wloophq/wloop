package api

// Why codes of an OmniboxResolved that fell to search or to the agent; each is a key of the client strings,
// worded with the resolution's Params.
const (
	OmniboxWhyUnclaimed    = "unclaimed_address"
	OmniboxWhyNoRef        = "no_unit_or_task"
	OmniboxWhyAmbiguous    = "ambiguous_ref"
	OmniboxWhyNothingNamed = "nothing_named"
)

// OmniboxRunFailed is the Error of an OmniboxResult whose handler refused or failed, worded with
// Params["error"], the handler's own message; outside the vocabulary and incomplete operations take the drop
// codes.
const OmniboxRunFailed = "run_failed"
