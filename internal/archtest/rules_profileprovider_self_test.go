package archtest

import "testing"

func TestRProfileProviderFlagsAPlantedKey(t *testing.T) {
	t.Parallel()
	src := "[facets.source.values]\nacme = \"Acme\"\nsession = \"Session\"\n\n[[rows]]\nid = \"x\"\nlabels = { acme = \"Acme\" }\n\n[views.mine]\nlabel = \"acme\"\n"
	got := ruleProfileProvider("profiles/default/views.toml", []byte(src), []string{"acme"})
	flagged(t, "planted", got, 2, "keys the acme plugin")
}
