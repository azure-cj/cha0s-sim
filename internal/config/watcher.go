package config

import (
	"log"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Store struct {
	current atomic.Pointer[Config]
	watcher *fsnotify.Watcher
	path    string
	base    string
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

	// Watch the parent DIRECTORY rather than the file itself: on Windows a
	// file-level watch does not surface the Create event that a rename-temp-over
	// save produces, and an atomic save invalidates the original file handle.
	// A directory watch sees both delete+recreate and rename-based saves.
	if err := watcher.Add(filepath.Dir(path)); err != nil {
		watcher.Close()
		return nil, err
	}

	s := &Store{
		watcher: watcher,
		path:    path,
		base:    filepath.Base(path),
	}
	s.current.Store(cfg)

	go s.watchLoop()

	return s, nil
}

// NewEmptyStore creates a Store for a config path that does not exist yet (or
// is empty), tolerating a missing file — the "first run" scenario the desktop
// app reaches when no chaos.yaml has ever been written. It holds an empty
// in-memory config and deliberately starts NO file watcher: there is nothing to
// watch. After the first rule is persisted, the file is created; live reloads
// still won't trigger until the app restarts or reloads the store, which is the
// documented behavior of this constructor.
func NewEmptyStore(path string) (*Store, error) {
	s := &Store{
		path: path,
	}
	s.current.Store(&Config{})
	return s, nil
}

func (s *Store) Current() *Config {
	return s.current.Load()
}

func (s *Store) Close() error {
	if s.watcher == nil {
		return nil
	}
	return s.watcher.Close()
}

// reload reads and parses the config, retrying briefly on transient errors.
// A rename-based save may leave the watcher's read slightly ahead of the rename
// completing on Windows, surfacing as a short-lived sharing violation; a few
// retries absorb that instead of incorrectly keeping a stale config.
func (s *Store) reload() (*Config, error) {
	const attempts = 5
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(10 * time.Millisecond)
		}
		var cfg *Config
		cfg, err = Load(s.path)
		if err == nil {
			return cfg, nil
		}
	}
	return nil, err
}

func (s *Store) watchLoop() {
	for {
		select {
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			// We watch the whole directory, so first narrow to the config file
			// itself; then react to Write, Create, and Rename ops. Together these
			// cover in-place writes, delete+recreate saves, and atomic
			// temp-file-rename saves. All of them guarantee the file at path is a
			// complete, loadable snapshot by the time we read it (rename makes the
			// new content visible atomically), so a reload never observes a
			// partially-written file. Chmod-only noise and unrelated files are
			// ignored.
			if filepath.Base(event.Name) != s.base {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			newCfg, err := s.reload()
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
