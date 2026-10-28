package extract

import "os"

// The profile's prompts, read from the plugin's profile layer.
var (
	promptTemplate = mustRead("../profile/" + PromptFile)
	briefSchema    = mustRead("../profile/" + BriefSchemaFile)
	testPrompts    = func() *Prompts {
		p, err := NewPrompts(promptTemplate, briefSchema)
		if err != nil {
			panic(err)
		}
		return p
	}()
)

func mustRead(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Schema is the schema an extraction answers to, of the test prompts.
func Schema() string { return testPrompts.Schema() }

func schemaFor(x *Extra) (string, any, error) { return testPrompts.schemaFor(x) }
