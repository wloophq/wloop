package config

import (
	"reflect"
	"strings"
)

// Plugin is one plugin's switch; a nil Enabled means enabled.
type Plugin struct {
	Enabled *bool `json:"enabled,omitempty"`
}

var switchType = reflect.TypeFor[*bool]()

// PluginEnabled says whether the plugin id is on: its <id>.enabled when the
// file has a namespace for it, else its entry in Plugins; a nil switch is
// on.
func (c Config) PluginEnabled(id string) bool {
	on := c.Plugins[id].Enabled
	if sw, ok := c.switches()[id]; ok {
		on = sw
	}
	return on == nil || *on
}

// PluginSwitches are the ids of every plugin the config can switch.
func (c Config) PluginSwitches() []string {
	sw := c.switches()
	out := make([]string, 0, len(sw)+len(c.Plugins))
	for id := range sw {
		out = append(out, id)
	}
	for id := range c.Plugins {
		if _, ok := sw[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

// switches are the <id>.enabled of the plugins whose settings live under
// their id: each top-level table with an Enabled *bool.
func (c Config) switches() map[string]*bool {
	out := map[string]*bool{}
	v := reflect.ValueOf(c)
	for i := range v.NumField() {
		f := v.Type().Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Type.Kind() != reflect.Struct || name == "" || name == "-" {
			continue
		}
		if e := v.Field(i).FieldByName("Enabled"); e.IsValid() && e.Type() == switchType {
			out[name] = e.Interface().(*bool)
		}
	}
	return out
}

// KeyOf is the file's key of Config's top-level field, such as the
// namespace a plugin's settings live under.
func KeyOf(field string) string {
	f, ok := reflect.TypeFor[Config]().FieldByName(field)
	if !ok {
		panic("config: no field " + field)
	}
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return name
}
