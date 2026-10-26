package intent

import (
	"fmt"
	"regexp"
	"slices"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dunit "github.com/wloophq/wloop/internal/domain/unit"
)

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// provider reads provider as one id or a list of ids; absent is the
// profile's default provider.
func (s *sheet) provider(d *dintent.Def, v any) {
	switch v := v.(type) {
	case nil:
		if d.Provider == "" {
			s.add("provider", "is required: name it here, or set provider in %s", DefaultsFile)
		}
	case string:
		d.Provider = dunit.ProviderID(v)
	case []any:
		if len(v) == 0 {
			s.add("provider", "lists at least one provider")
			return
		}
		for i, p := range v {
			id, ok := p.(string)
			if !ok {
				s.add(fmt.Sprintf("provider[%d]", i), "must be a string")
				return
			}
			d.Providers = append(d.Providers, dunit.ProviderID(id))
		}
		d.Provider = d.Providers[0]
	default:
		s.add("provider", "must be a provider id or a list of them")
	}
}

// workspace reads workspace as a builtin's name or a table naming the
// repository; a builder intent needs none.
func (s *sheet) workspace(d *dintent.Def, v any) {
	switch v := v.(type) {
	case nil:
		if d.Builds == "" {
			s.add("workspace", "is required")
		}
	case string:
		d.Workspace = v
	case map[string]any:
		w := &dintent.WorkspaceRepo{}
		for k, raw := range v {
			str, ok := raw.(string)
			switch {
			case k != "repo" && k != "repo_input":
				s.add("workspace."+k, "unknown key; a workspace table holds repo or repo_input")
				return
			case !ok:
				s.add("workspace."+k, "must be a string")
				return
			case k == "repo":
				w.Repo = str
			default:
				w.RepoInput = str
			}
		}
		if (w.Repo == "") == (w.RepoInput == "") {
			s.add("workspace", "a workspace table holds exactly one of repo or repo_input")
			return
		}
		d.WorkspaceRepo = w
	default:
		s.add("workspace", "must be a workspace builtin's name or a table with repo or repo_input")
	}
}

// validateKeys checks the keys that replace Go's switches on intent names
// and the builder's.
func (s *sheet) validateKeys(d dintent.Def) {
	for _, p := range d.Providers {
		if !dunit.ValidProvider(string(p)) && p != dintent.AnyProvider {
			s.add("provider", "%q is not a provider id: lowercase letters, digits, _ and -, starting with a letter", p)
		}
	}
	if w := d.WorkspaceRepo; w != nil {
		if w.Repo != "" && !repoRe.MatchString(w.Repo) {
			s.add("workspace.repo", "%q is not a repository: owner/name", w.Repo)
		}
		if w.RepoInput != "" && !slices.ContainsFunc(d.Inputs, func(in dintent.Input) bool { return in.Name == w.RepoInput }) {
			s.add("workspace.repo_input", "%q is no input of the intent", w.RepoInput)
		}
	}
	if d.Builds != "" {
		if d.Role != "" {
			s.add("role", "a builder intent holds no role")
		}
		if d.Prompt == "" {
			s.add("prompt", "is required with builds: the prompt file, such as prompts/brief.md")
		}
	} else {
		for k, v := range map[string]string{"prompt": d.Prompt, "schema": d.Schema} {
			if v != "" {
				s.add(k, "is read only with builds")
			}
		}
	}
}
