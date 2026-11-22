// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

// Package memory keeps meetings in memory, as the H2 database of the Java version did: everything is gone
// after a restart. It is the store for development and tests.
package memory

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/ergulcu/bit-meet-poc/internal/meeting"
)

// Store is safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	meetings map[int64]meeting.Meeting
	nextID   int64
}

var _ meeting.Store = (*Store)(nil)

func New() *Store {
	return &Store{meetings: map[int64]meeting.Meeting{}, nextID: 1}
}

func (s *Store) ByPlanner(_ context.Context, planner string) ([]meeting.Meeting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []meeting.Meeting
	for _, m := range s.meetings {
		if m.Planner == planner {
			result = append(result, clone(m))
		}
	}
	slices.SortFunc(result, func(a, b meeting.Meeting) int {
		if c := a.Begin.Compare(b.Begin); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return result, nil
}

func (s *Store) Get(_ context.Context, id int64) (meeting.Meeting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(id)
}

// Lock holds one lock for all planners, which is plenty for a store in memory.
func (s *Store) Lock(_ context.Context, _ string, f func(meeting.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, savedID := maps.Clone(s.meetings), s.nextID
	if err := f(tx{s}); err != nil {
		s.meetings, s.nextID = saved, savedID
		return err
	}
	return nil
}

func (s *Store) get(id int64) (meeting.Meeting, error) {
	m, ok := s.meetings[id]
	if !ok {
		return meeting.Meeting{}, meeting.ErrNotFound
	}
	return clone(m), nil
}

// tx works on the store while its lock is held.
type tx struct{ s *Store }

func (t tx) Get(_ context.Context, id int64) (meeting.Meeting, error) { return t.s.get(id) }

func (t tx) Overlapping(_ context.Context, planner string, begin, end time.Time, excludeID int64) (bool, error) {
	for _, m := range t.s.meetings {
		if m.Planner == planner && m.Status == meeting.Active && m.ID != excludeID &&
			meeting.Overlaps(m.Begin, m.End, begin, end) {
			return true, nil
		}
	}
	return false, nil
}

func (t tx) Insert(_ context.Context, m *meeting.Meeting) error {
	m.ID = t.s.nextID
	t.s.nextID++
	t.s.meetings[m.ID] = clone(*m)
	return nil
}

func (t tx) Update(_ context.Context, m meeting.Meeting) error {
	if _, ok := t.s.meetings[m.ID]; !ok {
		return meeting.ErrNotFound
	}
	t.s.meetings[m.ID] = clone(m)
	return nil
}

// clone copies the guests, so that callers cannot change a stored meeting through the slice.
func clone(m meeting.Meeting) meeting.Meeting {
	m.Guests = append([]string{}, m.Guests...)
	return m
}
