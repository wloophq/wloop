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

// ActionsSchema is the JSON Schema of actions.toml, for editors.
//
//go:embed actions.schema.json
var ActionsSchema []byte

// ConfigSchema is the JSON Schema of config.json, for editors.
//
//go:embed config.schema.json
var ConfigSchema []byte

// GTDSchema is the JSON Schema of gtd.toml, for editors.
//
//go:embed gtd.schema.json
var GTDSchema []byte

// KeysSchema is the JSON Schema of keys.toml, for editors.
//
//go:embed keys.schema.json
var KeysSchema []byte
