// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

// Package meeting holds the meetings of planners and the rules they must follow.
package meeting

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Status of a meeting.
type Status string

const (
	Active    Status = "ACTIVE"
	Finished  Status = "FINISHED"
	Cancelled Status = "CANCELLED"
)

// Meeting is planned by one planner for a time range. Begin and End are wall clock times without a zone,
// as the API sends them.
type Meeting struct {
	ID      int64
	Planner string
	Subject string
	Guests  []string
	Begin   time.Time
	End     time.Time
	Status  Status
}

// ErrNotFound is returned by a Store for an unknown id.
var ErrNotFound = errors.New("meeting not found")

// NotFoundError is returned by the Service for an unknown id.
type NotFoundError struct{ ID int64 }

func (e *NotFoundError) Error() string { return fmt.Sprintf("Meeting %d not found", e.ID) }

// RuleError means a meeting breaks a rule: its dates, an overlap, its wording or its status.
type RuleError struct{ Reason string }

func (e *RuleError) Error() string { return e.Reason }

func ruleError(format string, args ...any) error {
	return &RuleError{Reason: fmt.Sprintf(format, args...)}
}

// Store keeps the meetings.
type Store interface {
	// ByPlanner returns the planner's meetings ordered by their begin.
	ByPlanner(ctx context.Context, planner string) ([]Meeting, error)
	// Get returns ErrNotFound for an unknown id.
	Get(ctx context.Context, id int64) (Meeting, error)
	// Lock runs f in one transaction while no one else can change the planner's meetings, so that an overlap
	// check and the write that follows it cannot be overtaken by a concurrent request. When f fails,
	// nothing it wrote is kept.
	Lock(ctx context.Context, planner string, f func(Tx) error) error
}

// Tx changes meetings inside Store.Lock.
type Tx interface {
	Get(ctx context.Context, id int64) (Meeting, error)
	// Overlapping tells whether an active meeting of the planner other than excludeID overlaps [begin, end).
	Overlapping(ctx context.Context, planner string, begin, end time.Time, excludeID int64) (bool, error)
	// Insert sets the ID of the new meeting.
	Insert(ctx context.Context, m *Meeting) error
	Update(ctx context.Context, m Meeting) error
}

// Service applies the rules.
type Service struct {
	store Store
	words *BadWords
}

func NewService(store Store, words *BadWords) *Service {
	return &Service{store: store, words: words}
}

// ByPlanner returns the planner's meetings ordered by their begin.
func (s *Service) ByPlanner(ctx context.Context, planner string) ([]Meeting, error) {
	return s.store.ByPlanner(ctx, planner)
}

// Create stores a new active meeting.
func (s *Service) Create(ctx context.Context, m Meeting) (Meeting, error) {
	m.ID = 0
	m.Status = Active
	err := s.store.Lock(ctx, m.Planner, func(tx Tx) error {
		if err := s.check(ctx, tx, m.Planner, m.Subject, m.Begin, m.End, 0); err != nil {
			return err
		}
		return tx.Insert(ctx, &m)
	})
	return m, err
}

// Update changes the subject, guests and dates of an active meeting; the planner stays.
func (s *Service) Update(ctx context.Context, id int64, subject string, guests []string, begin, end time.Time) (Meeting, error) {
	current, err := s.get(ctx, id)
	if err != nil {
		return Meeting{}, err
	}
	var m Meeting
	err = s.store.Lock(ctx, current.Planner, func(tx Tx) error {
		// Read again under the lock: the meeting may have been cancelled in the meantime.
		if m, err = tx.Get(ctx, id); err != nil {
			return err
		}
		if m.Status != Active {
			return ruleError("Only active meetings can be updated, this one is %s", m.Status)
		}
		if err := s.check(ctx, tx, m.Planner, subject, begin, end, id); err != nil {
			return err
		}
		m.Subject, m.Guests, m.Begin, m.End = subject, guests, begin, end
		return tx.Update(ctx, m)
	})
	return m, s.notFound(id, err)
}

// Cancel cancels a meeting that has not finished.
func (s *Service) Cancel(ctx context.Context, id int64) (Meeting, error) {
	current, err := s.get(ctx, id)
	if err != nil {
		return Meeting{}, err
	}
	var m Meeting
	err = s.store.Lock(ctx, current.Planner, func(tx Tx) error {
		if m, err = tx.Get(ctx, id); err != nil {
			return err
		}
		if m.Status == Finished {
			return ruleError("A finished meeting cannot be cancelled")
		}
		m.Status = Cancelled
		return tx.Update(ctx, m)
	})
	return m, s.notFound(id, err)
}

func (s *Service) get(ctx context.Context, id int64) (Meeting, error) {
	m, err := s.store.Get(ctx, id)
	return m, s.notFound(id, err)
}

func (s *Service) notFound(id int64, err error) error {
	if errors.Is(err, ErrNotFound) {
		return &NotFoundError{ID: id}
	}
	return err
}

func (s *Service) check(ctx context.Context, tx Tx, planner, subject string, begin, end time.Time, excludeID int64) error {
	if !end.After(begin) {
		return ruleError("The meeting must end after it begins")
	}
	if _, found := s.words.FindIn(subject); found {
		return ruleError("The subject contains a forbidden word")
	}
	overlapping, err := tx.Overlapping(ctx, planner, begin, end, excludeID)
	if err != nil {
		return err
	}
	if overlapping {
		return ruleError("The planner already has a meeting in this time range")
	}
	return nil
}

// Overlaps tells whether [aBegin, aEnd) and [bBegin, bEnd) overlap: each one starts before the other ends.
// This covers partial overlaps and one meeting containing the other; back-to-back meetings do not overlap.
func Overlaps(aBegin, aEnd, bBegin, bEnd time.Time) bool {
	return aBegin.Before(bEnd) && aEnd.After(bBegin)
}
