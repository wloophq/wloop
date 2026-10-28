package cli

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/skills"
)

var (
	codeSpan   = regexp.MustCompile("`([^`]+)`")
	bareWord   = regexp.MustCompile(`^[a-z][a-z-]*$`)
	toolName   = regexp.MustCompile(`^(turn|inbox|task)_[a-z]+$`)
	flagInSpan = regexp.MustCompile(`--[a-z][a-z-]*`)
)

// TestOperatorSkillNamesOnlyExistingCommands reads every command the operator skill tells an agent to run:
// each names a command, its subcommand one the command's help lists, and each flag one that help names; each
// tool it names is one the MCP server serves.
func TestOperatorSkillNamesOnlyExistingCommands(t *testing.T) {
	te := newTestEnv(t, nil)
	names := te.Env.commandNames()
	topics := allTopics(te.Env.helpGroups())
	body := string(skills.Operator.Render("wloop", "test"))
	var tools []string
	for _, v := range builtin.Tools {
		tools = append(tools, v.Name)
	}
	checked := 0
	for _, m := range codeSpan.FindAllStringSubmatch(body, -1) {
		span := strings.ReplaceAll(m[1], `\|`, "|")
		if toolName.MatchString(span) && !slices.Contains(tools, span) {
			t.Errorf("the skill names the tool %s, which the MCP server does not serve (%v)", span, tools)
		}
		fields := strings.Fields(span)
		if len(fields) < 2 || fields[0] != "wloop" {
			continue
		}
		checked++
		var words []string
		for _, f := range fields[1:] {
			if !bareWord.MatchString(f) {
				break
			}
			words = append(words, f)
		}
		if len(words) == 0 {
			if f := fields[1]; strings.HasPrefix(f, "-") && !slices.Contains(helpFlags, f) {
				t.Errorf("%q: %s is no command", span, f)
			}
			continue
		}
		if !slices.Contains(names, words[0]) {
			t.Errorf("%q: %s is no command", span, words[0])
			continue
		}
		target, ok := resolveHelp(topics, words)
		if !ok {
			t.Errorf("%q: no help for %v", span, words)
			continue
		}
		var help []string
		for _, tp := range append(target.children, derefTopic(target.topic)...) {
			help = append(append(help, tp.Usage...), tp.Names...)
			for _, f := range tp.Flags {
				help = append(help, f.Name)
			}
		}
		text := strings.Join(help, "\n")
		if len(words) > 1 && !regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(words[0]+" "+words[1])+`\b`).MatchString(text) &&
			!regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(words[0])+` \[?<`).MatchString(text) {
			t.Errorf("%q: %s has no subcommand %s:\n%s", span, words[0], words[1], text)
		}
		for _, f := range flagInSpan.FindAllString(span, -1) {
			if !slices.Contains(helpFlags, f) && !regexp.MustCompile(regexp.QuoteMeta(f)+`\b`).MatchString(text) {
				t.Errorf("%q: the help of %s names no %s:\n%s", span, strings.Join(words, " "), f, text)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("read %d commands of the skill", checked)
	}
}

func derefTopic(tp *helpTopic) []helpTopic {
	if tp == nil {
		return nil
	}
	return []helpTopic{*tp}
}
