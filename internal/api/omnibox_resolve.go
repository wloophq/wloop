package api

// Why codes of an OmniboxResolved that fell to search or to the agent; each is a key of the client strings,
// worded with the resolution's Params.
const (
	OmniboxWhyUnclaimed    = "unclaimed_address"
	OmniboxWhyNoRef        = "no_unit_or_task"
	OmniboxWhyAmbiguous    = "ambiguous_ref"
	OmniboxWhyNothingNamed = "nothing_named"
)

// Why codes of an OmniboxUnit that offers no action; each is a key of the client strings, worded with the
// unit's Params.
const (
	OmniboxUnitUnreadable = "unit_unreadable"
	OmniboxUnitNoRow      = "unit_no_row"
)

// The Error of an OmniboxResult whose handler refused or failed, by its status: each is a key of the client
// strings, whose words name no handler text; Params["detail"] keeps the handler's own message, in English.
// Outside the vocabulary and incomplete operations take the drop codes.
const (
	OmniboxRunInvalid     = "run_invalid"     // 400
	OmniboxRunForbidden   = "run_forbidden"   // 403
	OmniboxRunNotFound    = "run_not_found"   // 404
	OmniboxRunConflict    = "run_conflict"    // 409
	OmniboxRunUnavailable = "run_unavailable" // 503
	OmniboxRunFailed      = "run_failed"      // any other
)
