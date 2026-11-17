// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// driver: the PostgreSQL driver. It understands the usual connection
// strings and URLs, the sslmode/sslcert/sslkey/sslrootcert settings
// among them.
const driver = "pgx"

// metricsText is what the program has done, in the Prometheus text format.
func metricsText(c *counters, held int) string {
	connected := 0
	if c.connected.Load() {
		connected = 1
	}
	var b strings.Builder
	one := func(name, kind, help string, value int64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n%s %d\n", name, help, name, kind, name, value)
	}
	one("dnsinfo_connected", "gauge", "1 when the database is there.", int64(connected))
	one("dnsinfo_streams", "gauge", "Name servers sending their answers right now.", c.connections.Load())
	one("dnsinfo_last_success_timestamp_seconds", "gauge", "When counters were last written to the database (0: never).", c.lastSuccess.Load())
	one("dnsinfo_messages_total", "counter", "dnstap messages read.", c.frames.Load())
	one("dnsinfo_answers_total", "counter", "Answers counted.", c.counted.Load())
	one("dnsinfo_other_total", "counter", "Messages that are no answer to a client's question.", c.other.Load())
	one("dnsinfo_unreadable_total", "counter", "Messages that could not be read.", c.unreadable.Load())
	one("dnsinfo_dropped_total", "counter", "Answers not counted because the database was away too long.", c.dropped.Load())
	one("dnsinfo_written_total", "counter", "Counters written to the database.", c.written.Load())
	one("dnsinfo_errors_total", "counter", "Failed connects and writes to the database.", c.errors.Load())
	one("dnsinfo_held", "gauge", "Counters in memory, waiting to be written.", int64(held))
	return b.String()
}

// writeMetrics puts the metrics where the node exporter reads them: a
// whole new file moved into place, never half a file.
func writeMetrics(path string, c *counters, held int) {
	if path == "" {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(metricsText(c, held)), 0o644); err != nil {
		log.Printf("metrics: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("metrics: %v", err)
	}
}
