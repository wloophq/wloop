package intent_test

import "testing"

// TestDraftWordsFollowTheLocale serves a draft card's words in the profile's locale: the GitHub plugin's field
// labels and value words, the question gate's label and the stale note.
func TestDraftWordsFollowTheLocale(t *testing.T) {
	l, problems := loadProfile(t, "pt-BR", nil)
	if len(problems) != 0 {
		t.Fatalf("problems %v", problems)
	}
	f := l.UI["github"].Drafts["review"].Fields
	if f["event"].Label != "Veredito" || f["event"].Values["COMMENT"] != "Comentar" || f["comments"].Label != "Comentários na linha" {
		t.Errorf("pt-BR field words %+v", f)
	}
	if g, _ := l.Gates.Gate("question"); g.Label != "O agente pergunta" {
		t.Errorf("question gate label %q", g.Label)
	}
}
