package intent

import _ "embed"

// Schema is the JSON Schema of intent.toml, for editors (Taplo's #:schema directive).
//
//go:embed intent.schema.json
var Schema []byte

// WorkflowSchema is the JSON Schema of workflow.toml, for editors.
//
//go:embed workflow.schema.json
var WorkflowSchema []byte
