// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

// Package storetest checks that a meeting.Store behaves as the service expects. Every store runs the same tests.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ergulcu/bit-meet-poc/internal/meeting"
)

// At returns 10 March 2026 at the given hour and minute.
func At(hour, minute int) time.Time {
	return time.Date(2026, 3, 10, hour, minute, 0, 0, time.UTC)
}

func insert(t *testing.T, s meeting.Store, m meeting.Meeting) meeting.Meeting {
	t.Helper()
	if m.Status == "" {
		m.Status = meeting.Active
	}
	err := s.Lock(context.Background(), m.Planner, func(tx meeting.Tx) error {
		return tx.Insert(context.Background(), &m)
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func overlapping(t *testing.T, s meeting.Store, planner string, begin, end time.Time, excludeID int64) bool {
	t.Helper()
	var result bool
	err := s.Lock(context.Background(), planner, func(tx meeting.Tx) error {
		var err error
		result, err = tx.Overlapping(context.Background(), planner, begin, end, excludeID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// Run runs the tests; newStore returns an empty store.
func Run(t *testing.T, newStore func(t *testing.T) meeting.Store) {
	ctx := context.Background()

	t.Run("InsertAndGet", func(t *testing.T) {
		s := newStore(t)
		m := insert(t, s, meeting.Meeting{Planner: "ali@example.com", Subject: "Sprint planning",
			Guests: []string{"veli@example.com", "ayse@example.com"}, Begin: At(14, 0), End: At(15, 0)})
		if m.ID == 0 {
			t.Fatal("Insert must set the id")
		}
		got, err := s.Get(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Planner != m.Planner || got.Subject != m.Subject || !slices.Equal(got.Guests, m.Guests) ||
			!got.Begin.Equal(m.Begin) || !got.End.Equal(m.End) || got.Status != meeting.Active {
			t.Errorf("got %+v, want %+v", got, m)
		}
	})

	t.Run("NoGuests", func(t *testing.T) {
		s := newStore(t)
		m := insert(t, s, meeting.Meeting{Planner: "ali@example.com", Subject: "Alone", Begin: At(9, 0), End: At(10, 0)})
		got, _ := s.Get(ctx, m.ID)
		if got.Guests == nil || len(got.Guests) != 0 {
			t.Errorf("guests = %#v, want an empty list", got.Guests)
		}
	})

	t.Run("UnknownID", func(t *testing.T) {
		if _, err := newStore(t).Get(ctx, 4711); !errors.Is(err, meeting.ErrNotFound) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("ByPlannerIsOrderedAndSeparated", func(t *testing.T) {
		s := newStore(t)
		insert(t, s, meeting.Meeting{Planner: "ali@example.com", Subject: "Late", Begin: At(16, 0), End: At(17, 0)})
		insert(t, s, meeting.Meeting{Planner: "veli@example.com", Subject: "Other", Begin: At(8, 0), End: At(9, 0)})
		insert(t, s, meeting.Meeting{Planner: "ali@example.com", Subject: "Early", Begin: At(9, 0), End: At(10, 0)})
		got, err := s.ByPlanner(ctx, "ali@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Subject != "Early" || got[1].Subject != "Late" {
			t.Errorf("got %+v", got)
		}
		if none, _ := s.ByPlanner(ctx, "nobody@example.com"); len(none) != 0 {
			t.Errorf("unknown planner: %+v", none)
		}
	})

	t.Run("Overlapping", func(t *testing.T) {
		s := newStore(t)
		existing := insert(t, s, meeting.Meeting{Planner: "ali@example.com", Subject: "Existing",
			Begin: At(10, 0), End: At(11, 0)})
		insert(t, s, meeting.Meeting{Planner: "ali@example.com", Subject: "Cancelled",
			Begin: At(13, 0), End: At(14, 0), Status: meeting.Cancelled})
		tests := []struct {
			name       string
			begin, end time.Time
			exclude    int64
			planner    string
			want       bool
		}{
			{"partly before", At(9, 30), At(10, 30), 0, "", true},
			{"partly after", At(10, 30), At(11, 30), 0, "", true},
			{"inside", At(10, 15), At(10, 45), 0, "", true},
			{"contains it", At(9, 0), At(12, 0), 0, "", true}, // missed by the first Java version
			{"same range", At(10, 0), At(11, 0), 0, "", true},
			{"ends when it begins", At(9, 0), At(10, 0), 0, "", false},
			{"begins when it ends", At(11, 0), At(12, 0), 0, "", false},
			{"over a cancelled meeting", At(13, 0), At(14, 0), 0, "", false},
			{"itself while updating", At(10, 30), At(11, 30), existing.ID, "", false},
			{"another planner", At(10, 0), At(11, 0), 0, "veli@example.com", false},
		}
		for _, tt := range tests {
			planner := tt.planner
			if planner == "" {
				planner = "ali@example.com"
			}
			if got := overlapping(t, s, planner, tt.begin, tt.end, tt.exclude); got != tt.want {
				t.Errorf("%s: overlapping = %v, want %v", tt.name, got, tt.want)
			}
		}
	})

	t.Run("Update", func(t *testing.T) {
		s := newStore(t)
		m := insert(t, s, meeting.Meeting{Planner: "ali@example.com", Subject: "Draft", Guests: []string{"a@example.com"},
			Begin: At(10, 0), End: At(11, 0)})
		m.Subject, m.Guests, m.Begin, m.End, m.Status = "Final", []string{"b@example.com"}, At(12, 0), At(13, 0), meeting.Cancelled
		err := s.Lock(ctx, m.Planner, func(tx meeting.Tx) error { return tx.Update(ctx, m) })
		if err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, m.ID)
		if got.Subject != "Final" || !slices.Equal(got.Guests, []string{"b@example.com"}) || !got.Begin.Equal(At(12, 0)) ||
			got.Status != meeting.Cancelled {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("FailedLockKeepsNothing", func(t *testing.T) {
		s := newStore(t)
		m := insert(t, s, meeting.Meeting{Planner: "ali@example.com", Subject: "Kept", Begin: At(10, 0), End: At(11, 0)})
		boom := errors.New("boom")
		err := s.Lock(ctx, m.Planner, func(tx meeting.Tx) error {
			n := meeting.Meeting{Planner: m.Planner, Subject: "Lost", Begin: At(12, 0), End: At(13, 0), Status: meeting.Active}
			if err := tx.Insert(ctx, &n); err != nil {
				return err
			}
			m.Subject = "Changed"
			if err := tx.Update(ctx, m); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		got, _ := s.ByPlanner(ctx, m.Planner)
		if len(got) != 1 || got[0].Subject != "Kept" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("ConcurrentCreatesDoNotOverlap", func(t *testing.T) {
		// Many requests for the same time range at once: exactly one may win.
		s := newStore(t)
		service := meeting.NewService(s, meeting.NewBadWords(nil))
		const requests = 20
		var wg sync.WaitGroup
		errs := make(chan error, requests)
		for i := range requests {
			wg.Go(func() {
				_, err := service.Create(ctx, meeting.Meeting{Planner: "ali@example.com",
					Subject: fmt.Sprintf("Request %d", i), Begin: At(10, i), End: At(11, i)})
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		created := 0
		for err := range errs {
			var rule *meeting.RuleError
			switch {
			case err == nil:
				created++
			case !errors.As(err, &rule):
				t.Errorf("unexpected error: %v", err)
			}
		}
		if created != 1 {
			t.Errorf("%d overlapping meetings were created, want 1", created)
		}
	})
}
