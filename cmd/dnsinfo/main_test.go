// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dhtech/dnsinfo/internal/count"
	"github.com/dhtech/dnsinfo/internal/dnstap"
	"github.com/dhtech/dnsinfo/internal/fstrm"
)

// What a name server sends, built by hand: Frame Streams around
// protocol buffers around DNS messages.

func uvarint(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func vfield(number int, v uint64) []byte { return append(uvarint(uint64(number)<<3), uvarint(v)...) }

func bfield(number int, b []byte) []byte {
	return append(append(uvarint(uint64(number)<<3|2), uvarint(uint64(len(b)))...), b...)
}

// dns is a DNS answer to a question for name, with result code rcode.
func dns(name string, rcode byte) []byte {
	m := []byte{0, 1, 0x81, 0x80 | rcode, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, label := range strings.Split(name, ".") {
		m = append(m, byte(len(label)))
		m = append(m, label...)
	}
	return append(m, 0, 0, 1, 0, 1)
}

// tap is a dnstap message of the given kind around a DNS message, with
// the client's address in it as a name server puts it there.
func tap(kind int, seconds uint64, answer []byte) []byte {
	m := vfield(1, uint64(kind))
	m = append(m, bfield(4, []byte{192, 0, 2, 7})...) // the client: never read
	if seconds != 0 {
		m = append(m, vfield(12, seconds)...)
	}
	if answer != nil {
		m = append(m, bfield(14, answer)...)
	}
	return append(bfield(14, m), vfield(15, 1)...)
}

func control(kind uint32, contentType string) []byte {
	body := binary.BigEndian.AppendUint32(nil, kind)
	if contentType != "" {
		body = binary.BigEndian.AppendUint32(body, 1)
		body = binary.BigEndian.AppendUint32(body, uint32(len(contentType)))
		body = append(body, contentType...)
	}
	out := make([]byte, 4, 8+len(body))
	out = binary.BigEndian.AppendUint32(out, uint32(len(body)))
	return append(out, body...)
}

// send plays the name server: the handshake, the frames, STOP.
func send(t *testing.T, conn net.Conn, frames ...[]byte) {
	t.Helper()
	defer conn.Close()
	conn.Write(control(4, dnstap.ContentType)) // READY
	accept := make([]byte, len(control(1, dnstap.ContentType)))
	if _, err := conn.Read(accept); err != nil {
		t.Errorf("no ACCEPT: %v", err)
		return
	}
	conn.Write(control(2, dnstap.ContentType)) // START
	for _, f := range frames {
		conn.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(f))), f...))
	}
	conn.Write(control(3, ""))  // STOP
	conn.Read(make([]byte, 64)) // FINISH
}

const hour10 = 1767348000 // 2026-01-02 10:00:00 UTC

func TestAStreamIsCountedByDomainAndNothingElseIsKept(t *testing.T) {
	a, b := net.Pipe()
	go send(t, b,
		tap(dnstap.ClientResponse, hour10+5, dns("www.example.org", 0)),
		tap(dnstap.ClientResponse, hour10+6, dns("mail.example.org", 0)),
		tap(dnstap.ClientResponse, hour10+7, dns("nothing.example.net", 3)),
		tap(dnstap.ClientResponse, hour10+3700, dns("www.example.org", 0)),
		tap(dnstap.ClientResponse, hour10+8, dns("www.example.co.uk", 2)),
		tap(5, hour10+9, nil),                                    // a client's question: not an answer
		tap(dnstap.ClientResponse, hour10+9, nil),                // an answer without the answer
		tap(dnstap.ClientResponse, hour10+9, []byte{1, 2, 3}),    // not a DNS message
		[]byte{0x72, 0xff, 0xff, 0xff, 0x0f},                     // not a dnstap message
		tap(dnstap.ClientResponse, 0, dns("now.example.org", 0)), // no time in it: ours is used
	)
	counter := count.New(1000)
	var c counters
	now := func() time.Time { return time.Unix(hour10+7300, 0) }
	if err := read(a, counter, &c, 1000, now); !errors.Is(err, fstrm.ErrStopped) {
		t.Fatalf("the stream ended with %v", err)
	}
	got := map[string]int64{}
	for _, r := range counter.Take() {
		got[r.Domain+" "+r.Hour.UTC().Format("15h")+" "+r.Result] = r.Answers
	}
	want := map[string]int64{
		"example.org 10h answered": 2,
		"example.net 10h noname":   1,
		"example.org 11h answered": 1,
		"example.co.uk 10h failed": 1,
		"example.org 12h answered": 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	if c.frames.Load() != 10 || c.counted.Load() != 6 || c.other.Load() != 2 || c.unreadable.Load() != 2 {
		t.Fatalf("frames %d counted %d other %d unreadable %d", c.frames.Load(), c.counted.Load(), c.other.Load(), c.unreadable.Load())
	}
}

func TestWithTheDatabaseAwayTooLongAnswersAreDroppedAndCounted(t *testing.T) {
	a, b := net.Pipe()
	go send(t, b,
		tap(dnstap.ClientResponse, hour10, dns("a.example", 0)),
		tap(dnstap.ClientResponse, hour10, dns("b.example", 0)),
		tap(dnstap.ClientResponse, hour10, dns("c.example", 0)),
		tap(dnstap.ClientResponse, hour10, dns("a.example", 0)))
	counter := count.New(1000)
	var c counters
	read(a, counter, &c, 2, time.Now) // room for two counters
	if c.counted.Load() != 2 || c.dropped.Load() != 2 || counter.Held() != 2 {
		t.Fatalf("counted %d dropped %d held %d", c.counted.Load(), c.dropped.Load(), counter.Held())
	}
}

func TestTheSocketIsForItsOwnerAndGroupOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dnstap.sock")
	l, err := listen(path, "")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o660 {
		t.Fatalf("mode %v, %v", info.Mode().Perm(), err)
	}
	l.Close()
	// a socket left over from a run that died is taken away
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	l, err = listen(path, "")
	if err != nil {
		t.Fatalf("with a socket left over: %v", err)
	}
	l.Close()
	if _, err := listen(path, "no-such-group-here"); err == nil {
		t.Fatal("a group that does not exist was accepted")
	}
}

func TestMetricsText(t *testing.T) {
	var c counters
	c.frames.Store(12)
	c.counted.Store(10)
	c.connected.Store(true)
	c.lastSuccess.Store(1767348000)
	text := metricsText(&c, 3)
	for _, want := range []string{
		"dnsinfo_connected 1\n", "dnsinfo_messages_total 12\n", "dnsinfo_answers_total 10\n",
		"dnsinfo_held 3\n", "dnsinfo_last_success_timestamp_seconds 1767348000\n",
		"# TYPE dnsinfo_answers_total counter\n", "# TYPE dnsinfo_held gauge\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}
	path := filepath.Join(t.TempDir(), "dnsinfo.prom")
	writeMetrics(path, &c, 3)
	if got, err := os.ReadFile(path); err != nil || string(got) != text {
		t.Fatalf("the file: %v", err)
	}
}
