// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

// Package postgres keeps the meetings in PostgreSQL, so they survive a restart and several instances of the
// service can share them. The overlap check and the write that follows it run in one transaction that holds
// an advisory lock on the planner, so two instances cannot both accept overlapping meetings.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ergulcu/bit-meet-poc/internal/meeting"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLock keeps two starting instances from migrating at the same time.
const migrationLock = 2_022_112_001

// Store is safe for concurrent use.
type Store struct {
	pool *pgxpool.Pool
}

var _ meeting.Store = (*Store)(nil)

// Open connects to the database and brings the schema up to date.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) Close() { s.pool.Close() }

func (s *Store) migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
		return fmt.Errorf("lock for migration: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLock) //nolint:errcheck

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		var applied bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).
			Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", version, err)
		}
	}
	return nil
}

const columns = "id, planner, subject, guests, begin_date, end_date, status"

func (s *Store) ByPlanner(ctx context.Context, planner string) ([]meeting.Meeting, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+columns+" FROM meeting WHERE planner = $1 ORDER BY begin_date, id", planner)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (meeting.Meeting, error) { return scan(r) })
}

func (s *Store) Get(ctx context.Context, id int64) (meeting.Meeting, error) {
	return get(ctx, s.pool, id, "")
}

func (s *Store) Lock(ctx context.Context, planner string, f func(meeting.Tx) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(t pgx.Tx) error {
		// Held until the transaction ends.
		if _, err := t.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", planner); err != nil {
			return err
		}
		return f(tx{t})
	})
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func get(ctx context.Context, q querier, id int64, suffix string) (meeting.Meeting, error) {
	rows := q.QueryRow(ctx, "SELECT "+columns+" FROM meeting WHERE id = $1"+suffix, id)
	m, err := scan(rows)
	if errors.Is(err, pgx.ErrNoRows) {
		return meeting.Meeting{}, meeting.ErrNotFound
	}
	return m, err
}

func scan(row pgx.Row) (meeting.Meeting, error) {
	var m meeting.Meeting
	var status string
	err := row.Scan(&m.ID, &m.Planner, &m.Subject, &m.Guests, &m.Begin, &m.End, &status)
	m.Status = meeting.Status(status)
	if m.Guests == nil {
		m.Guests = []string{}
	}
	return m, err
}

type tx struct{ t pgx.Tx }

func (t tx) Get(ctx context.Context, id int64) (meeting.Meeting, error) {
	return get(ctx, t.t, id, " FOR UPDATE")
}

func (t tx) Overlapping(ctx context.Context, planner string, begin, end time.Time, excludeID int64) (bool, error) {
	var found bool
	err := t.t.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM meeting
		WHERE planner = $1 AND status = 'ACTIVE' AND begin_date < $3 AND end_date > $2 AND id <> $4)`,
		planner, begin, end, excludeID).Scan(&found)
	return found, err
}

func (t tx) Insert(ctx context.Context, m *meeting.Meeting) error {
	return t.t.QueryRow(ctx, `INSERT INTO meeting (planner, subject, guests, begin_date, end_date, status)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		m.Planner, m.Subject, guests(m.Guests), m.Begin, m.End, string(m.Status)).Scan(&m.ID)
}

func (t tx) Update(ctx context.Context, m meeting.Meeting) error {
	tag, err := t.t.Exec(ctx, `UPDATE meeting SET subject = $2, guests = $3, begin_date = $4, end_date = $5, status = $6
		WHERE id = $1`, m.ID, m.Subject, guests(m.Guests), m.Begin, m.End, string(m.Status))
	if err == nil && tag.RowsAffected() == 0 {
		return meeting.ErrNotFound
	}
	return err
}

func guests(g []string) []string {
	if g == nil {
		return []string{}
	}
	return g
}
