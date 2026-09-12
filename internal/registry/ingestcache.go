package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

// The contract ingest cache: why a service's OpenAPI documents are parsed
// once per *contract revision* rather than once per Build.
//
// Build is the daemon's answer to "something in this service package
// changed", and the watcher calls it for any change anywhere under the
// package -- including api/docs/*.md. Parsing the contract is by far the
// most expensive thing Build does: on a 1.2 MB OpenAPI document, libopenapi
// builds a low model, a high model and a yaml node tree, and Sapien then
// normalizes all of it, which measured at roughly 350 MB of allocation per
// call. A daemon with two workspaces open, watching three such services
// while an agent wrote documentation into one of them at a file every
// second and a half, allocated 20 GB in four minutes and drove a 9 GB
// physical footprint; 61% of that allocation was this parse, re-running on
// contracts whose bytes had not changed since the previous pass.
//
// So the ingest is keyed by exactly what it is a function of: the service
// id, the concept list, and the sha256 of every contract file. Identical
// key means ingestContracts would return an identical result, so the
// previous result is returned instead. A contract edit changes a hash,
// misses the cache, and is re-parsed immediately -- the watcher stays as
// prompt as it ever was, which is the point of having it.
//
// What the cache deliberately does *not* do is keep every service's
// contract parsed, because a parsed contract is live heap for as long as it
// is held -- measured at ~73 MB for the 1.2 MB contract above -- and a
// daemon spends most of its life idle. Trading a churn problem for a
// permanent-footprint one is not a fix. So retention is earned, twice over:
//
//   - An ingest is only *kept* the second time the same contract bytes are
//     built. The first build records the key alone. Indexing a workspace at
//     startup, or a single `sapien service sync`, therefore retains
//     nothing; only a contract that is being rebuilt -- which is exactly
//     the burst this exists for -- costs memory.
//   - A kept ingest is dropped once ingestCacheTTL passes without use, and
//     the cache never holds more than ingestCacheMaxEntries services.
//     Expiry is lazy (it runs on use), so the Syncer's periodic git tick
//     also calls Sweep: a daemon that goes quiet does not sit on a parsed
//     contract until someone happens to touch a file.

const (
	// ingestCacheTTL is how long an unused parsed contract is kept. It has
	// to outlast the gap between two writes in a burst (an agent writing
	// docs pauses to think, a developer saves every few seconds) without
	// keeping contracts alive through the idle stretches in between; two
	// minutes covers the first and is short next to the daemon's 30 minute
	// idle timeout.
	ingestCacheTTL = 2 * time.Minute
	// ingestCacheMaxEntries bounds how many services can be held at once.
	// Four covers a burst that alternates between a couple of services
	// while keeping the worst case -- four parsed contracts -- comfortably
	// inside the daemon's 2 GiB soft heap limit even when every one of them
	// is as large as contracts get.
	ingestCacheMaxEntries = 4
)

// ingestEntry is what the cache holds for one service.
type ingestEntry struct {
	key  string
	used time.Time
	// val is the parsed contracts, or nil for a "built once" marker. See
	// the retention rules in this file's header comment: the value is only
	// attached the second time the same key is built.
	val *mergedContracts
}

// ingestCache caches contract ingests by service, keyed on the content of
// the files that produced them. The zero value is not usable; call
// newIngestCache.
//
// The cached *mergedContracts is shared, not copied, so every caller must
// treat it as read-only -- see Build, which copies merged.Warnings before
// appending coverage warnings to it for exactly this reason.
type ingestCache struct {
	mu      sync.Mutex
	entries map[string]*ingestEntry
	ttl     time.Duration
	max     int
	// now is time.Now, overridable in tests.
	now func() time.Time
}

func newIngestCache() *ingestCache {
	return &ingestCache{
		entries: map[string]*ingestEntry{},
		ttl:     ingestCacheTTL,
		max:     ingestCacheMaxEntries,
		now:     time.Now,
	}
}

// get returns the cached ingest for service, or nil when there is none
// under key (including when all that is held is a "built once" marker). A
// hit refreshes the entry's last-used time.
func (c *ingestCache) get(service, key string) *mergedContracts {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.expireLocked(now)

	e, ok := c.entries[service]
	if !ok || e.key != key {
		return nil
	}
	e.used = now
	return e.val
}

// put records that service's contracts were just ingested under key. The
// first time a key is seen it records only the key, so a one-off build
// keeps nothing; a second build of the same key attaches val and starts
// serving it.
func (c *ingestCache) put(service, key string, val *mergedContracts) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.expireLocked(now)

	if e, ok := c.entries[service]; ok && e.key == key {
		e.used = now
		e.val = val
		return
	}
	c.entries[service] = &ingestEntry{key: key, used: now}
	c.evictOldestLocked()
}

// Sweep drops expired entries. The cache expires lazily on use, which is
// enough while a workspace is being edited and useless once it is not: a
// daemon whose last change was ten minutes ago should not still be holding
// that contract parsed. The Syncer's periodic git tick calls this.
func (c *ingestCache) Sweep() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireLocked(c.now())
}

// expireLocked drops every entry unused for longer than the TTL.
func (c *ingestCache) expireLocked(now time.Time) {
	for service, e := range c.entries {
		if now.Sub(e.used) > c.ttl {
			delete(c.entries, service)
		}
	}
}

// evictOldestLocked drops least-recently-used entries until the cache is
// back within its size bound.
func (c *ingestCache) evictOldestLocked() {
	for len(c.entries) > c.max {
		oldest, oldestAt := "", time.Time{}
		for service, e := range c.entries {
			if oldest == "" || e.used.Before(oldestAt) {
				oldest, oldestAt = service, e.used
			}
		}
		delete(c.entries, oldest)
	}
}

// ingestKey is the identity of one contract ingest: everything
// ingestContracts reads. Two calls with equal keys produce equal results,
// which is what makes reusing one for the other sound.
func ingestKey(serviceID string, contractHashes map[string]string, concepts []string) string {
	rels := make([]string, 0, len(contractHashes))
	for rel := range contractHashes {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	h := sha256.New()
	h.Write([]byte(serviceID))
	h.Write([]byte{0})
	for _, rel := range rels {
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write([]byte(contractHashes[rel]))
		h.Write([]byte{0})
	}
	// Concepts feed the ingest (they become operation concepts), and their
	// order is service.yaml's, so it is part of the identity too.
	h.Write([]byte(strings.Join(concepts, "\x00")))
	return hex.EncodeToString(h.Sum(nil))
}
