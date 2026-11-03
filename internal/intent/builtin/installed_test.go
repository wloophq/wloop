package builtin

import "testing"

func TestInstalledIsTheSameCoreVocabularyUntilOneIsInstalled(t *testing.T) {
	was := installed
	installed = nil
	t.Cleanup(func() { installed = was })
	first, second := Installed(), Installed()
	if first != second {
		t.Error("Installed built new core tables on each call")
	}
}
