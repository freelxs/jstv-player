package main

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	persistentLogName   = "player.log"
	persistentLogMax    = 1 << 20 // 1 MiB per file
	persistentLogCopies = 3
	persistentLogTotal  = (persistentLogCopies + 1) * persistentLogMax
	persistentLogLine   = 4096
	persistentLogAge    = 7 * 24 * time.Hour
	persistentLogLayout = time.RFC3339Nano
)

func (p *Player) persistentLogDir() string { return filepath.Join(p.dir, "logs") }

func (p *Player) logPath(index int) string {
	name := persistentLogName
	if index > 0 {
		name += "." + string(rune('0'+index))
	}
	return filepath.Join(p.persistentLogDir(), name)
}

func (p *Player) appendPersistentLog(line string) error {
	line = strings.ToValidUTF8(line, "�")
	if _, ok := parsePersistentLogTime(line); !ok {
		line = time.Now().Format(persistentLogLayout) + " " + line
	}
	line = truncatePersistentLogLine(line, persistentLogLine-1) // include the trailing newline in the cap
	line += "\n"
	dir := p.persistentLogDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	now := time.Now()
	if p.logCleanupAt.IsZero() || now.Sub(p.logCleanupAt) >= time.Hour {
		if err := p.cleanupPersistentLogs(now); err != nil {
			return err
		}
	}
	active := p.logPath(0)
	info, err := os.Stat(active)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info != nil && info.Size()+int64(len(line)) > persistentLogMax {
		if err := os.Remove(p.logPath(persistentLogCopies)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for i := persistentLogCopies - 1; i >= 0; i-- {
			src, dst := p.logPath(i), p.logPath(i+1)
			if _, err := os.Stat(src); err == nil {
				if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err := os.Rename(src, dst); err != nil {
					return err
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	f, err := os.OpenFile(active, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	_, writeErr := f.WriteString(line)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	// Four bounded files are the total-size cap (4 MiB). Keep this explicit
	// check so future rotation changes cannot silently weaken the bound.
	var total int64
	for i := 0; i <= persistentLogCopies; i++ {
		if info, err := os.Stat(p.logPath(i)); err == nil {
			total += info.Size()
		}
	}
	if total > persistentLogTotal {
		return errors.New("persistent log size limit exceeded")
	}
	return nil
}

func (p *Player) readPersistentLogs() ([]string, error) {
	now := time.Now()
	cleanupErr := p.cleanupPersistentLogs(now)
	var out []string
	// Oldest rotated file first, then the active file.
	for i := persistentLogCopies; i >= 0; i-- {
		f, err := os.Open(p.logPath(i))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		if info.Size() > persistentLogMax {
			_ = f.Close()
			return nil, errors.New("persistent log file exceeds size limit")
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), persistentLogLine+1)
		for scanner.Scan() {
			line := scanner.Text()
			if persistentLogLineFresh(line, now) {
				out = append(out, line)
			}
		}
		scanErr, closeErr := scanner.Err(), f.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if cleanupErr != nil {
		return out, cleanupErr
	}
	return out, nil
}

func parsePersistentLogTime(line string) (time.Time, bool) {
	// Read prior P2 local-time rows for upgrade compatibility. New rows use
	// RFC3339 UTC so daylight-saving transitions cannot extend their lifetime.
	if len(line) >= 20 && line[10] == ' ' {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", line[:19], time.Local); err == nil {
			return t, true
		}
	}
	sep := strings.IndexByte(line, ' ')
	if sep <= 0 {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, line[:sep])
	return t, err == nil
}

func persistentLogLineFresh(line string, now time.Time) bool {
	t, ok := parsePersistentLogTime(line)
	return ok && !t.Before(now.Add(-persistentLogAge)) && !t.After(now.Add(5*time.Minute))
}

func filterPersistentLogLines(lines []string, now time.Time) []string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if persistentLogLineFresh(line, now) {
			filtered = append(filtered, line)
		}
	}
	return filtered
}

func truncatePersistentLogLine(line string, maxBytes int) string {
	if len(line) <= maxBytes {
		return line
	}
	line = line[:maxBytes]
	for !utf8.ValidString(line) {
		line = line[:len(line)-1]
	}
	return line
}

// cleanupPersistentLogs filters rows by their own timestamp rather than file
// mtime, since appending a fresh row must not extend older rows' lifetime.
func (p *Player) cleanupPersistentLogs(now time.Time) error {
	for i := persistentLogCopies; i >= 0; i-- {
		path := p.logPath(i)
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if len(b) > persistentLogMax {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		var kept strings.Builder
		scanner := bufio.NewScanner(strings.NewReader(string(b)))
		scanner.Buffer(make([]byte, 4096), persistentLogLine)
		for scanner.Scan() {
			line := scanner.Text()
			if persistentLogLineFresh(line, now) {
				kept.WriteString(line)
				kept.WriteByte('\n')
			}
		}
		if err := scanner.Err(); err != nil {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if int64(kept.Len()) == int64(len(b)) {
			continue
		}
		if kept.Len() == 0 {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if err := os.WriteFile(path, []byte(kept.String()), 0o600); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return err
		}
	}
	p.logCleanupAt = now
	return nil
}

func (p *Player) clearPersistentLogs() error {
	for i := 0; i <= persistentLogCopies; i++ {
		if err := os.Remove(p.logPath(i)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
