// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

package meeting_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ergulcu/bit-meet-poc/internal/meeting"
	"github.com/ergulcu/bit-meet-poc/internal/store/memory"
	"github.com/ergulcu/bit-meet-poc/internal/store/storetest"
)

var ctx = context.Background()

const planner = "ali@example.com"

func newService(t *testing.T) (*meeting.Service, *memory.Store) {
	t.Helper()
	s := memory.New()
	return meeting.NewService(s, meeting.NewBadWords([]string{"yasak"})), s
}

func create(t *testing.T, svc *meeting.Service, subject string, beginHour, endHour int) meeting.Meeting {
	t.Helper()
	m, err := svc.Create(ctx, meeting.Meeting{Planner: planner, Subject: subject,
		Begin: storetest.At(beginHour, 0), End: storetest.At(endHour, 0)})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func wantRule(t *testing.T, err error, reason string) {
	t.Helper()
	var rule *meeting.RuleError
	if !errors.As(err, &rule) || rule.Reason != reason {
		t.Errorf("err = %v, want rule %q", err, reason)
	}
}

func TestCreate(t *testing.T) {
	svc, _ := newService(t)
	m, err := svc.Create(ctx, meeting.Meeting{ID: 99, Planner: planner, Subject: "Sprint planning",
		Guests: []string{"veli@example.com"}, Begin: storetest.At(14, 0), End: storetest.At(15, 0), Status: meeting.Cancelled})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID == 99 || m.ID == 0 || m.Status != meeting.Active {
		t.Errorf("a new meeting gets its own id and is active: %+v", m)
	}
}

func TestAMeetingMustEndAfterItBegins(t *testing.T) {
	svc, _ := newService(t)
	_, err := svc.Create(ctx, meeting.Meeting{Planner: planner, Subject: "Backwards",
		Begin: storetest.At(11, 0), End: storetest.At(10, 0)})
	wantRule(t, err, "The meeting must end after it begins")
	_, err = svc.Create(ctx, meeting.Meeting{Planner: planner, Subject: "No time",
		Begin: storetest.At(11, 0), End: storetest.At(11, 0)})
	wantRule(t, err, "The meeting must end after it begins")
}

func TestForbiddenWordsAreRefused(t *testing.T) {
	svc, _ := newService(t)
	_, err := svc.Create(ctx, meeting.Meeting{Planner: planner, Subject: "Bu YASAK",
		Begin: storetest.At(10, 0), End: storetest.At(11, 0)})
	wantRule(t, err, "The subject contains a forbidden word")
}

func TestOverlapsAreRefused(t *testing.T) {
	svc, _ := newService(t)
	create(t, svc, "Existing", 10, 11)
	_, err := svc.Create(ctx, meeting.Meeting{Planner: planner, Subject: "Contains it",
		Begin: storetest.At(9, 0), End: storetest.At(12, 0)})
	wantRule(t, err, "The planner already has a meeting in this time range")

	// Back to back is fine, and so is another planner at the same time.
	create(t, svc, "Right after", 11, 12)
	if _, err := svc.Create(ctx, meeting.Meeting{Planner: "veli@example.com", Subject: "Same time",
		Begin: storetest.At(10, 0), End: storetest.At(11, 0)}); err != nil {
		t.Error(err)
	}
}

func TestUpdateDoesNotCollideWithItself(t *testing.T) {
	svc, _ := newService(t)
	m := create(t, svc, "Planning", 10, 11)
	updated, err := svc.Update(ctx, m.ID, "Longer planning", []string{"veli@example.com"},
		storetest.At(10, 0), storetest.At(12, 0))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Subject != "Longer planning" || !updated.End.Equal(storetest.At(12, 0)) || updated.Planner != planner {
		t.Errorf("updated = %+v", updated)
	}
}

func TestUpdateChecksTheOtherMeetings(t *testing.T) {
	svc, _ := newService(t)
	create(t, svc, "Morning", 9, 10)
	m := create(t, svc, "Noon", 12, 13)
	_, err := svc.Update(ctx, m.ID, "Noon", nil, storetest.At(9, 30), storetest.At(13, 0))
	wantRule(t, err, "The planner already has a meeting in this time range")
	_, err = svc.Update(ctx, m.ID, "yasak", nil, storetest.At(12, 0), storetest.At(13, 0))
	wantRule(t, err, "The subject contains a forbidden word")
}

func TestOnlyActiveMeetingsCanBeUpdated(t *testing.T) {
	svc, _ := newService(t)
	m := create(t, svc, "Planning", 10, 11)
	if _, err := svc.Cancel(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Update(ctx, m.ID, "Planning", nil, storetest.At(10, 0), storetest.At(11, 0))
	wantRule(t, err, "Only active meetings can be updated, this one is CANCELLED")
}

func TestCancelFreesTheTimeRange(t *testing.T) {
	svc, _ := newService(t)
	m := create(t, svc, "Planning", 10, 11)
	cancelled, err := svc.Cancel(ctx, m.ID)
	if err != nil || cancelled.Status != meeting.Cancelled {
		t.Fatalf("cancelled = %+v, %v", cancelled, err)
	}
	create(t, svc, "Instead", 10, 11)
}

func TestAFinishedMeetingCannotBeCancelled(t *testing.T) {
	svc, s := newService(t)
	m := create(t, svc, "Planning", 10, 11)
	m.Status = meeting.Finished
	if err := s.Lock(ctx, planner, func(tx meeting.Tx) error { return tx.Update(ctx, m) }); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Cancel(ctx, m.ID)
	wantRule(t, err, "A finished meeting cannot be cancelled")
}

func TestUnknownMeetings(t *testing.T) {
	svc, _ := newService(t)
	var notFound *meeting.NotFoundError
	if _, err := svc.Cancel(ctx, 42); !errors.As(err, &notFound) || err.Error() != "Meeting 42 not found" {
		t.Errorf("cancel: %v", err)
	}
	if _, err := svc.Update(ctx, 42, "Planning", nil, storetest.At(10, 0), storetest.At(11, 0)); !errors.As(err, &notFound) {
		t.Errorf("update: %v", err)
	}
}

func TestByPlanner(t *testing.T) {
	svc, _ := newService(t)
	create(t, svc, "Second", 12, 13)
	create(t, svc, "First", 8, 9)
	got, err := svc.ByPlanner(ctx, planner)
	if err != nil || len(got) != 2 || got[0].Subject != "First" {
		t.Errorf("got %+v, %v", got, err)
	}
}
