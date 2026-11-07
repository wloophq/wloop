package i18n

import "testing"

func TestNormalize(t *testing.T) { t.Skip("D1 Normalize turns POSIX and loose locales into BCP 47") }

func TestPick(t *testing.T) {
	t.Skip("D1 Pick prefers the configured locale, else matches the detected ones")
}

func TestFormFollowsCLDR(t *testing.T) { t.Skip("D1 Form follows CLDR") }

func TestFormsForFallsBackToOther(t *testing.T) { t.Skip("D1 Forms.For falls back to other") }

func TestInterpolate(t *testing.T) { t.Skip("D1 Interpolate fills {name} and keeps unknown ones") }
