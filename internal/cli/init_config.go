package cli

import "github.com/wloophq/wloop/internal/config"

// mergeInitConfig is the config file raw with the wizard's answers written
// in, every other line kept as written; a path the file spells another way,
// such as with ~, stays as written.
func mergeInitConfig(raw []byte, a initAnswers) ([]byte, error) {
	d, err := config.ParseDoc(raw)
	if err != nil {
		return nil, err
	}
	for _, p := range []struct {
		key, v string
		read   func(string) string
	}{
		{"projects_dir", a.ProjectsDir, config.ExpandHome},
		{"profile", a.Profile, config.ResolveProfile},
	} {
		if old, ok := d.Get(p.key).(string); ok && p.read(old) == p.v {
			continue
		}
		if err := d.Set([]string{p.key}, p.v); err != nil {
			return nil, err
		}
	}
	sets := []struct {
		path []string
		v    any
	}{
		{[]string{"agent"}, a.Agent},
		{[]string{config.KeyOf("GitHub"), "scope", "repos"}, a.Repos},
		{[]string{config.KeyOf("GitHub"), "scope", "teams"}, a.Teams},
		{[]string{config.KeyOf("GitHub"), "scope", "people"}, a.People},
		{[]string{"slack", "client_id"}, a.SlackClientID},
	}
	for _, s := range sets {
		if err := d.Set(s.path, s.v); err != nil {
			return nil, err
		}
	}
	return d.Bytes(), nil
}
