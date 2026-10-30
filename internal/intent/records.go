package intent

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

// RecordsFile is the profile file that declares the record kinds.
const RecordsFile = "records.toml"

// RecordsDef is the profile's records.toml over every layer: the record
// kinds a run, a person or a plugin writes on a task, in declared order.
type RecordsDef struct {
	Kinds []RecordKind
}

// RecordKind is a declared record kind: the schema its data follows, who
// may write it, and the task properties its fields set, by field.
type RecordKind struct {
	Name       string
	SchemaFile string
	Schema     *jsonschema.Schema
	Writers    []task.Writer
	Projects   map[string]string
}

// Kind is the record kind name declares.
func (d RecordsDef) Kind(name string) (RecordKind, bool) {
	i := slices.IndexFunc(d.Kinds, func(k RecordKind) bool { return k.Name == name })
	if i < 0 {
		return RecordKind{}, false
	}
	return d.Kinds[i], true
}

func (d RecordsDef) names() []string {
	out := make([]string, len(d.Kinds))
	for i, k := range d.Kinds {
		out[i] = k.Name
	}
	return out
}

// Writes says whether w may write a record of k.
func (k RecordKind) Writes(w task.Writer) bool { return slices.Contains(k.Writers, w) }

type mRecordsFile struct {
	Records map[string]mRecordKind `toml:"records"`
}

type mRecordKind struct {
	Schema   *string           `toml:"schema"`
	Writers  []string          `toml:"writers"`
	Projects map[string]string `toml:"projects"`
}

// loadRecords reads records.toml from bundled and the user's layers, a
// higher layer's key replacing a lower one's, then compiles each kind's
// schema from the highest layer that holds its file.
func loadRecords(layers []profile.Layer, bundled fs.FS, tasks *TasksDef) (RecordsDef, Problems) {
	var d RecordsDef
	where := map[string]*sheet{}
	problems, _ := readLayered(layers, bundled, RecordsFile, func(file string, src []byte) Problems {
		m, problems, _, complete := decode[mRecordsFile](file, src)
		if !complete {
			return problems
		}
		s := &sheet{file: file, at: positions(src), problems: problems}
		for _, name := range declared(s, "records", m.Records) {
			mergeRecordKind(s, &d, name, m.Records[name], tasks)
			where[name] = s
		}
		s.addGrammar(grammar(builtin.SurfaceRecords, src))
		return s.problems
	})
	for i := range d.Kinds {
		k := &d.Kinds[i]
		src, file, err := readProfileFile(layers, bundled, k.SchemaFile)
		path := "records." + k.Name + ".schema"
		if err != nil {
			problems = append(problems, where[k.Name].problem(path, "%s: %v", k.SchemaFile, err))
			continue
		}
		schema, err := jsonschema.Compile(src, jsonschema.Options{})
		if err != nil {
			problems = append(problems, where[k.Name].problem(path, "%s: %v", file, err))
			continue
		}
		k.Schema = schema
	}
	d.Kinds = slices.DeleteFunc(d.Kinds, func(k RecordKind) bool { return k.Schema == nil })
	return d, problems
}

func mergeRecordKind(s *sheet, d *RecordsDef, name string, m mRecordKind, tasks *TasksDef) {
	path := "records." + name
	i := slices.IndexFunc(d.Kinds, func(k RecordKind) bool { return k.Name == name })
	k := RecordKind{Name: name}
	if i >= 0 {
		k = d.Kinds[i]
		k.Projects = maps.Clone(k.Projects)
	}
	n := len(s.problems)
	switch {
	case m.Schema != nil && strings.TrimSpace(*m.Schema) == "":
		s.add(path+".schema", "is required")
	case m.Schema != nil:
		k.SchemaFile = strings.TrimSpace(*m.Schema)
	case i < 0:
		s.add(path+".schema", "is required: the JSON schema a record of the kind follows")
	}
	if m.Writers != nil {
		k.Writers = nil
		for j, w := range m.Writers {
			if !slices.Contains(task.Writers, task.Writer(w)) {
				s.oneOf(fmt.Sprintf("%s.writers[%d]", path, j), w, builtin.ValuesOf(builtin.SurfaceRecords, "records.*.writers"))
				continue
			}
			k.Writers = append(k.Writers, task.Writer(w))
		}
	}
	for _, field := range slices.Sorted(maps.Keys(m.Projects)) {
		prop := m.Projects[field]
		if tasks != nil {
			if _, ok := tasks.Prop(prop); !ok {
				s.add(path+".projects."+field, "%q names no property of %s%s", prop, TasksFile, expr.DidYouMean(prop, tasks.propNames()))
				continue
			}
		}
		if k.Projects == nil {
			k.Projects = map[string]string{}
		}
		k.Projects[field] = prop
	}
	switch {
	case len(s.problems) > n:
	case i < 0:
		d.Kinds = append(d.Kinds, k)
	default:
		d.Kinds[i] = k
	}
}

// readProfileFile is name, relative to the profile's root, from the highest
// user layer that holds it, or else from bundled; file is where it was read.
func readProfileFile(layers []profile.Layer, bundled fs.FS, name string) (src []byte, file string, err error) {
	for _, l := range slices.Backward(layers) {
		if l.Bundled() {
			continue
		}
		r, err := profile.ReadFile(l, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		return r.Body, r.Path, err
	}
	if bundled == nil {
		return nil, name, fs.ErrNotExist
	}
	src, err = fs.ReadFile(bundled, name)
	return src, name, err
}

// declaredRecord checks that the record kind r requires and the gate it
// opens without it are declared.
func (s *sheet) declaredRecord(r dintent.RecordRules, decl declarations) {
	if r.Requires != "" {
		if _, ok := decl.records.Kind(r.Requires); !ok {
			s.add("record.requires", "%q names no record kind of %s%s", r.Requires, RecordsFile, expr.DidYouMean(r.Requires, decl.records.names()))
		}
	}
	if r.Missing != "" {
		if _, ok := decl.gates.Gate(r.Missing); !ok {
			s.add("record.missing", "%q names no gate of %s%s", r.Missing, GatesFile, expr.DidYouMean(r.Missing, decl.gates.names()))
		}
	}
}
