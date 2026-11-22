// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ergulcu/bit-meet-poc/internal/meeting"
	"github.com/ergulcu/bit-meet-poc/internal/store/storetest"
)

// The tests need a PostgreSQL server. BITMEET_TEST_POSTGRES points at one; otherwise one is started with
// Testcontainers, and without Docker the tests are skipped.
var (
	serverOnce sync.Once
	serverURL  string
	serverErr  error
	container  *tcpostgres.PostgresContainer
)

// TestMain stops the container itself, so nothing is left behind when Ryuk (the Testcontainers reaper)
// is turned off with TESTCONTAINERS_RYUK_DISABLED=true.
func TestMain(m *testing.M) {
	code := m.Run()
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
	os.Exit(code)
}

func adminURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("BITMEET_TEST_POSTGRES"); url != "" {
		return url
	}
	if testing.Short() {
		t.Skip("PostgreSQL tests are skipped with -short")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)
	serverOnce.Do(func() {
		ctx := context.Background()
		container, serverErr = tcpostgres.Run(ctx, "postgres:18-alpine",
			tcpostgres.WithDatabase("bitmeet"), tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
			tcpostgres.BasicWaitStrategies())
		if serverErr == nil {
			serverURL, serverErr = container.ConnectionString(ctx, "sslmode=disable")
		}
	})
	if serverErr != nil {
		t.Fatalf("start PostgreSQL: %v", serverErr)
	}
	return serverURL
}

var (
	dbMu      sync.Mutex
	dbCounter int
)

// newStore creates a fresh database for every test so the tests do not see each other's rows.
func newStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	admin := adminURL(t)
	dbMu.Lock()
	dbCounter++
	name := fmt.Sprintf("bitmeet_test_%d_%d", os.Getpid(), dbCounter)
	dbMu.Unlock()

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(admin)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		if c, err := pgx.Connect(context.Background(), admin); err == nil {
			c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") //nolint:errcheck
			c.Close(context.Background())
		}
	})
	return s
}

func TestStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) meeting.Store { return newStore(t) })
}

func TestMigrationsRunOnce(t *testing.T) {
	s := newStore(t)
	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	var count int
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("schema_migrations has %d rows, want 1", count)
	}
}

func TestTheDatabaseRefusesAMeetingThatEndsBeforeItBegins(t *testing.T) {
	s := newStore(t)
	err := s.Lock(context.Background(), "ali@example.com", func(tx meeting.Tx) error {
		return tx.Insert(context.Background(), &meeting.Meeting{Planner: "ali@example.com", Subject: "Backwards",
			Begin: storetest.At(11, 0), End: storetest.At(10, 0), Status: meeting.Active})
	})
	if err == nil {
		t.Error("the check constraint must refuse it")
	}
}
