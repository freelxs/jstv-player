package main

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPersistentLogsAreOffByDefaultAndRedactedBeforeWrite(t *testing.T) {
	p := &Player{dir: t.TempDir()}
	p.log("disabled token=not-written")
	if _, err := os.Stat(p.persistentLogDir()); !os.IsNotExist(err) {
		t.Fatalf("disabled persistence created log path: %v", err)
	}
	p.settings.PersistLogs = true
	p.log("fetch https://user:pass@example.test/a?token=secret")
	b, err := os.ReadFile(p.logPath(0))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "pass") || strings.Contains(string(b), "token=secret") || !strings.Contains(string(b), "REDACTED") {
		t.Fatalf("persistent content was not redacted: %s", b)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(p.persistentLogDir())
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("log directory mode = %o", info.Mode().Perm())
		}
		info, err = os.Stat(p.logPath(0))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("log file mode = %o", info.Mode().Perm())
		}
	}
}

func TestPersistentLogsRotateBoundAndSerializeConcurrentWrites(t *testing.T) {
	p := &Player{dir: t.TempDir(), settings: Settings{PersistLogs: true}}
	var wg sync.WaitGroup
	for i := 0; i < 1100; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); p.log(strings.Repeat("x", 4000) + string(rune('a'+i%26))) }(i)
	}
	wg.Wait()
	var total int64
	files := 0
	for i := 0; i <= persistentLogCopies; i++ {
		info, err := os.Stat(p.logPath(i))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		files++
		if info.Size() > persistentLogMax {
			t.Fatalf("log file %d exceeds limit: %d", i, info.Size())
		}
		total += info.Size()
	}
	if files < 2 || total > persistentLogTotal {
		t.Fatalf("rotation/total bound: files=%d total=%d", files, total)
	}
	if got, err := p.readPersistentLogs(); err != nil || len(got) == 0 {
		t.Fatalf("read logs: rows=%d err=%v", len(got), err)
	}
}

func TestClearLogsDoesNotTouchOtherApplicationData(t *testing.T) {
	dir := t.TempDir()
	p := &Player{dir: dir, settings: Settings{PersistLogs: true}, logs: []string{"memory"}}
	if err := os.MkdirAll(filepath.Join(dir, "subs", "one", "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	pin := filepath.Join(dir, "subs", "one", "source", "pin.json")
	if err := os.WriteFile(pin, []byte("pin"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.appendPersistentLog("row"); err != nil {
		t.Fatal(err)
	}
	if err := p.ClearLogs(); err != nil {
		t.Fatal(err)
	}
	if got := p.Logs(); len(got) != 0 {
		t.Fatalf("memory logs remain: %v", got)
	}
	if _, err := os.Stat(p.logPath(0)); !os.IsNotExist(err) {
		t.Fatalf("log remains: %v", err)
	}
	if b, err := os.ReadFile(pin); err != nil || string(b) != "pin" {
		t.Fatalf("pin modified: %q, %v", b, err)
	}
}

func TestSaveSettingsDisablesPersistentWritesBeforeReturning(t *testing.T) {
	p := &Player{dir: t.TempDir(), settings: Settings{PersistLogs: true}}
	p.log("before")
	s := p.settings
	s.PersistLogs = false
	if err := p.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p.logPath(0))
	if err != nil {
		t.Fatal(err)
	}
	p.log("after")
	updated, err := os.Stat(p.logPath(0))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Size() != info.Size() {
		t.Fatal("log write continued after disabling persistence")
	}
}

func TestPersistentLogDiskErrorsDoNotEscapeLogCall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Player{dir: dir, settings: Settings{PersistLogs: true}}
	p.log("playback continues")
	if len(p.Logs()) != 1 {
		t.Fatal("in-memory log should remain available")
	}
}

func TestPersistentLogsBoundSingleLineAndExpireOldRotations(t *testing.T) {
	p := &Player{dir: t.TempDir()}
	if err := p.appendPersistentLog(strings.Repeat("z", persistentLogLine+100)); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p.logPath(0))
	if err != nil {
		t.Fatal(err)
	}
	row := bufio.NewScanner(f)
	if !row.Scan() {
		scanErr := row.Err()
		_ = f.Close()
		t.Fatalf("read first row: %v", scanErr)
	}
	line := row.Text()
	_ = f.Close()
	if len(line) > persistentLogLine {
		t.Fatalf("line exceeds bound: %d", len(line))
	}
	old := p.logPath(3)
	if err := os.WriteFile(old, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-persistentLogAge - time.Hour)
	if err := os.Chtimes(old, aged, aged); err != nil {
		t.Fatal(err)
	}
	p.logCleanupAt = time.Time{} // simulate a cleanup boundary before the next append
	if err := p.appendPersistentLog("fresh"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("expired file remains: %v", err)
	}
}

func TestPersistentLogTruncationPreservesUTF8AndIncludesNewlineInLimit(t *testing.T) {
	p := &Player{dir: t.TempDir()}
	stamp := time.Now().Format(persistentLogLayout) + " "
	line := stamp + strings.Repeat("a", persistentLogLine-len(stamp)-3) + "€"
	if err := p.appendPersistentLog(line); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p.logPath(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > persistentLogLine || !strings.HasSuffix(string(b), "\n") || !utf8.Valid(b) {
		t.Fatalf("persistent row violates UTF-8/newline bound: bytes=%d valid=%v", len(b), utf8.Valid(b))
	}
}

func TestPersistentLogsFilterRowsByTimestampEvenWhenFileMtimeIsFresh(t *testing.T) {
	dir := t.TempDir()
	p := &Player{dir: dir, settings: Settings{PersistLogs: true}}
	now := time.Now()
	old := now.Add(-persistentLogAge-time.Hour).Format(persistentLogLayout) + " old-row"
	fresh := now.Format(persistentLogLayout) + " fresh-row"
	if err := os.MkdirAll(p.persistentLogDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.logPath(0), []byte(old+"\n"+fresh+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p.logPath(0), now, now); err != nil {
		t.Fatal(err)
	}
	got := p.Logs()
	if len(got) != 1 || !strings.Contains(got[0], "fresh-row") {
		t.Fatalf("fresh file mtime must not preserve expired rows: %q", got)
	}
	b, err := os.ReadFile(p.logPath(0))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "old-row") {
		t.Fatalf("expired row not cleaned from disk: %s", b)
	}
}

func TestPersistentLogsRestartReadOnlyCleansExpiredRowsWithoutAppend(t *testing.T) {
	dir := t.TempDir()
	first := &Player{dir: dir, settings: Settings{PersistLogs: true}}
	old := time.Now().Add(-persistentLogAge-time.Hour).Format(persistentLogLayout) + " expired"
	fresh := time.Now().Format(persistentLogLayout) + " retained"
	if err := first.appendPersistentLog(old); err != nil {
		t.Fatal(err)
	}
	if err := first.appendPersistentLog(fresh); err != nil {
		t.Fatal(err)
	}
	second := &Player{dir: dir, settings: Settings{PersistLogs: true}}
	got := second.Logs() // new Player, read-only path: no append occurs
	if len(got) != 1 || !strings.Contains(got[0], "retained") || len(second.logs) != 0 {
		t.Fatalf("restart read returned unexpected rows or appended: rows=%q memory=%q", got, second.logs)
	}
	stored, err := os.ReadFile(second.logPath(0))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "expired") {
		t.Fatalf("read-only cleanup left expired row: %s", stored)
	}
}

func TestDisabledPersistentLogsCleanExpiredDiskRowsButDoNotDisplayDisk(t *testing.T) {
	p := &Player{dir: t.TempDir()}
	if err := os.MkdirAll(p.persistentLogDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-persistentLogAge-time.Hour).Format(persistentLogLayout) + " expired"
	fresh := time.Now().Format(persistentLogLayout) + " persisted-while-disabled"
	if err := os.WriteFile(p.logPath(0), []byte(old+"\n"+fresh+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := p.Logs(); len(got) != 0 {
		t.Fatalf("disabled persistence displayed disk rows: %q", got)
	}
	b, err := os.ReadFile(p.logPath(0))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "expired") || !strings.Contains(string(b), "persisted-while-disabled") {
		t.Fatalf("disabled read cleanup did not preserve/filter the expected rows: %s", b)
	}
}

func TestPersistentLogsRemoveOversizedExistingFilesBeforeWriting(t *testing.T) {
	p := &Player{dir: t.TempDir()}
	if err := os.MkdirAll(p.persistentLogDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	oversized := p.logPath(2)
	if err := os.WriteFile(oversized, make([]byte, persistentLogMax+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.appendPersistentLog("small"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oversized); !os.IsNotExist(err) {
		t.Fatalf("oversized old file remains: %v", err)
	}
}
