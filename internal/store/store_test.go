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
	if err := raw.QueryRowContext(ctx, `SELECT count(*), sum(answers) FROM dnsinfo_domain`).Scan(&n, &answers); err != nil || n != 4 || answers != 22 {
		t.Fatalf("%d rows, %d answers, %v", n, answers, err)
	}
	var total, answered, noname, failed int64
	var first, last time.Time
	err := raw.QueryRowContext(ctx, `SELECT answers, answered, noname, failed, first_hour, last_hour
		FROM dnsinfo_total WHERE domain = 'example.org'`).Scan(&total, &answered, &noname, &failed, &first, &last)
	if err != nil || total != 20 || answered != 16 || noname != 4 || failed != 0 ||
		!first.Equal(hour) || !last.Equal(hour.Add(time.Hour)) {
		t.Fatalf("total %d answered %d noname %d failed %d %s..%s, %v", total, answered, noname, failed, first, last, err)
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
