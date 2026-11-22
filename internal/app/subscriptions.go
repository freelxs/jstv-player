package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// Subscription is one user-supplied plugin address.
type Subscription struct {
	ID   string `json:"id"`
	Type string `json:"type,omitempty"` // auto | plugin | live | tvbox | kazumi
	Name string `json:"name"`
	URL  string `json:"url"` // index.js, a live list, or a TVBox JSON config
	// Line is the chosen path through a multi-repository config.
	// Names are separated by U+001F so a line name can contain slashes.
	Line string `json:"line,omitempty"`
}

var (
	errNoSub    = errors.New("找不到该订阅")
	errEmptyURL = errors.New("订阅地址不能为空")
)

func newSubID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// migrate turns the old single sourceUrl setting into a subscription and
// makes sure Active points at an existing entry.
func (s *Settings) migrate() {
	// Existing subscriptions predate native data feeds and were all plugins.
	// Preserve directory-style legacy addresses instead of reclassifying them.
	for i := range s.Subscriptions {
		if s.Subscriptions[i].Type == "" {
			s.Subscriptions[i].Type = "plugin"
		}
	}
	if u := strings.TrimSpace(s.SourceURL); u != "" {
		found := false
		for _, x := range s.Subscriptions {
			if x.URL == u {
				found = true
				if s.Active == "" {
					s.Active = x.ID
				}
			}
		}
		if !found {
			sub := Subscription{ID: newSubID(), Name: "订阅 1", URL: u, Type: "plugin"}
			s.Subscriptions = append(s.Subscriptions, sub)
			if s.Active == "" {
				s.Active = sub.ID
			}
		}
	}
	s.SourceURL = ""
	if s.find(s.Active) < 0 {
		s.Active = ""
		if len(s.Subscriptions) > 0 {
			s.Active = s.Subscriptions[0].ID
		}
	}
}

func (s *Settings) find(id string) int {
	for i, x := range s.Subscriptions {
		if x.ID == id {
			return i
		}
	}
	return -1
}

// active returns the active subscription, if any.
func (s *Settings) active() (Subscription, bool) {
	if i := s.find(s.Active); i >= 0 {
		return s.Subscriptions[i], true
	}
	return Subscription{}, false
}

// add appends a subscription; the first one becomes active.
func (s *Settings) add(name, url string) (Subscription, error) {
	url, name = strings.TrimSpace(url), strings.TrimSpace(name)
	if url == "" {
		return Subscription{}, errEmptyURL
	}
	if name == "" {
		name = defaultSubName(len(s.Subscriptions) + 1)
	}
	sub := Subscription{ID: newSubID(), Name: name, URL: url}
	s.Subscriptions = append(s.Subscriptions, sub)
	if s.Active == "" {
		s.Active = sub.ID
	}
	return sub, nil
}

// update changes a subscription's name and address.
func (s *Settings) update(id, name, url string) error {
	i := s.find(id)
	if i < 0 {
		return errNoSub
	}
	url, name = strings.TrimSpace(url), strings.TrimSpace(name)
	if url == "" {
		return errEmptyURL
	}
	if name == "" {
		name = defaultSubName(i + 1)
	}
	if s.Subscriptions[i].URL != url {
		s.Subscriptions[i].Line = ""
	}
	s.Subscriptions[i].Name, s.Subscriptions[i].URL = name, url
	return nil
}

func validSubType(kind string) bool {
	switch kind {
	case "auto", "plugin", "live", "tvbox", "kazumi":
		return true
	default:
		return false
	}
}

const repoLineSep = "\x1f"

func splitRepoLine(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	parts := strings.Split(line, repoLineSep)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// remove deletes a subscription; if it was active the next one takes over.
func (s *Settings) remove(id string) error {
	i := s.find(id)
	if i < 0 {
		return errNoSub
	}
	s.Subscriptions = append(s.Subscriptions[:i:i], s.Subscriptions[i+1:]...)
	if s.Active == id {
		s.Active = ""
		if len(s.Subscriptions) > 0 {
			s.Active = s.Subscriptions[min(i, len(s.Subscriptions)-1)].ID
		}
	}
	return nil
}

func defaultSubName(n int) string {
	return "订阅 " + strconv.Itoa(n)
}
