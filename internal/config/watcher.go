package config

import (
	"log"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"
)

type Store struct {
	current atomic.Pointer[Config]
	watcher *fsnotify.Watcher
	path    string
}

func NewStore(path string) (*Store, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if err := watcher.Add(path); err != nil {
		watcher.Close()
		return nil, err
	}

	s := &Store{
		watcher: watcher,
		path:    path,
	}
	s.current.Store(cfg)

	go s.watchLoop()

	return s, nil
}

func (s *Store) Current() *Config {
	return s.current.Load()
}

func (s *Store) Close() error {
	return s.watcher.Close()
}

func (s *Store) watchLoop() {
	for {
		select {
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			// Some editors save via delete+recreate rather than an in-place write,
			// so react to both Write and Create ops. Remove/Rename/Chmod are ignored
			// to avoid reacting to noise.
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			// Some editors' atomic-save pattern (renaming a temp file over the
			// original) can cause the watcher to lose the file handle. A future
			// improvement could defensively re-add the watch path after any event,
			// but that is intentionally not implemented here yet.
			newCfg, err := Load(s.path)
			if err != nil {
				log.Printf("config reload failed, keeping previous config: %v", err)
				continue
			}
			s.current.Store(newCfg)
			log.Printf("config reloaded successfully: %d rules active", len(newCfg.Rules))
		case err, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("config watcher error: %v", err)
		}
	}
}
