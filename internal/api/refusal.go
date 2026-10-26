package api

import (
	"fmt"
	"strconv"
	"strings"
)

// RefusalNotOffered is the refusal code of an action the row does not offer.
const RefusalNotOffered = "not_offered"

// BuiltinWhy is the refusal code of a built-in action disabled by its
// actions.toml why: "<id>.why".
func BuiltinWhy(id string) string { return id + ".why" }

// RefuseCase is the refusal code of an intent's action disabled by its
// [[action.refuse]] case i: "action.refuse[<i>]".
func RefuseCase(i int) string { return fmt.Sprintf("action.refuse[%d]", i) }

// RefuseCaseIndex is the case a RefuseCase code names.
func RefuseCaseIndex(code string) (int, bool) {
	n, _ := strings.CutPrefix(code, "action.refuse[")
	i, err := strconv.Atoi(strings.TrimSuffix(n, "]"))
	return i, err == nil && i >= 0 && RefuseCase(i) == code
}
