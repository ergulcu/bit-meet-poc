// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ergulcu/bit-meet-poc/internal/meeting"
	"github.com/ergulcu/bit-meet-poc/internal/store/memory"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	svc := meeting.NewService(memory.New(), meeting.NewBadWords([]string{"yasak"}))
	srv := httptest.NewServer(New(svc, nil, slog.New(slog.DiscardHandler)))
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, srv *httptest.Server, method, path, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

const sprint = `{"planner": "ali@example.com", "subject": "Sprint planning", "guests": ["veli@example.com"],
	"beginDate": "10-03-2026 14:00", "endDate": "10-03-2026 15:00"}`

func TestCreateAndList(t *testing.T) {
	srv := newServer(t)
	resp, body := call(t, srv, "POST", "/meetings", sprint)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if loc := resp.Header.Get("Location"); loc != "/meetings/ali@example.com" {
		t.Errorf("Location = %q", loc)
	}
	want := `{"id":1,"planner":"ali@example.com","subject":"Sprint planning","guests":["veli@example.com"],` +
		`"beginDate":"10-03-2026 14:00","endDate":"10-03-2026 15:00","status":"ACTIVE"}`
	if strings.TrimSpace(body) != want {
		t.Errorf("body = %s", body)
	}

	call(t, srv, "POST", "/meetings", `{"planner": "ali@example.com", "subject": "Daily standup",
		"beginDate": "10-03-2026 09:00", "endDate": "10-03-2026 09:15"}`)
	resp, body = call(t, srv, "GET", "/meetings/ali@example.com", "")
	var list []map[string]any
	if err := json.Unmarshal([]byte(body), &list); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if len(list) != 2 || list[0]["subject"] != "Daily standup" {
		t.Errorf("list = %s", body)
	}
	if guests, ok := list[0]["guests"].([]any); !ok || len(guests) != 0 {
		t.Errorf("no guests must be an empty list: %s", body)
	}
}

func TestTheClockHas24Hours(t *testing.T) {
	// The Java version used "hh" and turned 14:00 into 02:00.
	srv := newServer(t)
	_, body := call(t, srv, "POST", "/meetings", sprint)
	if !strings.Contains(body, `"beginDate":"10-03-2026 14:00"`) {
		t.Errorf("body = %s", body)
	}
}

func problem(t *testing.T, resp *http.Response, body string, status int) Problem {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status %d, want %d: %s", resp.StatusCode, status, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var p Problem
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInvalidInputIs400(t *testing.T) {
	srv := newServer(t)
	resp, body := call(t, srv, "POST", "/meetings", `{"planner": "not an address", "subject": "abc",
		"guests": ["veli@example.com", " "], "endDate": "10-03-2026 15:00"}`)
	p := problem(t, resp, body, http.StatusBadRequest)
	want := map[string]string{
		"planner":   "must be a well-formed email address",
		"subject":   "Subject must be between 5 to 100 characters long",
		"guests[1]": "must not be blank",
		"beginDate": "must not be null",
	}
	if len(p.Errors) != len(want) {
		t.Errorf("errors = %+v", p.Errors)
	}
	for _, e := range p.Errors {
		if want[e.Field] != e.Message {
			t.Errorf("%s: %q, want %q", e.Field, e.Message, want[e.Field])
		}
	}
	if p.Instance != "/meetings" || p.Title != "Bad Request" {
		t.Errorf("problem = %+v", p)
	}
}

func TestBadDatesAndBodiesAre400(t *testing.T) {
	srv := newServer(t)
	for _, body := range []string{
		strings.Replace(sprint, "10-03-2026 14:00", "2026-03-10T14:00", 1),
		strings.Replace(sprint, "10-03-2026 14:00", "10-03-2026 2pm", 1),
		`{"planner": `,
		`[]`,
	} {
		resp, out := call(t, srv, "POST", "/meetings", body)
		p := problem(t, resp, out, http.StatusBadRequest)
		if !strings.HasPrefix(p.Detail, "Failed to read request") {
			t.Errorf("%s: detail %q", body, p.Detail)
		}
	}
}

func TestBrokenRulesAre409(t *testing.T) {
	srv := newServer(t)
	call(t, srv, "POST", "/meetings", sprint)
	resp, body := call(t, srv, "POST", "/meetings", strings.Replace(sprint, "Sprint planning", "Overlapping one", 1))
	if p := problem(t, resp, body, http.StatusConflict); p.Detail != "The planner already has a meeting in this time range" {
		t.Errorf("detail = %q", p.Detail)
	}
	resp, body = call(t, srv, "POST", "/meetings", strings.Replace(sprint, "Sprint planning", "Bu yasak konu", 1))
	if p := problem(t, resp, body, http.StatusConflict); p.Detail != "The subject contains a forbidden word" {
		t.Errorf("detail = %q", p.Detail)
	}
}

func TestUpdate(t *testing.T) {
	srv := newServer(t)
	call(t, srv, "POST", "/meetings", sprint)
	// The planner in the body is ignored; the meeting keeps its planner.
	resp, body := call(t, srv, "PUT", "/meetings/update/1", `{"planner": "someone@example.com",
		"subject": "Sprint review", "beginDate": "10-03-2026 14:30", "endDate": "10-03-2026 16:00"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"planner":"ali@example.com"`) ||
		!strings.Contains(body, `"subject":"Sprint review"`) || !strings.Contains(body, `"guests":[]`) {
		t.Errorf("status %d: %s", resp.StatusCode, body)
	}
}

func TestUpdateValidatesWithoutAPlanner(t *testing.T) {
	srv := newServer(t)
	call(t, srv, "POST", "/meetings", sprint)
	resp, body := call(t, srv, "PUT", "/meetings/update/1", `{"subject": "  ", "beginDate": "10-03-2026 14:30",
		"endDate": "10-03-2026 16:00"}`)
	p := problem(t, resp, body, http.StatusBadRequest)
	if len(p.Errors) != 1 || p.Errors[0].Field != "subject" {
		t.Errorf("errors = %+v", p.Errors)
	}
}

func TestCancel(t *testing.T) {
	srv := newServer(t)
	call(t, srv, "POST", "/meetings", sprint)
	resp, body := call(t, srv, "PUT", "/meetings/cancel/1", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"status":"CANCELLED"`) {
		t.Errorf("status %d: %s", resp.StatusCode, body)
	}
	resp, body = call(t, srv, "PUT", "/meetings/update/1", strings.Replace(sprint, `"planner": "ali@example.com", `, "", 1))
	problem(t, resp, body, http.StatusConflict)
}

func TestUnknownAndInvalidIDs(t *testing.T) {
	srv := newServer(t)
	resp, body := call(t, srv, "PUT", "/meetings/cancel/7", "")
	if p := problem(t, resp, body, http.StatusNotFound); p.Detail != "Meeting 7 not found" {
		t.Errorf("detail = %q", p.Detail)
	}
	resp, body = call(t, srv, "PUT", "/meetings/cancel/seven", "")
	problem(t, resp, body, http.StatusBadRequest)
	resp, body = call(t, srv, "PUT", "/meetings/update/0", sprint)
	problem(t, resp, body, http.StatusBadRequest)
}

func TestHealthAndDocs(t *testing.T) {
	srv := newServer(t)
	if resp, body := call(t, srv, "GET", "/healthz", ""); resp.StatusCode != http.StatusOK || !strings.Contains(body, "UP") {
		t.Errorf("health: %d %s", resp.StatusCode, body)
	}
	if resp, body := call(t, srv, "GET", "/openapi.yaml", ""); resp.StatusCode != http.StatusOK ||
		!strings.Contains(body, "/meetings/cancel/{id}") {
		t.Errorf("openapi: %d", resp.StatusCode)
	}
	if resp, body := call(t, srv, "GET", "/docs", ""); resp.StatusCode != http.StatusOK || !strings.Contains(body, "swagger-ui") {
		t.Errorf("docs: %d", resp.StatusCode)
	}
}

func TestHealthReportsTheDatabase(t *testing.T) {
	svc := meeting.NewService(memory.New(), meeting.NewBadWords(nil))
	down := func(context.Context) error { return errors.New("connection refused") }
	srv := httptest.NewServer(New(svc, down, slog.New(slog.DiscardHandler)))
	defer srv.Close()
	if resp, body := call(t, srv, "GET", "/healthz", ""); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("health: %d %s", resp.StatusCode, body)
	}
}
