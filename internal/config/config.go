// Package config loads Sapien's engine-level settings (PLAN.md §16, §18,
// §21): whether/how semantic search is enabled, git managed-clone
// behavior, the daemon's idle-exit timeout and heap ceiling, and where
// `sapien friction send` posts a queued friction report.
//
// Settings live in two YAML files, merged with the workspace winning over
// the user level:
//
//   - user:      ~/.sapien/config.yaml (or $SAPIEN_CONFIG, if set)
//   - workspace: <workspace>/.sapien/config.yaml
//
// Both files may carry other top-level keys this package does not know
// about -- most notably `mcp:`, read separately by internal/mcp.Permissions
// -- which are silently ignored here, exactly as this package's own keys
// (`semantic:`, `git:`, `daemon:`, `friction:`) are ignored by that
// package's own loader. Neither loader declares the other's keys, and
// neither enables yaml.v3's "reject unknown fields" mode, so the two
// coexist in one file without either needing to know the other's schema.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// configFileName is the well-known config file name at both the user and
// workspace level.
const configFileName = "config.yaml"

// Default durations (PLAN §18, §21), used whenever the corresponding string
// field is empty or fails to parse. Load itself rejects an unparsable
// non-empty value before this ever needs to substitute one silently.
const (
	defaultGitSyncInterval    = 10 * time.Minute
	defaultDaemonIdleTimeout  = 30 * time.Minute
	defaultGitSyncIntervalStr = "10m"
	defaultDaemonIdleTimeoutS = "30m"
)

// defaultDaemonMemoryLimit is the daemon's default soft heap ceiling
// (GOMEMLIMIT), applied by `sapien serve` unless the operator overrides it
// in config or in the environment.
//
// The number is chosen from what the daemon actually needs. Its steady
// state is small -- the catalog lives in SQLite, not on the heap, so an
// idle daemon serving several workspaces sits in the tens of megabytes.
// The peak is transient and comes from parsing one service's OpenAPI
// contract, which for a 1.2 MB document costs a few hundred megabytes of
// live heap while it runs. 2 GiB leaves room for several of those at once
// (two workspaces re-indexing concurrently, say) and still stops a runaway
// well short of the 18 GB footprint that made this a bug. It is a *soft*
// limit: Go never fails an allocation because of it, it only collects more
// aggressively as the heap approaches it, so an unusually large workspace
// gets slower rather than broken -- and can raise the number.
const defaultDaemonMemoryLimit = "2GiB"

// Semantic configures the optional semantic-search layer (internal/semantic,
// PLAN §16). Off by default.
type Semantic struct {
	Enabled bool   `yaml:"enabled"`
	Kind    string `yaml:"kind"` // "openai" | "ollama"
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
	// APIKey is either the literal key, or "${env.NAME}"/"$NAME" to read it
	// from an environment variable at use time (internal/semantic.NewHTTPEmbedder
	// resolves this the same way; config never resolves it itself).
	APIKey    string `yaml:"api_key"`
	BatchSize int    `yaml:"batch_size"`
}

// Git configures managed git clones (internal/gitsrc, PLAN §18).
type Git struct {
	// CacheDir is where managed clones live. Empty defers to gitsrc's own
	// default ("~/.sapien/repos").
	CacheDir string `yaml:"cache_dir"`
	// SyncInterval is a Go duration string; empty defaults to 10m.
	SyncInterval string `yaml:"sync_interval"`
	// Timeout is a Go duration string bounding each git invocation; empty
	// defers to gitsrc's own default (60s).
	Timeout string `yaml:"timeout"`
}

// Daemon configures `sapien serve` (PLAN §4, §21).
type Daemon struct {
	// IdleTimeout is a Go duration string; empty defaults to 30m.
	IdleTimeout string `yaml:"idle_timeout"`
	// MemoryLimit is the daemon's soft heap ceiling, written the way Go's
	// own GOMEMLIMIT is ("2GiB", "512MiB", "1073741824"). Empty defaults to
	// 2GiB; "0" or "off" leaves the runtime unbounded, as it was before
	// this setting existed. See MemoryLimitBytes.
	MemoryLimit string `yaml:"memory_limit"`
}

// Friction configures where `sapien friction send` posts a queued report
// (internal/friction): the repository a GitHub Discussion is created on,
// and which discussion category it lands in. Reports are never sent
// automatically -- this only decides the destination once a human runs
// `send`.
type Friction struct {
	// Repo is "owner/name". Empty defaults to "gs-sinha/sapien".
	Repo string `yaml:"repo"`
	// Category is a GitHub Discussion category name or slug on Repo. Empty
	// defaults to "General".
	Category string `yaml:"category"`
}

// defaultFrictionRepo and defaultFrictionCategory are Friction's defaults:
// this repository's own Discussions, "General" category, so `sapien
// friction send` works out of the box for reports about Sapien itself.
const (
	defaultFrictionRepo     = "gs-sinha/sapien"
	defaultFrictionCategory = "General"
)

// Config is Sapien's merged engine-level configuration.
type Config struct {
	Semantic Semantic `yaml:"semantic"`
	Git      Git      `yaml:"git"`
	Daemon   Daemon   `yaml:"daemon"`
	Friction Friction `yaml:"friction"`
}

// Defaults returns the configuration Load would produce if neither the
// user nor the workspace config file existed.
func Defaults() Config {
	return Config{
		Git:      Git{SyncInterval: defaultGitSyncIntervalStr},
		Daemon:   Daemon{IdleTimeout: defaultDaemonIdleTimeoutS, MemoryLimit: defaultDaemonMemoryLimit},
		Friction: Friction{Repo: defaultFrictionRepo, Category: defaultFrictionCategory},
	}
}

// SyncIntervalDuration parses g.SyncInterval, falling back to 10 minutes
// when it is empty or (should Load not have been used to construct g)
// unparsable.
func (g Git) SyncIntervalDuration() time.Duration {
	if d, err := time.ParseDuration(g.SyncInterval); err == nil && d > 0 {
		return d
	}
	return defaultGitSyncInterval
}

// TimeoutDuration parses g.Timeout, returning 0 (defer to gitsrc's own
// default) when it is empty or unparsable.
func (g Git) TimeoutDuration() time.Duration {
	if d, err := time.ParseDuration(g.Timeout); err == nil && d > 0 {
		return d
	}
	return 0
}

// IdleTimeoutDuration parses d.IdleTimeout, falling back to 30 minutes when
// it is empty or unparsable.
func (d Daemon) IdleTimeoutDuration() time.Duration {
	if dur, err := time.ParseDuration(d.IdleTimeout); err == nil && dur > 0 {
		return dur
	}
	return defaultDaemonIdleTimeout
}

// MemoryLimitBytes parses d.MemoryLimit into a byte count for
// runtime/debug.SetMemoryLimit, returning 0 for "no limit" -- which is what
// an explicit "0"/"off" means, and also what an unparsable value falls back
// to (Load rejects those before they get here, so this only matters for a
// Daemon built by hand). An empty value takes the 2 GiB default.
func (d Daemon) MemoryLimitBytes() int64 {
	if d.MemoryLimit == "" {
		n, _ := parseByteSize(defaultDaemonMemoryLimit)
		return n
	}
	n, err := parseByteSize(d.MemoryLimit)
	if err != nil {
		return 0
	}
	return n
}

// byteSuffixes are the units parseByteSize accepts, longest first so "MiB"
// is matched before "B". They are exactly the ones Go's own GOMEMLIMIT
// accepts, so the same string works in either place.
var byteSuffixes = []struct {
	suffix string
	scale  int64
}{
	{"KiB", 1 << 10},
	{"MiB", 1 << 20},
	{"GiB", 1 << 30},
	{"TiB", 1 << 40},
	{"B", 1},
}

// parseByteSize parses a GOMEMLIMIT-style byte size: a non-negative integer
// with an optional B/KiB/MiB/GiB/TiB suffix, or the words "off"/"none" for
// no limit (0).
func parseByteSize(s string) (int64, error) {
	t := strings.TrimSpace(s)
	switch strings.ToLower(t) {
	case "", "0", "off", "none", "unlimited":
		return 0, nil
	}
	for _, u := range byteSuffixes {
		if !strings.HasSuffix(t, u.suffix) {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(t, u.suffix)), 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("want an integer followed by one of B, KiB, MiB, GiB, TiB")
		}
		return n * u.scale, nil
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("want an integer followed by one of B, KiB, MiB, GiB, TiB")
	}
	return n, nil
}

// UserPath returns the user-level config file path: $SAPIEN_CONFIG if set
// and non-empty, else "~/.sapien/config.yaml" (falling back to a temp-dir
// path if the home directory cannot be determined, so callers always get a
// usable, if unwritable-in-practice, path rather than an error).
func UserPath() string {
	if p := os.Getenv("SAPIEN_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), domain.WorkspaceStateDir, configFileName)
	}
	return filepath.Join(home, domain.WorkspaceStateDir, configFileName)
}

// WorkspacePath returns ws's workspace-level config file path,
// "<ws.Dir>/.sapien/config.yaml".
func WorkspacePath(ws *domain.Workspace) string {
	return filepath.Join(ws.Dir, domain.WorkspaceStateDir, configFileName)
}

// Load reads the user-level config file, then (when ws is non-nil) merges
// the workspace-level config file over it field-by-field -- a field left
// unset (absent from the YAML) in a later file never resets a value set by
// an earlier one; the workspace only overrides what it actually mentions. A
// missing file at either level is not an error: Load simply keeps
// whatever it already has (Defaults() to start). An invalid duration string
// anywhere is reported as errs.Invalid, naming the offending key.
func Load(ws *domain.Workspace) (Config, error) {
	cfg := Defaults()

	if err := mergeFile(&cfg, UserPath()); err != nil {
		return Config{}, err
	}
	if ws != nil {
		if err := mergeFile(&cfg, WorkspacePath(ws)); err != nil {
			return Config{}, err
		}
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// rawSemantic/rawGit/rawDaemon mirror Semantic/Git/Daemon with optional
// (pointer) fields, so a partial YAML document only overrides the fields it
// actually sets (mirroring internal/mcp/permissions.go's rawPermissions
// pattern).
type rawSemantic struct {
	Enabled   *bool   `yaml:"enabled"`
	Kind      *string `yaml:"kind"`
	BaseURL   *string `yaml:"base_url"`
	Model     *string `yaml:"model"`
	APIKey    *string `yaml:"api_key"`
	BatchSize *int    `yaml:"batch_size"`
}

type rawGit struct {
	CacheDir     *string `yaml:"cache_dir"`
	SyncInterval *string `yaml:"sync_interval"`
	Timeout      *string `yaml:"timeout"`
}

type rawDaemon struct {
	IdleTimeout *string `yaml:"idle_timeout"`
	MemoryLimit *string `yaml:"memory_limit"`
}

type rawFriction struct {
	Repo     *string `yaml:"repo"`
	Category *string `yaml:"category"`
}

// rawConfig is the shape of one config.yaml. Unknown top-level keys (`mcp:`
// chief among them) are simply not declared here, so yaml.v3 -- which
// ignores keys it has no destination field for, unless KnownFields(true) is
// set (it never is, here or in internal/mcp) -- leaves them alone.
type rawConfig struct {
	Semantic rawSemantic `yaml:"semantic"`
	Git      rawGit      `yaml:"git"`
	Daemon   rawDaemon   `yaml:"daemon"`
	Friction rawFriction `yaml:"friction"`
}

// mergeFile reads path (a no-op, not an error, if it does not exist) and
// overlays its explicitly-set fields onto cfg.
func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errs.Wrap(errs.Internal, err, "config: reading %s", path)
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return errs.Wrap(errs.Invalid, err, "config: parsing %s", path).WithDetail("file", path)
	}

	applyRaw(cfg, raw)
	return nil
}

// applyRaw overlays every non-nil field of raw onto cfg, in place.
func applyRaw(cfg *Config, raw rawConfig) {
	s, g, d := &cfg.Semantic, &cfg.Git, &cfg.Daemon

	if raw.Semantic.Enabled != nil {
		s.Enabled = *raw.Semantic.Enabled
	}
	if raw.Semantic.Kind != nil {
		s.Kind = *raw.Semantic.Kind
	}
	if raw.Semantic.BaseURL != nil {
		s.BaseURL = *raw.Semantic.BaseURL
	}
	if raw.Semantic.Model != nil {
		s.Model = *raw.Semantic.Model
	}
	if raw.Semantic.APIKey != nil {
		s.APIKey = *raw.Semantic.APIKey
	}
	if raw.Semantic.BatchSize != nil {
		s.BatchSize = *raw.Semantic.BatchSize
	}

	if raw.Git.CacheDir != nil {
		g.CacheDir = *raw.Git.CacheDir
	}
	if raw.Git.SyncInterval != nil {
		g.SyncInterval = *raw.Git.SyncInterval
	}
	if raw.Git.Timeout != nil {
		g.Timeout = *raw.Git.Timeout
	}

	if raw.Daemon.IdleTimeout != nil {
		d.IdleTimeout = *raw.Daemon.IdleTimeout
	}
	if raw.Daemon.MemoryLimit != nil {
		d.MemoryLimit = *raw.Daemon.MemoryLimit
	}

	f := &cfg.Friction
	if raw.Friction.Repo != nil {
		f.Repo = *raw.Friction.Repo
	}
	if raw.Friction.Category != nil {
		f.Category = *raw.Friction.Category
	}
}

// validate rejects an unparsable, non-empty duration string in any of the
// three duration fields, naming the offending key (both in the message and
// as a "key" detail) so a CLI or log line can point straight at it.
func validate(cfg Config) error {
	checks := []struct {
		key   string
		value string
	}{
		{"git.sync_interval", cfg.Git.SyncInterval},
		{"git.timeout", cfg.Git.Timeout},
		{"daemon.idle_timeout", cfg.Daemon.IdleTimeout},
	}
	for _, c := range checks {
		if c.value == "" {
			continue
		}
		if _, err := time.ParseDuration(c.value); err != nil {
			return errs.New(errs.Invalid, "config: invalid %s %q: %v", c.key, c.value, err).
				WithDetail("key", c.key).
				WithDetail("value", c.value)
		}
	}
	if _, err := parseByteSize(cfg.Daemon.MemoryLimit); err != nil {
		return errs.New(errs.Invalid, "config: invalid daemon.memory_limit %q: %v", cfg.Daemon.MemoryLimit, err).
			WithDetail("key", "daemon.memory_limit").
			WithDetail("value", cfg.Daemon.MemoryLimit)
	}
	return nil
}
