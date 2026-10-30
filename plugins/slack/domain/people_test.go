package slack

import "testing"

func TestAPersonKeyIsAUserAGroupOrANamedTeam(t *testing.T) {
	for key, valid := range map[string]bool{
		"slack:U0000000001":                 true,
		"slack:W0000000001":                 true,
		"group:S0000000001":                 true,
		"group:Plataforma Billing":          true,
		"group:@team-a":                     true,
		"slack:U01":                         false,
		"slack:UX":                          false,
		"slack:":                            false,
		"group:":                            false,
		"group: ":                           false,
		"group:group:Plataforma Billing":    false,
		"group:slack:U0000000001":           false,
		"group:ana (U0000000001)":           false,
		"group:<@U0000000001|ana>":          false,
		"group:<!subteam^S0000000001|@t-a>": false,
		"group:U0000000001":                 false,
		"group:Billing ":                    false,
		"github:octocat":                    false,
		"":                                  false,
	} {
		if err := ValidPersonKey(key); (err == nil) != valid {
			t.Errorf("ValidPersonKey(%q) = %v, want valid %v", key, err, valid)
		}
	}
}

func TestSlackIDsHaveTheirShape(t *testing.T) {
	for id, want := range map[string][2]bool{
		"U0000000001": {true, false}, "W086HPY2938": {true, false}, "S0000000001": {false, true},
		"UX": {false, false}, "WIP": {false, false}, "SEC": {false, false}, "U0001": {false, false}, "u0000000001": {false, false},
	} {
		if got := [2]bool{IsUserID(id), IsGroupID(id)}; got != want {
			t.Errorf("%s: user, group = %v, want %v", id, got, want)
		}
	}
}
