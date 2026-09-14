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
	"github.com/gs-sinha/sapien/internal/workspace"
)

// defaultDebounce is used when NewWatcher is given a non-positive debounce.
const defaultDebounce = 200 * time.Millisecond

// maxDelayFactor turns the debounce into a ceiling on how long a flush can
// be deferred: debounce * maxDelayFactor, so the 200ms default caps a
// deferral at five seconds.
//
// The debounce is a trailing one -- every event restarts it -- which is
// what makes a burst of writes collapse into a single Change. Left
// uncapped, that is also a starvation bug: a writer that never pauses for a
// whole debounce window (a git checkout unpacking a tree, a build step
// regenerating a contract, an editor autosaving into a large file) resets
// the timer indefinitely and indexing never happens at all, for as long as
// the writing lasts. The cap bounds that: however long the stream runs, the
// watcher still flushes what it has seen every five seconds.
//
// Five seconds is deliberately generous. The cap only ever *adds* index
// passes -- passes the quiet-period debounce would not have run -- and each
// one costs a resync, so a short cap would reintroduce exactly the churn
// this work is here to remove. It is a liveness floor for a file still
// being written, not a freshness target: the settled, final state is always
// indexed one debounce after the writing stops, as before.
const maxDelayFactor = 25

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
	// maxDelay bounds how long a flush can be deferred by a stream of
	// events that never pauses for a full debounce. See maxDelayFactor.
	maxDelay time.Duration
	onChange func(Change)

	fsw *fsnotify.Watcher

	// dirIndex, pendingServices, pendingWorkspace and firstPending are only
	// ever touched from the single goroutine started by Start, so they need
	// no lock.
	dirIndex         map[string]watchTarget
	pendingServices  map[string]bool
	pendingWorkspace map[string]bool
	// firstPending is when the oldest unflushed change was seen, or the
	// zero time when nothing is pending; maxDelay is measured from it.
	firstPending time.Time

	closeOnce sync.Once
}

// NewWatcher constructs a Watcher for ws's service packages (name -> Package,
// as discovered by DiscoverPackage) plus its flows/, memories/,
// environments/ and local/{flows,memories} directories and workspace file.
// debounce <= 0 uses a 200ms default. Call Start to begin watching.
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
		maxDelay: debounce * maxDelayFactor,
		onChange: onChange,
		fsw:      fsw,
	}, nil
}

// Start begins watching: every service package directory (recursively --
// subdirectories created later are picked up automatically), ws's flows/,
// memories/, environments/ directories and the local tier's
// local/{flows,memories} (recursively), and ws's own directory (to catch
// changes to the workspace file). It returns once the initial set of
// watches is established; events are then delivered to onChange,
// debounced/coalesced per a 200ms (by default) window of quiet -- and, when
// the writing never goes quiet, at least once per debounce*maxDelayFactor
// -- until ctx is done or Close is called.
//
// The local tier reports as the same areas as the team tier ("flows",
// "memories"): the engine reindexes both tiers of an area together, and
// nothing downstream needs to know which one a file was in.
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

	// The local tier is created lazily, on the first local write, so the
	// daemon usually starts before local/ exists. A directory that is not
	// there when the watch is set up is never watched (addTree skips it,
	// and a directory created later directly under the workspace root
	// inherits the root's "workspace" target, whose only job is the
	// workspace file). Creating the tier here, self-ignoring, is the
	// smallest thing that makes the first local flow's later edits
	// observable: it is invisible to git, and Open already creates .sapien/
	// on the same reasoning.
	if err := workspace.EnsureLocalDir(w.ws); err != nil {
		return err
	}

	for _, wsDir := range []struct{ dir, name string }{
		{filepath.Join(w.ws.Dir, domain.FlowsDir), "flows"},
		{filepath.Join(w.ws.Dir, domain.MemoriesDir), "memories"},
		{filepath.Join(w.ws.Dir, domain.EnvironmentsDir), "environments"},
		{workspace.LocalFlowsDir(w.ws), "flows"},
		{workspace.LocalMemoriesDir(w.ws), "memories"},
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
			d := w.nextFlushIn(time.Now())
			if timer == nil {
				timer = time.NewTimer(d)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(d)
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
	if w.firstPending.IsZero() {
		w.firstPending = time.Now()
	}
	if t.service {
		w.pendingServices[t.name] = true
	} else {
		w.pendingWorkspace[t.name] = true
	}
}

// nextFlushIn is how long the debounce timer should run for after an event
// at now: a full debounce window, unless that would push the oldest pending
// change past maxDelay, in which case only what is left of that budget (and
// never less than zero, which fires the timer immediately).
func (w *Watcher) nextFlushIn(now time.Time) time.Duration {
	d := w.debounce
	if w.firstPending.IsZero() {
		return d
	}
	if remaining := w.maxDelay - now.Sub(w.firstPending); remaining < d {
		d = remaining
	}
	if d < 0 {
		d = 0
	}
	return d
}

func (w *Watcher) flush() {
	w.firstPending = time.Time{}
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
