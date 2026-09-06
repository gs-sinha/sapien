package registry

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// defaultDebounce is used when NewWatcher is given a non-positive debounce.
const defaultDebounce = 200 * time.Millisecond

// Change describes what changed in one debounced batch of filesystem events
// (PLAN §17).
type Change struct {
	// Services lists the names of services whose package changed.
	Services []string
	// Workspace lists which workspace-level areas changed: "flows",
	// "memories", "environments", and/or "workspace" (the workspace file
	// itself).
	Workspace []string
}

// watchTarget is what a watched directory maps back to.
type watchTarget struct {
	service bool // true: Services entry named name; false: Workspace entry named name
	name    string
}

// Watcher watches a workspace's service packages and workspace-level
// directories for changes, debouncing bursts of filesystem events into
// coalesced Change notifications (PLAN §17).
type Watcher struct {
	ws       *domain.Workspace
	packages map[string]*Package
	debounce time.Duration
	onChange func(Change)

	fsw *fsnotify.Watcher

	// dirIndex, pendingServices, and pendingWorkspace are only ever touched
	// from the single goroutine started by Start, so they need no lock.
	dirIndex         map[string]watchTarget
	pendingServices  map[string]bool
	pendingWorkspace map[string]bool

	closeOnce sync.Once
}

// NewWatcher constructs a Watcher for ws's service packages (name -> Package,
// as discovered by DiscoverPackage) plus its flows/, memories/,
// environments/ directories and workspace file. debounce <= 0 uses a 200ms
// default. Call Start to begin watching.
func NewWatcher(ws *domain.Workspace, packages map[string]*Package, debounce time.Duration, onChange func(Change)) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "creating filesystem watcher")
	}
	if debounce <= 0 {
		debounce = defaultDebounce
	}
	return &Watcher{
		ws:       ws,
		packages: packages,
		debounce: debounce,
		onChange: onChange,
		fsw:      fsw,
	}, nil
}

// Start begins watching: every service package directory (recursively --
// subdirectories created later are picked up automatically), ws's flows/,
// memories/, environments/ directories (recursively), and ws's own directory
// (to catch changes to the workspace file). It returns once the initial set
// of watches is established; events are then delivered to onChange,
// debounced/coalesced per a 200ms (by default) window, until ctx is done or
// Close is called.
func (w *Watcher) Start(ctx context.Context) error {
	w.dirIndex = map[string]watchTarget{}
	w.pendingServices = map[string]bool{}
	w.pendingWorkspace = map[string]bool{}

	for name, pkg := range w.packages {
		if pkg == nil {
			continue
		}
		if err := w.addTree(pkg.Dir, watchTarget{service: true, name: name}); err != nil {
			return err
		}
	}

	for _, wsDir := range []struct{ dir, name string }{
		{filepath.Join(w.ws.Dir, domain.FlowsDir), "flows"},
		{filepath.Join(w.ws.Dir, domain.MemoriesDir), "memories"},
		{filepath.Join(w.ws.Dir, domain.EnvironmentsDir), "environments"},
	} {
		if isDir(wsDir.dir) {
			if err := w.addTree(wsDir.dir, watchTarget{name: wsDir.name}); err != nil {
				return err
			}
		}
	}

	// The workspace root itself, non-recursively, purely to notice changes
	// to the workspace file (a direct child of it).
	if err := w.fsw.Add(w.ws.Dir); err != nil {
		return errs.Wrap(errs.Internal, err, "watching %s", w.ws.Dir)
	}
	w.dirIndex[cleanPath(w.ws.Dir)] = watchTarget{name: "workspace"}

	go w.loop(ctx)
	return nil
}

// Close stops the watcher. Safe to call more than once.
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		err = w.fsw.Close()
	})
	return err
}

// loop is the Watcher's single goroutine: it owns dirIndex and the pending
// sets, reads raw fsnotify events, and fires debounced Change callbacks.
func (w *Watcher) loop(ctx context.Context) {
	var timer *time.Timer
	var timerC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(ev)
			if timer == nil {
				timer = time.NewTimer(w.debounce)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(w.debounce)
			}
			timerC = timer.C

		case <-timerC:
			w.flush()
			timerC = nil

		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			// Best effort: a single watch error (e.g. a removed directory)
			// should not stop the loop.
		}
	}
}

// handleEvent updates the pending sets for one raw fsnotify event, and
// dynamically extends the watch tree when a new directory appears inside an
// already-watched tree.
func (w *Watcher) handleEvent(ev fsnotify.Event) {
	path := cleanPath(ev.Name)
	base := filepath.Base(path)
	if isIgnoredFileName(base) {
		return
	}

	if ev.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			if !isIgnoredDirName(base) {
				if parent, ok := w.dirIndex[cleanPath(filepath.Dir(path))]; ok {
					_ = w.addTree(path, parent)
					w.markPending(parent)
				}
			}
			return
		}
	}

	dir := cleanPath(filepath.Dir(path))
	target, ok := w.dirIndex[dir]
	if !ok {
		return
	}

	// The workspace root is watched only to notice the workspace file
	// itself; ignore any other direct child of it.
	if !target.service && target.name == "workspace" && path != cleanPath(w.ws.File) {
		return
	}

	w.markPending(target)
}

func (w *Watcher) markPending(t watchTarget) {
	if t.service {
		w.pendingServices[t.name] = true
	} else {
		w.pendingWorkspace[t.name] = true
	}
}

func (w *Watcher) flush() {
	if len(w.pendingServices) == 0 && len(w.pendingWorkspace) == 0 {
		return
	}
	ch := Change{}
	for name := range w.pendingServices {
		ch.Services = append(ch.Services, name)
	}
	for name := range w.pendingWorkspace {
		ch.Workspace = append(ch.Workspace, name)
	}
	sort.Strings(ch.Services)
	sort.Strings(ch.Workspace)

	w.pendingServices = map[string]bool{}
	w.pendingWorkspace = map[string]bool{}

	w.onChange(ch)
}

// addTree recursively adds dir and every non-ignored subdirectory to the
// fsnotify watch set, recording target for each. A missing dir is a silent
// no-op (not an error).
func (w *Watcher) addTree(dir string, target watchTarget) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != dir && isIgnoredDirName(d.Name()) {
			return filepath.SkipDir
		}
		if err := w.fsw.Add(path); err != nil {
			return errs.Wrap(errs.Internal, err, "watching %s", path)
		}
		w.dirIndex[cleanPath(path)] = target
		return nil
	})
}

// isIgnoredDirName reports whether a directory should never be watched:
// .sapien/ and any other hidden (dot-prefixed) directory.
func isIgnoredDirName(name string) bool {
	return strings.HasPrefix(name, ".")
}

// isIgnoredFileName reports whether a changed file name should never
// trigger a Change: editor swap/backup files and vim's atomic-rename probe.
func isIgnoredFileName(name string) bool {
	if name == "4913" {
		return true
	}
	if strings.HasSuffix(name, "~") {
		return true
	}
	if strings.HasPrefix(name, ".#") {
		return true
	}
	if strings.HasSuffix(name, ".swp") {
		return true
	}
	return false
}

func cleanPath(p string) string {
	return filepath.Clean(p)
}
