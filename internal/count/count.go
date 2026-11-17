// SPDX-License-Identifier: GPL-3.0-or-later

// Package count keeps the counters in memory between two writes to the
// database: how many answers per domain, hour and result.
package count

import (
	"sort"
	"sync"
	"time"
)

// Other is where answers are counted once an hour has as many different
// domains as it may hold - so that somebody asking for a million made-up
// domains fills one row, not a million.
const Other = "(other)"

// Row is one counter.
type Row struct {
	Domain  string
	Hour    time.Time // the start of the hour, UTC
	Result  string
	Answers int64
}

type key struct {
	domain string
	hour   int64 // unix time of the start of the hour
	result string
}

// Counter counts. It is safe to use from several goroutines.
type Counter struct {
	mu   sync.Mutex
	rows map[key]int64
	// the different domains seen in each hour since the program started
	seen  map[int64]map[string]struct{}
	limit int
}

// New returns a Counter that keeps at most limit different domains per
// hour; the rest of that hour goes to Other.
func New(limit int) *Counter {
	return &Counter{rows: map[key]int64{}, seen: map[int64]map[string]struct{}{}, limit: limit}
}

// Add counts one answer.
func (c *Counter) Add(domain, result string, when time.Time) {
	hour := when.Unix() - when.Unix()%3600
	c.mu.Lock()
	defer c.mu.Unlock()
	domains := c.seen[hour]
	if domains == nil {
		domains = map[string]struct{}{}
		c.seen[hour] = domains
		// hours long gone need no remembering
		for h := range c.seen {
			if h < hour-6*3600 {
				delete(c.seen, h)
			}
		}
	}
	if _, known := domains[domain]; !known {
		if len(domains) >= c.limit {
			domain = Other
		} else {
			domains[domain] = struct{}{}
		}
	}
	c.rows[key{domain, hour, result}]++
}

// Take hands out what was counted and starts over. The rows come in a
// fixed order, so that two programs writing the same rows to the
// database at the same time take their locks in the same order.
func (c *Counter) Take() []Row {
	c.mu.Lock()
	rows := c.rows
	c.rows = map[key]int64{}
	c.mu.Unlock()
	out := make([]Row, 0, len(rows))
	for k, n := range rows {
		out = append(out, Row{k.domain, time.Unix(k.hour, 0).UTC(), k.result, n})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Domain != b.Domain {
			return a.Domain < b.Domain
		}
		if !a.Hour.Equal(b.Hour) {
			return a.Hour.Before(b.Hour)
		}
		return a.Result < b.Result
	})
	return out
}

// Return puts rows back that could not be written, to be tried again
// with the next ones.
func (c *Counter) Return(rows []Row) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range rows {
		c.rows[key{r.Domain, r.Hour.Unix(), r.Result}] += r.Answers
	}
}

// Held says how many counters are waiting to be written.
func (c *Counter) Held() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.rows)
}
