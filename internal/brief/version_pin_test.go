package brief

import "testing"

// GitHub's briefs keep the version they have: a change to the prompt, the
// schema or the taxonomy they hash makes every stored brief stale.
func TestTheGitHubBriefVersionIsPinned(t *testing.T) {
	t.Parallel()
	if got := Version(DefaultTaxonomy); got != "b1-8ab3e5d390e0" {
		t.Fatalf("Version = %s", got)
	}
}

func TestTheComposedSchemaWithoutExtrasIsTheCoreSchema(t *testing.T) {
	t.Parallel()
	c, err := Compose(Schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.JSON() != Schema {
		t.Fatal("the schema sent with no extras is not today's")
	}
}
