// SPDX-License-Identifier: GPL-3.0-or-later

// Package store keeps the counters in PostgreSQL.
//
// The program owns its tables: it creates them on first start (and
// changes them when a newer version needs to), so the only thing the
// database has to provide is a role that may create tables in the
// database it is pointed at.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/dhtech/dnsinfo/internal/count"
)

// migrations are applied in order, each in one transaction; the number
// of those applied is kept in dnsinfo_schema. Never edit one that has
// been released - add another.
var migrations = []string{
	`CREATE TABLE dnsinfo_domain (
		domain  text        NOT NULL,
		hour    timestamptz NOT NULL,
		result  text        NOT NULL,
		answers bigint      NOT NULL,
		PRIMARY KEY (domain, hour, result)
	);
	COMMENT ON TABLE dnsinfo_domain IS 'How many answers the resolvers gave, per domain, hour and result (dnsinfo). Nothing about who asked.';
	COMMENT ON COLUMN dnsinfo_domain.domain IS 'the domain of the name asked for: example.org for www.example.org';
	COMMENT ON COLUMN dnsinfo_domain.hour IS 'the start of the hour the answers were given in';
	COMMENT ON COLUMN dnsinfo_domain.result IS 'answered, noname (no such name) or failed';
	CREATE INDEX dnsinfo_domain_hour ON dnsinfo_domain (hour);

	CREATE VIEW dnsinfo_total AS
		SELECT domain,
		       sum(answers)::bigint AS answers,
		       coalesce(sum(answers) FILTER (WHERE result = 'answered'), 0)::bigint AS answered,
		       coalesce(sum(answers) FILTER (WHERE result = 'noname'), 0)::bigint AS noname,
		       coalesce(sum(answers) FILTER (WHERE result = 'failed'), 0)::bigint AS failed,
		       min(hour) AS first_hour,
		       max(hour) AS last_hour
		FROM dnsinfo_domain
		GROUP BY domain;
	COMMENT ON VIEW dnsinfo_total IS 'Every domain with its count over all hours: the total. Sort by answers for the top list.';`,
}

// Store is the database.
type Store struct {
	db *sql.DB
}

// Open connects. driver is a database/sql driver name ("pgx").
func Open(ctx context.Context, driver, dsn string) (*Store, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	// one connection: this program writes a little, every few seconds
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the connection.
func (s *Store) Close() error { return s.db.Close() }

// Migrate brings the tables to this version's form and returns the
// schema version. Two programs starting at once is fine: the second
// waits for the first.
func (s *Store) Migrate(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// both resolvers may start at the same moment
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('dnsinfo_schema'))`); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS dnsinfo_schema (version integer NOT NULL)`); err != nil {
		return 0, err
	}
	var version int
	err = tx.QueryRowContext(ctx, `SELECT version FROM dnsinfo_schema`).Scan(&version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `INSERT INTO dnsinfo_schema (version) VALUES (0)`); err != nil {
			return 0, err
		}
	case err != nil:
		return 0, err
	}
	if version > len(migrations) {
		return 0, fmt.Errorf("the database is at schema version %d, this program knows %d: it is older than the database", version, len(migrations))
	}
	for ; version < len(migrations); version++ {
		if _, err := tx.ExecContext(ctx, migrations[version]); err != nil {
			return 0, fmt.Errorf("schema version %d: %w", version+1, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dnsinfo_schema SET version = $1`, version); err != nil {
		return 0, err
	}
	return version, tx.Commit()
}

// rowsPerStatement keeps a statement well under PostgreSQL's limit of
// 65535 parameters.
const rowsPerStatement = 1000

// Add adds the counts in rows to what the table has, all or nothing.
func (s *Store) Add(ctx context.Context, rows []count.Row) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for len(rows) > 0 {
		n := min(len(rows), rowsPerStatement)
		var b strings.Builder
		args := make([]any, 0, n*4)
		b.WriteString(`INSERT INTO dnsinfo_domain (domain, hour, result, answers) VALUES `)
		for i, r := range rows[:n] {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "($%d,$%d,$%d,$%d)", i*4+1, i*4+2, i*4+3, i*4+4)
			args = append(args, r.Domain, r.Hour, r.Result, r.Answers)
		}
		b.WriteString(` ON CONFLICT (domain, hour, result) DO UPDATE SET answers = dnsinfo_domain.answers + EXCLUDED.answers`)
		if _, err := tx.ExecContext(ctx, b.String(), args...); err != nil {
			return err
		}
		rows = rows[n:]
	}
	return tx.Commit()
}
