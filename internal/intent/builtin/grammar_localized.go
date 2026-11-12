package builtin

import (
	"path"
	"slices"
	"strings"
)

// SurfaceStrings is strings.toml, the client strings.
const SurfaceStrings Surface = "strings"

// LocalizedFiles are the whole-file texts a person reads, by file name under workflows/<name>/.
var LocalizedFiles = []string{"text.md.tmpl", "why.md.tmpl"}

// PersonNotices are the notices a person reads; the others an agent reads, in English.
var PersonNotices = []string{
	NoticeReplyPlaceholder, NoticeReplyButton, NoticeSendReplyPlaceholder, NoticeSendReplyButton,
	NoticeAnswerPlaceholder, NoticeAnswerButton, NoticeDigestTitle, NoticeDigestNew, NoticeDigestView,
	NoticeNotifyInbox, NoticeNotifyDraftForMe, NoticeNotifyReply, NoticeNotifySendReply, NoticeNotifyQuestion, NoticeNotifyPinned,
	NoticeNotifyProposal, NoticeNotifyPost, NoticeNotifyYes, NoticeNotifyNo, NoticeNotifyRecommended,
	NoticeNotifyDraft, NoticeNotifyDraftFailed, NoticeNotifyOpen, NoticeNotifyDiscard,
	NoticeDraftSent, NoticeDraftSaved, NoticeDraftByRule, NoticeChatTyped, NoticeChatQueued, NoticeChatStarted,
	NoticeTaskMerged, NoticeNothingToPick, NoticeWorkflowUnknown, NoticeConfigUnavailable, NoticeSkipsUnavailable,
}

// PluralForms are the CLDR plural categories a text's forms are named by.
var PluralForms = []string{"zero", "one", "two", "few", "many", "other"}

var stringsGrammar = slices.Concat([]Key{
	{Path: "strings", In: SurfaceStrings, Kind: KindMap, Doc: "The texts the clients show, by their English text, with `{name}` placeholders."},
	{Path: "strings.*", In: SurfaceStrings, Kind: KindAny, Localized: true, Doc: "A text: a string, or a table of its CLDR plural forms (`zero`, `one`, `two`, `few`, `many`, `other`), `other` required."},
}, pluralRows(), []Key{
	{Path: "formats", In: SurfaceStrings, Kind: KindMap, Doc: "How the Go clients write dates and numbers: Go time layouts (`time`, `date`, `datetime`) and number separators (`decimal`, `group`)."},
	{Path: "formats.*", In: SurfaceStrings, Kind: KindText, Localized: true, Doc: "One format."},
})

func pluralRows() []Key {
	out := make([]Key, len(PluralForms))
	for i, f := range PluralForms {
		out[i] = Key{Path: "strings.*." + f, In: SurfaceStrings, Kind: KindText, Localized: true, Doc: "The text's " + f + " form."}
	}
	return out
}

// Localized says whether the key at path of a file of surface s holds a text a person reads; path holds
// concrete segments, an array index written "[3]".
func Localized(s Surface, path []string) bool {
	if s == SurfaceNotices {
		return len(path) == 1 && slices.Contains(PersonNotices, path[0])
	}
	w := walker{rows: grammarRows()[s]}
	var pat []string
	for _, seg := range path {
		if strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]") {
			if len(pat) == 0 {
				return false
			}
			continue
		}
		next, ok := w.match(pat, seg)
		if !ok {
			return false
		}
		pat = append(pat, next)
	}
	r, ok := w.row(pat)
	return ok && r.Localized
}

// surfaceFiles are the surfaces of the localizable files at a layer's root.
var surfaceFiles = map[string]Surface{
	"actions.toml": SurfaceActions, "views.toml": SurfaceViews, "tasks.toml": SurfaceTasks, "gates.toml": SurfaceGates,
	"keys.toml": SurfaceKeys, "notices.toml": SurfaceNotices, "strings.toml": SurfaceStrings, "ui.toml": SurfaceUI, "timeline.toml": SurfaceTimeline,
	"decisions.toml": SurfaceDecisions, "home.toml": SurfaceHome, "omnibox.toml": SurfaceOmnibox,
}

// SurfaceOf is the surface of profile file rel when it may hold a text a person reads; false otherwise.
func SurfaceOf(rel string) (Surface, bool) {
	if s, ok := surfaceFiles[rel]; ok {
		return s, true
	}
	dir, file := path.Split(rel)
	parent, name := path.Split(strings.TrimSuffix(dir, "/"))
	if name == "" || strings.Contains(strings.TrimSuffix(parent, "/"), "/") {
		return "", false
	}
	switch {
	case parent == "intents/" && file == "intent.toml":
		return SurfaceIntent, true
	case parent == "workflows/" && file == "workflow.toml":
		return SurfaceWorkflow, true
	}
	return "", false
}
