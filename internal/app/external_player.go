package app

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/freelxs/jstv-player/internal/externalplayer"
)

// PickCustomPlayer shows the native file dialog and returns the chosen path,
// or "" when the dialog is canceled.
func (p *Player) PickCustomPlayer(ctx context.Context) (string, error) {
	if p.webMode.Load() {
		return "", errors.New("网页模式不能选择本机播放器")
	}
	opts := externalplayer.CustomPlayerDialogOptions(runtime.GOOS)
	opts.Parent = mygo.CallerWindow(ctx)
	if cur := strings.TrimSpace(p.GetSettings().CustomPlayer); cur != "" {
		opts.DefaultPath = cur
	}
	paths, err := mygo.Dialog.Open(opts)
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

func normalizeCustomPlayer(s *Settings) error {
	s.CustomPlayer = strings.TrimSpace(s.CustomPlayer)
	s.CustomArgs = strings.TrimSpace(s.CustomArgs)
	if s.Player != "custom" {
		return nil
	}
	if err := externalplayer.ValidateCustomPlayer(s.CustomPlayer); err != nil {
		return err
	}
	s.CustomPlayer = filepath.Clean(s.CustomPlayer)
	if _, err := externalplayer.CustomPlayerArgv(s.CustomArgs, "http://127.0.0.1/"); err != nil {
		return err
	}
	return nil
}
