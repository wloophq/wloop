package config

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
)

// Plugin is what [plugins.<id>] says of any plugin: its switch and how its
// binary is trusted.
type Plugin struct {
	// Enabled switches the plugin; nil means enabled.
	Enabled *bool `json:"enabled,omitempty"`
	// Trust "path" runs the plugin's binary whatever its checksum. Only the
	// user writes it: a bundled plugin is trusted by its recorded checksum,
	// and any other plugin's binary is refused without it.
	Trust string `json:"trust,omitempty"`
}

// Plugins are the [plugins.<id>] tables. The bundled plugins' tables hold
// their settings too; every other plugin's holds its switch and trust, and
// its own keys, which only the plugin reads.
type Plugins struct {
	GitHub GitHub `json:"github"`
	Slack  Slack  `json:"slack"`
	// Other are the switches and trust of every other plugin, by id; the
	// composition root may switch a plugin it registers here, as a test's
	// plugin is.
	Other map[string]Plugin `json:"-"`
	// Raw are the other plugins' tables as the file wrote them, by id, which
	// a plugin reads as its configuration.
	Raw map[string]json.RawMessage `json:"-"`
}

// takeOther moves out of tree's plugins table every plugin that is not
// bundled into Other and Raw, so the rest decodes into the bundled
// plugins' settings.
func (p *Plugins) takeOther(tree map[string]any) error {
	tables, _ := tree[KeyOf("Plugins")].(map[string]any)
	fields := p.bundled()
	p.Other, p.Raw = maps.Clone(p.Other), maps.Clone(p.Raw)
	for id, table := range tables {
		if _, ok := fields[id]; ok {
			continue
		}
		raw, err := json.Marshal(table)
		if err != nil {
			return err
		}
		var sw Plugin
		if err := json.Unmarshal(raw, &sw); err != nil {
			return err
		}
		if p.Other == nil {
			p.Other = map[string]Plugin{}
		}
		if p.Raw == nil {
			p.Raw = map[string]json.RawMessage{}
		}
		p.Other[id], p.Raw[id] = sw, raw
		delete(tables, id)
	}
	return nil
}

// bundled are the settings fields of the bundled plugins, by id.
func (p *Plugins) bundled() map[string]reflect.Value {
	out := map[string]reflect.Value{}
	v := reflect.ValueOf(p).Elem()
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if name := jsonName(f); name != "" && name != "-" && f.Type.Kind() == reflect.Struct {
			out[name] = v.Field(i)
		}
	}
	return out
}

// plugin is the switch and trust of plugin id.
func (p Plugins) plugin(id string) Plugin {
	if f, ok := p.bundled()[id]; ok {
		return f.FieldByName("Plugin").Interface().(Plugin)
	}
	return p.Other[id]
}

// PluginEnabled says whether the plugin id is on: [plugins.<id>] enabled,
// on when unset.
func (c Config) PluginEnabled(id string) bool {
	on := c.Plugins.plugin(id).Enabled
	return on == nil || *on
}

// PluginTrust is [plugins.<id>] trust, "" when unset.
func (c Config) PluginTrust(id string) string { return c.Plugins.plugin(id).Trust }

// PluginTable is [plugins.<id>] as the file wrote it, for a plugin that is
// not bundled; nil when the file has none.
func (c Config) PluginTable(id string) json.RawMessage { return c.Plugins.Raw[id] }

// PluginSwitches are the ids of every plugin the config can switch: the
// bundled ones and every other the file or the composition root names.
func (c Config) PluginSwitches() []string {
	ids := slices.Collect(maps.Keys(c.Plugins.bundled()))
	for id := range c.Plugins.Other {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// PluginPath is the key path of the bundled plugin whose settings are
// Plugins' field, such as plugins.github for "GitHub".
func PluginPath(field string) []string {
	f, ok := reflect.TypeFor[Plugins]().FieldByName(field)
	if !ok {
		panic("config: no plugin field " + field)
	}
	return []string{KeyOf("Plugins"), jsonName(f)}
}

// KeyOf is the file's key of Config's top-level field.
func KeyOf(field string) string {
	f, ok := reflect.TypeFor[Config]().FieldByName(field)
	if !ok {
		panic("config: no field " + field)
	}
	return jsonName(f)
}
