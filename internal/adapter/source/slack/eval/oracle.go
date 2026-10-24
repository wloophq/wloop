package eval

import _ "embed"

//go:embed oracle.md
var oracleTemplate string

//go:embed brief.schema.json
var briefSchema string

// OraclePrompt asks for the briefing of someone who read the conversation.
func OraclePrompt(base map[string]string, conversation string) string {
	return fill(oracleTemplate, with(base, "CONVERSATION", conversation))
}

func with(base map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(base)+len(kv)/2)
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}
