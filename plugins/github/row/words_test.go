package row

import (
	"os"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

type signalWordsFile struct {
	Situations map[string]string `toml:"situations"`
	Signals    map[string]struct {
		Label      string            `toml:"label"`
		Values     map[string]string `toml:"values"`
		ValuesFrom string            `toml:"values_from"`
		Notes      map[string]string `toml:"notes"`
	} `toml:"signals"`
}

func readWords(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := toml.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// TestEveryCardSignalCodeIsWordedInEachLocale holds that the plugin's layer words, in English and in pt-BR, every
// signal key the card sends, every value code and note it sends, and every situation kind the manifest declares,
// which the situation signal sends as its value.
func TestEveryCardSignalCodeIsWordedInEachLocale(t *testing.T) {
	var manifest struct {
		Source struct {
			Situations []struct{ Name string } `toml:"situations"`
		} `toml:"source"`
	}
	readWords(t, "../plugin.toml", &manifest)
	codes := map[string][]string{
		SignalChecks: {ChecksFailing, ChecksFailingNamed, ChecksPending, ChecksPassing},
		SignalReview: {ReviewApproved},
		SignalMerge:  {MergeConflicts},
	}
	for _, path := range []string{"../profile/ui.toml", "../profile/locales/pt-BR/ui.toml"} {
		var w signalWordsFile
		readWords(t, path, &w)
		for _, key := range []string{SignalSituation, SignalChecks, SignalReview, SignalMerge} {
			if strings.TrimSpace(w.Signals[key].Label) == "" {
				t.Errorf("%s: signal %s has no label", path, key)
			}
		}
		for key, vs := range codes {
			for _, v := range vs {
				if strings.TrimSpace(w.Signals[key].Values[v]) == "" {
					t.Errorf("%s: %s=%s has no words", path, key, v)
				}
			}
		}
		if strings.TrimSpace(w.Signals[SignalSituation].Notes[NoteStalled]) == "" {
			t.Errorf("%s: the %s note has no words", path, NoteStalled)
		}
		if len(manifest.Source.Situations) == 0 {
			t.Fatal("the manifest declares no situations")
		}
		for _, s := range manifest.Source.Situations {
			if strings.TrimSpace(w.Situations[s.Name]) == "" {
				t.Errorf("%s: situation %s has no words, which the situation signal reads", path, s.Name)
			}
		}
	}
}
