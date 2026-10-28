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

// ConfigSchema is the JSON Schema of config.toml, for editors.
//
//go:embed config.schema.json
var ConfigSchema []byte

// TasksSchema is the JSON Schema of tasks.toml, for editors.
//
//go:embed tasks.schema.json
var TasksSchema []byte

// ViewsSchema is the JSON Schema of views.toml, for editors.
//
//go:embed views.schema.json
var ViewsSchema []byte

// GatesSchema is the JSON Schema of gates.toml, for editors.
//
//go:embed gates.schema.json
var GatesSchema []byte

// RecordsSchema is the JSON Schema of records.toml, for editors.
//
//go:embed records.schema.json
var RecordsSchema []byte

// KeysSchema is the JSON Schema of keys.toml, for editors.
//
//go:embed keys.schema.json
var KeysSchema []byte
