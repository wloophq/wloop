package web

import "testing"

func TestTFallsBackToTheKey(t *testing.T) {
	var got struct{ Plain, Params, Missing, Plural string }
	nodeRun(t, staticJS(t, "i18n.js"), nil, `
process.stdout.write(JSON.stringify({
  Plain: t("Open cockpit"),
  Params: t("{n} of {total}", { n: 2, total: 5 }),
  Missing: t("{n} of {total}", { n: 2 }),
  Plural: tn("{n} drafts", 3),
}));`, nil, &got)
	if got.Plain != "Open cockpit" || got.Params != "2 of 5" || got.Missing != "2 of {total}" || got.Plural != "3 drafts" {
		t.Fatalf("got %+v", got)
	}
}

func TestTAndTnInPtBR(t *testing.T) { t.Skip("D5 t and tn read pt-BR strings and CLDR plural forms") }

func TestFmtDateAndNumberFollowTheLocale(t *testing.T) {
	t.Skip("D5 fmtDate and fmtNumber format in the served locale")
}

func TestPluralParityWithGo(t *testing.T) { t.Skip("D5 Intl.PluralRules agrees with i18n.Form") }

func TestBootRendersEnglishWhenStringsFail(t *testing.T) {
	t.Skip("D5 the cockpit renders English when /api/strings fails or is slow")
}
