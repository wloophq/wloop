package slack

import (
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
)

type wireMessage struct {
	Subtype     string `json:"subtype"`
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts"`
	ParentUser  string `json:"parent_user_id"`
	User        string `json:"user"`
	BotID       string `json:"bot_id"`
	AppID       string `json:"app_id"`
	Username    string `json:"username"`
	Text        string `json:"text"`
	Permalink   string `json:"permalink"`
	ReplyCount  int    `json:"reply_count"`
	LatestReply string `json:"latest_reply"`
	BotProfile  *struct {
		AppID string `json:"app_id"`
		Name  string `json:"name"`
	} `json:"bot_profile"`
	Edited *struct {
		TS string `json:"ts"`
	} `json:"edited"`
	Files []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Mimetype string `json:"mimetype"`
	} `json:"files"`
	Attachments []struct {
		TitleLink   string `json:"title_link"`
		FromURL     string `json:"from_url"`
		OriginalURL string `json:"original_url"`
	} `json:"attachments"`
	Reactions []struct {
		Name  string   `json:"name"`
		Users []string `json:"users"`
	} `json:"reactions"`
}

var (
	linkRE    = regexp.MustCompile(`<(https?://[^|>\s]+)(?:\|[^>]*)?>`)
	mentionRE = regexp.MustCompile(`<@(` + UserIDPattern + `)(?:\|[^>]*)?>`)
)

func (w wireMessage) message() Message {
	m := Message{
		TS: w.TS, ThreadTS: w.ThreadTS, ParentUser: w.ParentUser,
		User: w.User, BotID: w.BotID, AppID: w.AppID, Username: w.Username,
		Subtype: w.Subtype, Text: w.Text, Deleted: w.Subtype == "tombstone",
		ReplyCount: w.ReplyCount, LatestReply: w.LatestReply, Permalink: w.Permalink,
	}
	if p := w.BotProfile; p != nil {
		m.AppID = cmp.Or(m.AppID, p.AppID)
		m.Username = cmp.Or(m.Username, p.Name)
	}
	if w.Edited != nil {
		m.EditedTS = w.Edited.TS
	}
	for _, f := range w.Files {
		m.Files = append(m.Files, File{ID: f.ID, Title: f.Title, Mimetype: f.Mimetype})
	}
	for _, r := range w.Reactions {
		m.Reactions = append(m.Reactions, Reaction{Name: r.Name, Users: r.Users})
	}
	for _, s := range linkRE.FindAllStringSubmatch(w.Text, -1) {
		m.Links = appendNew(m.Links, s[1])
	}
	for _, a := range w.Attachments {
		for _, u := range []string{a.TitleLink, a.FromURL, a.OriginalURL} {
			if u != "" {
				m.Links = appendNew(m.Links, u)
			}
		}
	}
	for _, s := range mentionRE.FindAllStringSubmatch(w.Text, -1) {
		m.Mentions = appendNew(m.Mentions, s[1])
	}
	return m
}

func appendNew(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

// MessageFrom decodes one message of a Web API page (history, replies, search).
func MessageFrom(raw json.RawMessage) (Message, error) {
	var w wireMessage
	if err := json.Unmarshal(raw, &w); err != nil {
		return Message{}, fmt.Errorf("slack message: %w", err)
	}
	return w.message(), nil
}
