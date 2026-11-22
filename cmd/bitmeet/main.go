// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

// Command bitmeet serves the meeting planner API.
//
//	BITMEET_ADDR          listen address (:8088)
//	BITMEET_DATABASE_URL  PostgreSQL URL; without it the meetings are kept in memory
//	BITMEET_BAD_WORDS     file with the forbidden words, one per line (the built-in list otherwise)
//	BITMEET_LOG_LEVEL     debug, info (default), warn or error
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ergulcu/bit-meet-poc/internal/api"
	"github.com/ergulcu/bit-meet-poc/internal/meeting"
	"github.com/ergulcu/bit-meet-poc/internal/store/memory"
	"github.com/ergulcu/bit-meet-poc/internal/store/postgres"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv); err != nil {
		slog.Error("bitmeet stopped", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	var level slog.Level
	if err := level.UnmarshalText([]byte(or(getenv("BITMEET_LOG_LEVEL"), "info"))); err != nil {
		return fmt.Errorf("BITMEET_LOG_LEVEL: %w", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	words := meeting.DefaultBadWords()
	if path := getenv("BITMEET_BAD_WORDS"); path != "" {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("BITMEET_BAD_WORDS: %w", err)
		}
		words, err = meeting.ReadBadWords(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("BITMEET_BAD_WORDS: %w", err)
		}
	}

	var store meeting.Store
	var ping func(context.Context) error
	if url := getenv("BITMEET_DATABASE_URL"); url != "" {
		pg, err := postgres.Open(ctx, url)
		if err != nil {
			return err
		}
		defer pg.Close()
		store, ping = pg, pg.Ping
		log.Info("meetings are kept in PostgreSQL")
	} else {
		store = memory.New()
		log.Info("meetings are kept in memory; set BITMEET_DATABASE_URL to keep them in PostgreSQL")
	}

	srv := &http.Server{
		Addr:              or(getenv("BITMEET_ADDR"), ":8088"),
		Handler:           api.New(meeting.NewService(store, words), ping, log),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", srv.Addr, "badWords", words.Len(), "docs", "/docs")

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func or(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
