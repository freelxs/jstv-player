package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type sourcePin struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

func pinPath(dir string) string { return filepath.Join(dir, "source", "pin.json") }

func readPin(dir string) (sourcePin, error) {
	var p sourcePin
	b, err := os.ReadFile(pinPath(dir))
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(b, &p)
	return p, err
}

func writePin(dir string, p sourcePin) error {
	if err := os.MkdirAll(filepath.Dir(pinPath(dir)), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(pinPath(dir)), ".pin-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replacePinFile(name, pinPath(dir))
}

func displaySourceURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.User != nil {
		u.User = url.User("REDACTED")
	}
	q := u.Query()
	for k := range q {
		lower := strings.ToLower(k)
		if strings.Contains(lower, "token") || strings.Contains(lower, "key") || strings.Contains(lower, "sig") || strings.Contains(lower, "auth") || strings.Contains(lower, "password") {
			q.Set(k, "REDACTED")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

var errSourceTrustRequired = errors.New("source trust approval required")
