package config

// Plugin is one plugin's switch; a nil Enabled means enabled.
type Plugin struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// PluginEnabled says whether the plugin id is on: a missing entry or a nil Enabled is.
func (c Config) PluginEnabled(id string) bool {
	p, ok := c.Plugins[id]
	return !ok || p.Enabled == nil || *p.Enabled
}
