// SPDX-License-Identifier: GPL-3.0-or-later

// dnsinfo counts which domains a name server is asked for.
//
// The name server (BIND, with dnstap) sends every answer it gives a
// client to a unix socket. dnsinfo listens there, reads the name that
// was asked for out of each answer, cuts it down to its domain and adds
// one to that domain's counter for the hour, in a PostgreSQL database.
//
// Nothing else is kept: not who asked, not the full name, not the answer.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	dnstap "github.com/dnstap/golang-dnstap"
	framestream "github.com/farsightsec/golang-framestream"
	"google.golang.org/protobuf/proto"

	"github.com/dhtech/dnsinfo/internal/count"
	"github.com/dhtech/dnsinfo/internal/dnsmsg"
	"github.com/dhtech/dnsinfo/internal/domain"
	"github.com/dhtech/dnsinfo/internal/store"
)

// handshakeWait is how long a name server that has connected gets to
// say what it is going to send.
const handshakeWait = 10 * time.Second

// writeWait is how long one write to the database may take, the
// connecting included. A database that neither answers nor refuses
// would otherwise hold the program until the network gives up.
const writeWait = 30 * time.Second

// exitConfig is what the program ends with when it was started wrongly
// (EX_CONFIG of sysexits.h): starting it again would end the same way,
// and the unit does not try.
const exitConfig = 78

func misconfigured(what string) {
	log.Print(what)
	os.Exit(exitConfig)
}

// version is set by the release build (-ldflags -X main.version=...).
var version = "dev"

type options struct {
	socket      string
	socketGroup string
	dsn         string
	flush       time.Duration
	maxDomains  int
	maxHeld     int
	metrics     string
}

// counters are what the program has done since it started.
type counters struct {
	frames      atomic.Int64 // dnstap messages read
	counted     atomic.Int64 // answers counted
	other       atomic.Int64 // messages that are no answer to a client's question
	unreadable  atomic.Int64 // messages or answers that could not be read
	connections atomic.Int64 // name servers connected right now
	written     atomic.Int64 // counters written to the database
	dropped     atomic.Int64 // answers thrown away because the database was away too long
	errors      atomic.Int64 // failed writes and connects
	lastSuccess atomic.Int64 // unix time of the last good write
	connected   atomic.Bool  // the database is there
}

func main() {
	var o options
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.StringVar(&o.socket, "socket", "/run/dnsinfo/dnstap.sock",
		"the unix socket the name server sends its dnstap stream to (dnstap-output unix \"...\")")
	flag.StringVar(&o.socketGroup, "socket-group", "",
		"give the socket to this group, which may then write to it (the name server's group; optional)")
	flag.StringVar(&o.dsn, "dsn", os.Getenv("DNSINFO_DSN"),
		"PostgreSQL connection string or URL (default: $DNSINFO_DSN)")
	flag.DurationVar(&o.flush, "flush", 5*time.Second, "how often the counters are added to the database")
	flag.IntVar(&o.maxDomains, "max-domains", 100000,
		"at most this many different domains per hour; the rest is counted as \"(other)\"")
	flag.IntVar(&o.maxHeld, "max-held", 500000,
		"while the database is away, hold at most this many counters; then answers are dropped and counted as dropped")
	flag.StringVar(&o.metrics, "metrics", "",
		"write Prometheus metrics to this file (node exporter textfile collector; optional)")
	flag.Parse()
	if *showVersion {
		fmt.Printf("dnsinfo %s (%s)\n", version, runtime.Version())
		return
	}
	log.SetFlags(0) // the journal has the time
	if o.dsn == "" {
		misconfigured("no database given: -dsn or DNSINFO_DSN")
	}
	if o.flush < 100*time.Millisecond || o.maxDomains < 1 || o.maxHeld < 1 {
		misconfigured("-flush, -max-domains or -max-held is too small")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	listener, err := listen(o.socket, o.socketGroup)
	if err != nil {
		log.Fatalf("the socket: %v", err)
	}
	log.Printf("dnsinfo %s: listening on %s", version, o.socket)

	var c counters
	counter := count.New(o.maxDomains)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		accept(ctx, listener, counter, &c, o.maxHeld)
	}()
	write(ctx, o, counter, &c)
	listener.Close()
	wg.Wait()
	os.Remove(o.socket)
	log.Print("stopped")
}

// listen makes the socket. One that is left over from an earlier run is
// taken away first: only one dnsinfo runs on a machine.
func listen(path, group string) (net.Listener, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
		os.Remove(path)
	}
	// the name server must be able to write to it, nobody else
	old := syscall.Umask(0o117)
	l, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			l.Close()
			return nil, err
		}
		gid, _ := strconv.Atoi(g.Gid)
		if err := os.Chown(path, -1, gid); err != nil {
			l.Close()
			return nil, fmt.Errorf("giving it to the group %s: %w", group, err)
		}
	}
	return l, nil
}

// accept takes the name server's connections until ctx ends.
func accept(ctx context.Context, l net.Listener, counter *count.Counter, c *counters, maxHeld int) {
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("the socket: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			stopRead := context.AfterFunc(ctx, func() { conn.Close() })
			defer stopRead()
			c.connections.Add(1)
			defer c.connections.Add(-1)
			log.Print("a name server connected")
			err := read(conn, counter, c, maxHeld, time.Now)
			switch {
			case ctx.Err() != nil:
			case errors.Is(err, io.EOF):
				log.Print("the name server closed its stream")
			default:
				log.Printf("the stream ended: %v", err)
			}
		}()
	}
}

// read counts the answers of one stream until it ends: io.EOF when the
// name server closed it properly. The stream is dnstap's own format,
// read by the dnstap project's libraries: Frame Streams around
// protocol buffers.
func read(conn io.ReadWriter, counter *count.Counter, c *counters, maxHeld int, now func() time.Time) error {
	stream, err := framestream.NewReader(conn, &framestream.ReaderOptions{
		ContentTypes:  [][]byte{dnstap.FSContentType},
		Bidirectional: true,
		Timeout:       handshakeWait,
	})
	if err != nil {
		return err
	}
	buf := make([]byte, dnstap.MaxPayloadSize)
	var tap dnstap.Dnstap
	for {
		n, err := stream.ReadFrame(buf)
		if errors.Is(err, framestream.ErrDataFrameTooLarge) {
			// stepped over by the library; the stream goes on
			c.frames.Add(1)
			c.unreadable.Add(1)
			continue
		}
		if err != nil {
			return err
		}
		c.frames.Add(1)
		if err := proto.Unmarshal(buf[:n], &tap); err != nil {
			c.unreadable.Add(1)
			continue
		}
		m := tap.GetMessage()
		answer := m.GetResponseMessage()
		if m.GetType() != dnstap.Message_CLIENT_RESPONSE || len(answer) == 0 {
			c.other.Add(1)
			continue
		}
		name, result, err := dnsmsg.Answer(answer)
		if errors.Is(err, dnsmsg.ErrNotCounted) {
			c.other.Add(1)
			continue
		}
		if err != nil {
			c.unreadable.Add(1)
			continue
		}
		// The database has been away so long that memory would fill:
		// these answers are not counted, and that is counted.
		if counter.Held() >= maxHeld {
			c.dropped.Add(1)
			continue
		}
		when := now()
		if seconds := m.GetResponseTimeSec(); seconds != 0 {
			when = time.Unix(int64(seconds), 0)
		}
		counter.Add(domain.Of(name), result, when)
		c.counted.Add(1)
	}
}

// write adds the counters to the database every o.flush, until ctx ends
// - and once more then. While the database is away the counters stay in
// memory and are added when it is back.
func write(ctx context.Context, o options, counter *count.Counter, c *counters) {
	var db *store.Store
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	wait := o.flush
	nextTry := time.Now()
	flush := func(ctx context.Context) {
		if db == nil {
			if time.Now().Before(nextTry) {
				return
			}
			var err error
			if db, err = connect(ctx, o.dsn); err != nil {
				db = nil
				c.errors.Add(1)
				c.connected.Store(false)
				log.Printf("database: %s - again in %s", oneLine(err), wait)
				nextTry = time.Now().Add(wait)
				if wait < time.Minute {
					wait *= 2
				}
				return
			}
			wait = o.flush
			c.connected.Store(true)
		}
		rows := counter.Take()
		if err := db.Add(ctx, rows); err != nil {
			counter.Return(rows)
			c.errors.Add(1)
			c.connected.Store(false)
			log.Printf("writing %d counters: %s", len(rows), oneLine(err))
			db.Close()
			db = nil
			return
		}
		c.written.Add(int64(len(rows)))
		c.lastSuccess.Store(time.Now().Unix())
	}
	ticker := time.NewTicker(o.flush)
	defer ticker.Stop()
	for {
		limited, cancel := context.WithTimeout(ctx, writeWait)
		flush(limited)
		cancel()
		writeMetrics(o.metrics, c, counter.Held())
		select {
		case <-ctx.Done():
			// what was counted since the last write, with a little time of its own
			last, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			flush(last)
			cancel()
			writeMetrics(o.metrics, c, counter.Held())
			return
		case <-ticker.C:
		}
	}
}

// oneLine is an error as one line of the journal.
func oneLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

func connect(ctx context.Context, dsn string) (*store.Store, error) {
	db, err := store.Open(ctx, driver, dsn)
	if err != nil {
		return nil, err
	}
	schema, err := db.Migrate(ctx)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("tables: %w", err)
	}
	log.Printf("database ready: schema version %d", schema)
	return db, nil
}
