package catvod

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindNodePriorityAndBundleLayout(t *testing.T) {
	root := t.TempDir()
	explicit := filepath.Join(root, "explicit")
	bundled := filepath.Join(root, "Contents", "Resources", "node", "arm64", "bin", "node")
	bundledX64 := filepath.Join(root, "Contents", "Resources", "node", "amd64", "bin", "node")
	pathNode := filepath.Join(root, "path-node")
	for _, p := range []string{explicit, bundled, bundledX64, pathNode} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(root, "Contents", "MacOS", "JSTV Player")
	stat := os.Stat
	home := func() (string, error) { return root, nil }
	getenv := func(string) string { return "" }
	lookPath := func(string) (string, error) { return pathNode, nil }
	getExe := func() (string, error) { return exe, nil }
	got, err := findNode(explicit, "darwin", "arm64", getExe, lookPath, stat, home, getenv)
	if err != nil || got != explicit {
		t.Fatalf("explicit = %q, %v", got, err)
	}
	got, err = findNode("", "darwin", "arm64", getExe, func(string) (string, error) { return "", errors.New("no PATH") }, stat, home, getenv)
	if err != nil || got != bundled {
		t.Fatalf("bundle = %q, %v", got, err)
	}
	got, err = findNode("", "darwin", "amd64", getExe, func(string) (string, error) { return "", errors.New("no PATH") }, stat, home, getenv)
	if err != nil || got != bundledX64 {
		t.Fatalf("x64 bundle = %q, %v", got, err)
	}
	got, err = findNode("", "darwin", "arm64", func() (string, error) { return "", errors.New("no executable") }, lookPath, stat, home, getenv)
	if err != nil || got != pathNode {
		t.Fatalf("PATH fallback = %q, %v", got, err)
	}
}

func TestFindNodeWindowsBundleLayoutAndMissing(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "JSTV Player.exe")
	bundled := filepath.Join(root, "node", "bin", "node.exe")
	if err := os.MkdirAll(filepath.Dir(bundled), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundled, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	stat := func(path string) (os.FileInfo, error) {
		if filepath.Clean(path) == filepath.Clean(bundled) {
			return os.Stat(bundled)
		}
		return nil, os.ErrNotExist
	}
	getExe := func() (string, error) { return exe, nil }
	noPath := func(string) (string, error) { return "", errors.New("no PATH") }
	home := func() (string, error) { return root, nil }
	getenv := func(string) string { return "" }
	got, err := findNode("", "windows", "amd64", getExe, noPath, stat, home, getenv)
	if err != nil || got != bundled {
		t.Fatalf("bundle = %q, %v", got, err)
	}
	if _, err := findNode(filepath.Join(root, "missing"), "windows", "amd64", getExe, noPath, stat, home, getenv); err == nil {
		t.Fatal("missing explicit path accepted")
	}
	missingStat := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	if _, err := findNode("", "windows", "amd64", func() (string, error) { return "", errors.New("missing") }, noPath, missingStat, home, getenv); err == nil {
		t.Fatal("missing Node should fail")
	}

	// Exercise the documented system install fallback without consulting the
	// Windows runner's actual Program Files or user environment.
	systemNode := `C:\Program Files\nodejs\node.exe`
	systemStat := func(path string) (os.FileInfo, error) {
		if filepath.Clean(path) == filepath.Clean(systemNode) {
			return os.Stat(bundled)
		}
		return nil, os.ErrNotExist
	}
	got, err = findNode("", "windows", "amd64", func() (string, error) { return "", errors.New("missing") }, noPath, systemStat, home, getenv)
	if err != nil || got != systemNode {
		t.Fatalf("system fallback = %q, %v", got, err)
	}
}
