// SPDX-License-Identifier: GPL-3.0-or-later

package store

// These tests need a real PostgreSQL: they run when DNSINFO_TEST_DSN
// names a database the test may fill and empty (CI starts one), and are
// skipped otherwise.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/dhtech/dnsinfo/internal/count"
)

func open(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("DNSINFO_TEST_DSN")
	if dsn == "" {
		t.Skip("DNSINFO_TEST_DSN not set: no database to test against")
	}
	ctx := context.Background()
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	if _, err := raw.ExecContext(ctx, `DROP VIEW IF EXISTS dnsinfo_total;
		DROP TABLE IF EXISTS dnsinfo_domain, dnsinfo_schema`); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, "pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, raw
}

var hour = time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)

func TestMigrateTwiceAndAddUp(t *testing.T) {
	s, raw := open(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if v, err := s.Migrate(ctx); err != nil || v != len(migrations) {
			t.Fatalf("migrate %d: version %d, %v", i, v, err)
		}
	}
	rows := []count.Row{
		{Domain: "example.net", Hour: hour, Result: "answered", Answers: 1},
		{Domain: "example.org", Hour: hour, Result: "answered", Answers: 5},
		{Domain: "example.org", Hour: hour, Result: "noname", Answers: 2},
		{Domain: "example.org", Hour: hour.Add(time.Hour), Result: "answered", Answers: 3},
		{Domain: "example.org", Hour: hour, Result: "refused", Answers: 4},
		{Domain: "example.org", Hour: hour, Result: "failed", Answers: 1},
	}
	for i := 0; i < 2; i++ { // the same counts twice: they add up
		if err := s.Add(ctx, rows); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Add(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var n, answers int64
	if err := raw.QueryRowContext(ctx, `SELECT count(*), sum(answers) FROM dnsinfo_domain`).Scan(&n, &answers); err != nil || n != 6 || answers != 32 {
		t.Fatalf("%d rows, %d answers, %v", n, answers, err)
	}
	var total, answered, noname, refused, failed int64
	var first, last time.Time
	err := raw.QueryRowContext(ctx, `SELECT answers, answered, noname, refused, failed, first_hour, last_hour
		FROM dnsinfo_total WHERE domain = 'example.org'`).Scan(&total, &answered, &noname, &refused, &failed, &first, &last)
	if err != nil || total != 30 || answered != 16 || noname != 4 || refused != 8 || failed != 2 ||
		!first.Equal(hour) || !last.Equal(hour.Add(time.Hour)) {
		t.Fatalf("total %d answered %d noname %d refused %d failed %d %s..%s, %v", total, answered, noname, refused, failed, first, last, err)
	}
}

// A database a version before 0.2.0 made (schema version 1, with rows,
// and somebody else let read the view) is brought to this version: the
// rows stay, the view gains "refused", and the reader may still read it.
func TestUpgradeFromSchemaVersionOne(t *testing.T) {
	s, raw := open(t)
	ctx := context.Background()
	if _, err := raw.ExecContext(ctx, migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE dnsinfo_schema (version integer NOT NULL);
		INSERT INTO dnsinfo_schema (version) VALUES (1);
		INSERT INTO dnsinfo_domain (domain, hour, result, answers) VALUES ('example.org', '2026-01-02 10:00:00+00', 'failed', 7);
		DROP ROLE IF EXISTS dnsinfo_test_reader;
		CREATE ROLE dnsinfo_test_reader;
		GRANT SELECT ON dnsinfo_total TO dnsinfo_test_reader`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		raw.ExecContext(ctx, `DROP VIEW IF EXISTS dnsinfo_total; DROP ROLE IF EXISTS dnsinfo_test_reader`)
	})
	if v, err := s.Migrate(ctx); err != nil || v != len(migrations) {
		t.Fatalf("version %d, %v", v, err)
	}
	if err := s.Add(ctx, []count.Row{{Domain: "example.org", Hour: hour, Result: "refused", Answers: 3}}); err != nil {
		t.Fatal(err)
	}
	var failed, refused int64
	var mayRead bool
	err := raw.QueryRowContext(ctx, `SELECT failed, refused, has_table_privilege('dnsinfo_test_reader', 'dnsinfo_total', 'SELECT')
		FROM dnsinfo_total WHERE domain = 'example.org'`).Scan(&failed, &refused, &mayRead)
	if err != nil || failed != 7 || refused != 3 || !mayRead {
		t.Fatalf("failed %d refused %d, the reader may read: %v, %v", failed, refused, mayRead, err)
	}
}

func TestManyRowsInOneWrite(t *testing.T) {
	s, raw := open(t)
	ctx := context.Background()
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	rows := make([]count.Row, 2500) // more than one statement holds
	for i := range rows {
		rows[i] = count.Row{Domain: fmt.Sprintf("d%04d.example", i), Hour: hour, Result: "answered", Answers: int64(i)}
	}
	if err := s.Add(ctx, rows); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := raw.QueryRowContext(ctx, `SELECT count(*) FROM dnsinfo_domain`).Scan(&n); err != nil || n != 2500 {
		t.Fatalf("%d rows, %v", n, err)
	}
}

// Two programs - the two resolvers - add to the same rows at the same
// time: nothing is lost and they do not stop each other for good.
func TestTwoWritersAddToTheSameRows(t *testing.T) {
	s, raw := open(t)
	ctx := context.Background()
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	other, err := Open(ctx, "pgx", os.Getenv("DNSINFO_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	rows := make([]count.Row, 200)
	for i := range rows {
		rows[i] = count.Row{Domain: fmt.Sprintf("d%03d.example", i), Hour: hour, Result: "answered", Answers: 1}
	}
	var wg sync.WaitGroup
	for _, w := range []*Store{s, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if err := w.Add(ctx, rows); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	var n, answers int64
	if err := raw.QueryRowContext(ctx, `SELECT count(*), sum(answers) FROM dnsinfo_domain`).Scan(&n, &answers); err != nil || n != 200 || answers != 8000 {
		t.Fatalf("%d rows, %d answers, %v", n, answers, err)
	}
}
