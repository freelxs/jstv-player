package main

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const (
	persistentLogName   = "player.log"
	persistentLogMax    = 1 << 20 // 1 MiB per file
	persistentLogCopies = 3
	persistentLogTotal  = (persistentLogCopies + 1) * persistentLogMax
	persistentLogLine   = 4096
	persistentLogAge    = 7 * 24 * time.Hour
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
	if len(line) > persistentLogLine {
		line = line[:persistentLogLine]
	}
	line += "\n"
	dir := p.persistentLogDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	now := time.Now()
	for i := 0; i <= persistentLogCopies; i++ {
		path := p.logPath(i)
		info, err := os.Stat(path)
		if err == nil && (now.Sub(info.ModTime()) > persistentLogAge || info.Size() > persistentLogMax) {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
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
			out = append(out, scanner.Text())
		}
		scanErr, closeErr := scanner.Err(), f.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return out, nil
}

func (p *Player) clearPersistentLogs() error {
	for i := 0; i <= persistentLogCopies; i++ {
		if err := os.Remove(p.logPath(i)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
