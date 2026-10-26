package brief

import "testing"

func TestTheComposedSchemaAddsTheExtras(t *testing.T) { t.Skip("T5: C16") }

func TestAModelAnswerOutsideTheComposedSchemaIsRefused(t *testing.T) { t.Skip("T5: C16, RF10") }

func TestAMissingFieldIsFilledWithoutACoreRebrief(t *testing.T) {
	t.Skip("T5: C16: brief.missing and the missing-fields call")
}

func TestTheBriefIntentIsFoundByBuilds(t *testing.T) {
	t.Skip("T5: C16: never by name, one per provider")
}
