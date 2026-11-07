package profile

import "testing"

func TestTextsLayerRule(t *testing.T) { t.Skip("D2 Texts follows the per-key layer rule") }

func TestTextsResolvePerKeyWhenAFileReplacesAnotherWhole(t *testing.T) {
	t.Skip("D2 a whole-file copy resolves per key")
}

func TestTextsFileRule(t *testing.T) { t.Skip("D2 whole-file texts follow the layer rule") }

func TestLocalizeRefusesStrayLocaleKeys(t *testing.T) { t.Skip("D2 a stray locale key is a problem") }

func TestLocalizeRefusesAFieldTheBaseTextDoesNotName(t *testing.T) {
	t.Skip("D2 a locale text naming a field the base does not is a problem")
}

func TestAvailableListsEveryLayersLocales(t *testing.T) {
	t.Skip("D2 Available lists every layer's locales")
}
