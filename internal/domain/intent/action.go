package intent

// Action is how an intent's action is offered on a row. Its description is
// the intent's Summary.
type Action struct {
	Group    string
	Surfaces []string
	When     Pred
	Primary  Pred
	// Refuse are the cases that offer the action disabled; the first whose
	// When holds gives its Why.
	Refuse  []Refusal
	Note    Text
	Warning Text
	Confirm Text
}

// Refusal is one [[action.refuse]] case.
type Refusal struct {
	When Pred
	Why  Text
}
