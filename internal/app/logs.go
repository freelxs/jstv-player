package app

import (
	"time"
)

func (p *Player) log(s string) {
	s = sanitizeLogLine(s)
	line := time.Now().UTC().Format(persistentLogLayout+" ") + s
	p.logMu.Lock()
	defer p.logMu.Unlock()
	p.mu.Lock()
	p.logs = append(p.logs, line)
	if len(p.logs) > 500 {
		p.logs = p.logs[len(p.logs)-500:]
	}
	persist := p.settings.PersistLogs
	p.mu.Unlock()
	if persist {
		_ = p.appendPersistentLog(line)
	}
}

// Logs returns the recent output of the source process.
func (p *Player) Logs() []string {
	p.logMu.Lock()
	defer p.logMu.Unlock()
	p.mu.Lock()
	persist := p.settings.PersistLogs
	memory := append([]string(nil), p.logs...)
	p.mu.Unlock()
	lines, _ := p.readPersistentLogs()
	if persist {
		if len(lines) > 0 {
			return lines
		}
	}
	return filterPersistentLogLines(memory, time.Now())
}

// ClearLogs removes only in-memory and application-owned persistent log files.
func (p *Player) ClearLogs() error {
	p.logMu.Lock()
	defer p.logMu.Unlock()
	if err := p.clearPersistentLogs(); err != nil {
		return err
	}
	p.mu.Lock()
	p.logs = nil
	p.mu.Unlock()
	return nil
}
