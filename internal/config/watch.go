package config

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Change describes one configuration file change observed by Watch.
type Change struct {
	Doc   string // inference / workspace / tools / sandbox
	Layer Layer  // user / project
	Path  string
}

// Watch reports configuration file changes in the user and project
// layers. Events are debounced: rapid sequences for the same file are
// coalesced into one Change. The returned stop function closes the
// watcher; fn must not block for long (deliveries happen on the watch
// goroutine).
func (m *Manager) Watch(ctx context.Context, fn func(Change)) (stop func(), err error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	dirs := map[string]Layer{
		m.userDir: LayerUser,
	}
	if m.projectDir != "" {
		dirs[m.projectDir] = LayerProject
	}
	for dir := range dirs {
		if err := watcher.Add(dir); err != nil {
			_ = watcher.Close()
			return nil, fmt.Errorf("config watch: %w", err)
		}
	}

	const debounce = 100 * time.Millisecond
	var pending *Change
	var debounceTimer *time.Timer
	var debounceC <-chan time.Time
	flush := func() {
		if pending != nil {
			fn(*pending)
			pending = nil
			debounceC = nil
		}
	}
	arm := func() {
		if debounceTimer == nil {
			debounceTimer = time.NewTimer(debounce)
			debounceC = debounceTimer.C
		} else {
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			debounceTimer.Reset(debounce)
		}
	}

	go func() {
		defer watcher.Close()
		defer func() {
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
		}()
		for {
			select {
			case <-ctx.Done():
				flush()
				return
			case <-debounceC:
				flush()
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
					continue
				}
				dir, file := filepath.Split(event.Name)
				layer, ok := dirs[filepath.Clean(dir)]
				if !ok {
					continue
				}
				doc, ok := documentFor(file)
				if !ok {
					continue
				}
				pending = &Change{Doc: doc, Layer: layer, Path: event.Name}
				arm()
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	return func() { _ = watcher.Close() }, nil
}

func documentFor(file string) (string, bool) {
	name := strings.TrimSuffix(filepath.Base(file), ".yaml")
	switch name {
	case "inference", "workspace", "tools", "sandbox":
		return name, true
	default:
		return "", false
	}
}
