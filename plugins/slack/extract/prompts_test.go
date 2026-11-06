package extract

import (
	"os"
	"strings"
)

// The profile's prompts, read from the plugin's profile layer.
var (
	promptTemplate = mustRead("../profile/" + PromptFile)
	briefSchema    = mustRead("../profile/" + BriefSchemaFile)
	citedBlock     = mustRead("../profile/" + CitedFile)
	// CitedHeading and CitedEnds are the profile's cited block's first and last lines.
	CitedHeading = strings.SplitN(citedBlock, "\n", 2)[0]
	CitedEnds    = strings.TrimSpace(citedBlock[strings.LastIndex(strings.TrimRight(citedBlock, "\n"), "\n")+1:])
	testPrompts  = func() *Prompts {
		p, err := NewPrompts(promptTemplate, briefSchema, citedBlock)
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
