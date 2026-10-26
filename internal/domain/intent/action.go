package intent

// Action is how an intent's action is offered on a row.
type Action struct {
	Group     string
	Surfaces  []string
	When      Pred
	Enabled   Pred
	ReasonOff Text
	Primary   Pred
	Confirm   Text
	Note      Text
	Warning   string
}
