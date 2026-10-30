package web

import (
	"slices"
	"testing"
)

func TestTheRoundCountdownsAskForTheOpenPlanGatesOnly(t *testing.T) {
	var got struct {
		URL   string   `json:"url"`
		Units []string `json:"units"`
	}
	nodeRun(t, `const state = {};
const fetches = [];
const isSet = (v) => !!v && v !== "0001-01-01T00:00:00Z";
const getJSON = async (url) => {
  fetches.push(url);
  return [
    { id: 1, kind: "plan", unit: "github:acme/api#1", deadline: "2026-10-30T12:00:00Z" },
    { id: 2, kind: "plan", unit: "github:acme/api#2", deadline: "0001-01-01T00:00:00Z" },
    { id: 3, kind: "plan", unit: "", deadline: "2026-10-30T12:00:00Z" },
  ];
};
`+jsFunction(t, staticJS(t, "views.js"), "loadRoundGates"), nil, `(async () => {
  await loadRoundGates();
  process.stdout.write(JSON.stringify({ url: fetches[0], units: [...state.roundGates.keys()] }));
})();`, nil, &got)
	if got.URL != "/api/gates?status=open&kind=plan" || !slices.Equal(got.Units, []string{"github:acme/api#1"}) {
		t.Errorf("%+v", got)
	}
}
