package api

// Why codes of an OmniboxResolved that fell to search or to the agent; each is a key of the client strings,
// worded with the resolution's Params.
const (
	OmniboxWhyUnclaimed    = "unclaimed_address"
	OmniboxWhyNoRef        = "no_unit_or_task"
	OmniboxWhyAmbiguous    = "ambiguous_ref"
	OmniboxWhyNothingNamed = "nothing_named"
)
