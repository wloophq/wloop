//go:build darwin

package macos

import (
	"testing"

	"github.com/wloophq/wloop/internal/client/menubar"
)

func TestResponses(t *testing.T) {
	const id = "Q5|acme|api|30|5"
	cases := map[string]menubar.Response{
		"skip":                  {ID: id, Action: menubar.ActionServed},
		"hold":                  {ID: id, Action: menubar.ActionServed},
		"guide":                 {ID: id, Action: menubar.ActionServed},
		"run_now":               {ID: id, Action: menubar.ActionServed},
		"open":                  {ID: id, Action: menubar.ActionServed},
		"answer:text":           {ID: id, Action: menubar.ActionAnswer},
		"continue":              {ID: id, Action: menubar.ActionServed},
		"dismiss":               {ID: id, Action: menubar.ActionServed},
		"answer:option:publish": {ID: id, Action: menubar.ActionAnswer, OptionID: "publish"},
		"answer:yes":            {ID: id, Action: menubar.ActionAnswer, Yes: true},
		"answer:no":             {ID: id, Action: menubar.ActionAnswer, No: true},
		defaultActionIdentifier: {ID: id, Action: menubar.ActionOpen},
	}
	for action, want := range cases {
		want.Identifier = action
		if got, ok := response(id, action); !ok || got != want {
			t.Errorf("response(%q) = %+v %v, want %+v", action, got, ok, want)
		}
	}
	if _, ok := response(id, "com.apple.UNNotificationDismissActionIdentifier"); ok {
		t.Error("a dismissal became an action")
	}
}
