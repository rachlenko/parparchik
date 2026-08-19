// Package cleanup prunes old catalog entries under configurable retention
// rules (max age, max total size, max version count per logical package),
// with an explicit dry-run Plan step before Execute actually deletes
// anything — mainly relevant to proxy repository caches (Task 24, which
// already has its own per-request CacheTTL staleness check; this package
// is about bounding a cache's total footprint and lifetime, not request
// freshness) and any hosted bucket a deployment wants to bound the size of.
//
// Implemented: Rule (the three retention thresholds), Plan (pure,
// deterministic — decides what to delete without touching storage), and
// Execute (calls objectstore.Store.DeleteObject and catalog.Catalog.Remove
// for each planned entry).
//
// Not implemented (see docs/plans/ Task 29): a scheduled GC goroutine
// wired into cmd/parparchik, and the per-bucket config surface
// (config.Bucket fields) a real deployment would need to set retention
// thresholds per repository the way PARPARCHIK_PROXY_REPOS already
// configures CacheTTL. That's a config-schema and default-policy decision
// or a real repository to test it against, not this package's job.
package cleanup

import (
	"sort"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
)

// Rule configures retention thresholds. The zero value prunes nothing —
// every threshold is opt-in.
type Rule struct {
	// MaxAge marks any entry older than this (relative to the `now` Plan
	// is given) for deletion. <= 0 disables the rule.
	MaxAge time.Duration

	// MaxTotalSize marks the oldest entries for deletion, one at a time,
	// until the remaining entries' total size is at or under this many
	// bytes. <= 0 disables the rule. This is not an unconditional
	// guarantee the budget is reached — see Plan's doc comment for the
	// unparseable-LastModified caveat, which can leave a bucket over
	// budget if enough of its entries can't be ordered by age.
	MaxTotalSize int64

	// MaxVersionCount marks the oldest entries within a GroupKey group for
	// deletion beyond this count, keeping the newest MaxVersionCount. <= 0
	// or a nil GroupKey disables the rule.
	//
	// GroupKey maps an entry to its logical package identity (e.g. an npm
	// package name across its published version tarballs) so "version
	// count" means something — this package has no ecosystem awareness of
	// its own to derive that grouping automatically. A caller composing
	// this with a format package's key parser (Task 14-22) can supply one;
	// without it, this rule is simply inert. An entry GroupKey maps to ""
	// is excluded from this rule entirely (not placed in some default
	// "ungrouped" bucket) — useful for a GroupKey that returns "" to mean
	// "I couldn't identify this entry's package", consistent with this
	// package's "don't guess" stance elsewhere.
	MaxVersionCount int
	GroupKey        func(catalog.Entry) string
}

// Plan is pure and deterministic: it decides which of entries a Rule would
// delete as of now, without touching storage or the catalog. An entry
// whose LastModified does not parse as RFC3339 (the format objectstore
// always writes — see objectstore.formatTime) can never itself be
// evicted, by any rule — this package can't order it by age, so it
// doesn't guess, the same "don't guess" stance taken elsewhere in this
// codebase (e.g. license.NormalizeSPDX). It is not otherwise invisible,
// though: MaxTotalSize still counts its bytes against the bucket's total
// when deciding how much needs to be evicted from the *other*, parseable
// entries, and MaxVersionCount still counts it as one of the group's
// members when deciding whether the group is over budget.
//
// The returned slice is sorted by Key for deterministic output; an entry
// matching more than one threshold appears once.
func (r Rule) Plan(entries []catalog.Entry, now time.Time) []catalog.Entry {
	victims := make(map[string]catalog.Entry)

	ages := parseAges(entries)

	if r.MaxAge > 0 {
		for _, e := range entries {
			t, ok := ages[e.Key]
			if !ok {
				continue
			}
			if now.Sub(t) > r.MaxAge {
				victims[e.Key] = e
			}
		}
	}

	if r.MaxTotalSize > 0 {
		for _, e := range r.oldestFirst(entries, ages) {
			victims[e.Key] = e
		}
	}

	if r.MaxVersionCount > 0 && r.GroupKey != nil {
		for _, e := range r.versionOverflow(entries, ages) {
			victims[e.Key] = e
		}
	}

	result := make([]catalog.Entry, 0, len(victims))
	for _, e := range victims {
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

// oldestFirst returns the prefix of entries (excluding those with an
// unparseable LastModified, which never count toward eviction — but their
// size still counts against the running total, so they can push
// parseable-but-newer entries into eviction) needed to bring the total
// size of entries with a known timestamp at or under r.MaxTotalSize,
// evicting the oldest-known-timestamp entries first.
func (r Rule) oldestFirst(entries []catalog.Entry, ages map[string]time.Time) []catalog.Entry {
	var total int64
	for _, e := range entries {
		total += e.Size
	}
	if total <= r.MaxTotalSize {
		return nil
	}

	sorted := make([]catalog.Entry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		ti, iok := ages[sorted[i].Key]
		tj, jok := ages[sorted[j].Key]
		if !iok || !jok {
			// Entries with an unparseable timestamp sort last (never
			// evicted first) — see Plan's doc comment.
			return iok && !jok
		}
		if ti.Equal(tj) {
			// RFC3339 (the format objectstore always writes) has
			// second-precision, so a real batch of objects PUT within the
			// same second ties here — break the tie on Key so eviction
			// order is deterministic and doesn't depend on sort.Slice's
			// unspecified behavior for equal elements.
			return sorted[i].Key < sorted[j].Key
		}
		return ti.Before(tj)
	})

	var victims []catalog.Entry
	for _, e := range sorted {
		if total <= r.MaxTotalSize {
			break
		}
		if _, ok := ages[e.Key]; !ok {
			continue
		}
		victims = append(victims, e)
		total -= e.Size
	}
	return victims
}

func (r Rule) versionOverflow(entries []catalog.Entry, ages map[string]time.Time) []catalog.Entry {
	groups := make(map[string][]catalog.Entry)
	for _, e := range entries {
		key := r.GroupKey(e)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], e)
	}

	var victims []catalog.Entry
	for _, group := range groups {
		if len(group) <= r.MaxVersionCount {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			ti, iok := ages[group[i].Key]
			tj, jok := ages[group[j].Key]
			if !iok || !jok {
				return iok && !jok
			}
			if ti.Equal(tj) {
				// See the identical tie-break comment in oldestFirst.
				return group[i].Key < group[j].Key
			}
			return ti.Before(tj)
		})
		overflow := len(group) - r.MaxVersionCount
		for _, e := range group {
			if _, ok := ages[e.Key]; !ok {
				continue
			}
			if overflow == 0 {
				break
			}
			victims = append(victims, e)
			overflow--
		}
	}
	return victims
}

func parseAges(entries []catalog.Entry) map[string]time.Time {
	ages := make(map[string]time.Time, len(entries))
	for _, e := range entries {
		if t, err := time.Parse(time.RFC3339, e.LastModified); err == nil {
			ages[e.Key] = t
		}
	}
	return ages
}
