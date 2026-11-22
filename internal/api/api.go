// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

// Package api is the HTTP interface. Errors are RFC 9457 problem details: 400 for invalid input,
// 404 for an unknown meeting and 409 when a rule is broken.
package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ergulcu/bit-meet-poc/internal/meeting"
)

// DateLayout is dd-MM-yyyy HH:mm, on a 24-hour clock.
const DateLayout = "02-01-2006 15:04"

// Date is a wall clock time in DateLayout.
type Date time.Time

func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(d).Format(DateLayout))
}

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("dates are strings in the form dd-MM-yyyy HH:mm")
	}
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return fmt.Errorf("%q is not a date in the form dd-MM-yyyy HH:mm", s)
	}
	*d = Date(t)
	return nil
}

type meetingRequest struct {
	Planner   string   `json:"planner"`
	Subject   string   `json:"subject"`
	Guests    []string `json:"guests"`
	BeginDate *Date    `json:"beginDate"`
	EndDate   *Date    `json:"endDate"`
}

type meetingResponse struct {
	ID        int64          `json:"id"`
	Planner   string         `json:"planner"`
	Subject   string         `json:"subject"`
	Guests    []string       `json:"guests"`
	BeginDate Date           `json:"beginDate"`
	EndDate   Date           `json:"endDate"`
	Status    meeting.Status `json:"status"`
}

func response(m meeting.Meeting) meetingResponse {
	guests := m.Guests
	if guests == nil {
		guests = []string{}
	}
	return meetingResponse{ID: m.ID, Planner: m.Planner, Subject: m.Subject, Guests: guests,
		BeginDate: Date(m.Begin), EndDate: Date(m.End), Status: m.Status}
}

// FieldError is one invalid field of a request.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Problem is an RFC 9457 problem detail.
type Problem struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Detail   string       `json:"detail,omitempty"`
	Instance string       `json:"instance,omitempty"`
	Errors   []FieldError `json:"errors,omitempty"`
}

//go:embed openapi.yaml
var openAPI []byte

//go:embed docs.html
var docsPage []byte

type server struct {
	svc  *meeting.Service
	ping func(context.Context) error
	log  *slog.Logger
}

// New returns the handler. ping checks the database for /healthz; it may be nil.
func New(svc *meeting.Service, ping func(context.Context) error, log *slog.Logger) http.Handler {
	s := &server{svc: svc, ping: ping, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /meetings", s.create)
	mux.HandleFunc("GET /meetings/{planner}", s.list)
	mux.HandleFunc("PUT /meetings/update/{id}", s.update)
	mux.HandleFunc("PUT /meetings/cancel/{id}", s.cancel)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Write(openAPI) //nolint:errcheck
	})
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(docsPage) //nolint:errcheck
	})
	return s.logged(mux)
}

func (s *server) list(w http.ResponseWriter, r *http.Request) {
	meetings, err := s.svc.ByPlanner(r.Context(), r.PathValue("planner"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]meetingResponse, len(meetings))
	for i, m := range meetings {
		out[i] = response(m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) create(w http.ResponseWriter, r *http.Request) {
	var req meetingRequest
	if !s.decode(w, r, &req) {
		return
	}
	if errs := validate(&req, true); len(errs) > 0 {
		invalid(w, r, errs)
		return
	}
	m, err := s.svc.Create(r.Context(), meeting.Meeting{Planner: req.Planner, Subject: req.Subject, Guests: req.Guests,
		Begin: time.Time(*req.BeginDate), End: time.Time(*req.EndDate)})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/meetings/"+url.PathEscape(m.Planner))
	writeJSON(w, http.StatusCreated, response(m))
}

// update changes subject, guests and dates; a planner in the body is ignored.
func (s *server) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req meetingRequest
	if !s.decode(w, r, &req) {
		return
	}
	if errs := validate(&req, false); len(errs) > 0 {
		invalid(w, r, errs)
		return
	}
	m, err := s.svc.Update(r.Context(), id, req.Subject, req.Guests, time.Time(*req.BeginDate), time.Time(*req.EndDate))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response(m))
}

func (s *server) cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := s.svc.Cancel(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response(m))
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if s.ping != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "DOWN"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "UP"})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeProblem(w, r, http.StatusBadRequest, "The meeting id must be a positive number", nil)
		return 0, false
	}
	return id, true
}

func (s *server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "Failed to read request: "+err.Error(), nil)
		return false
	}
	return true
}

func validate(req *meetingRequest, withPlanner bool) []FieldError {
	var errs []FieldError
	add := func(field, message string) { errs = append(errs, FieldError{field, message}) }
	if withPlanner {
		email(add, "planner", req.Planner)
	}
	switch n := utf8.RuneCountInString(req.Subject); {
	case strings.TrimSpace(req.Subject) == "":
		add("subject", "must not be blank")
	case n < 5 || n > 100:
		add("subject", "Subject must be between 5 to 100 characters long")
	}
	for i, g := range req.Guests {
		email(add, fmt.Sprintf("guests[%d]", i), g)
	}
	if req.BeginDate == nil {
		add("beginDate", "must not be null")
	}
	if req.EndDate == nil {
		add("endDate", "must not be null")
	}
	return errs
}

func email(add func(string, string), field, value string) {
	if strings.TrimSpace(value) == "" {
		add(field, "must not be blank")
		return
	}
	a, err := mail.ParseAddress(value)
	if err != nil || a.Address != value || a.Name != "" {
		add(field, "must be a well-formed email address")
	}
}

func invalid(w http.ResponseWriter, r *http.Request, errs []FieldError) {
	writeProblem(w, r, http.StatusBadRequest, "Invalid request content.", errs)
}

func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var rule *meeting.RuleError
	var notFound *meeting.NotFoundError
	switch {
	case errors.As(err, &rule):
		writeProblem(w, r, http.StatusConflict, rule.Reason, nil)
	case errors.As(err, &notFound):
		writeProblem(w, r, http.StatusNotFound, notFound.Error(), nil)
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeProblem(w, r, http.StatusInternalServerError, "", nil)
	}
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string, errs []FieldError) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, //nolint:errcheck
		Detail: detail, Instance: r.URL.Path, Errors: errs})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (s *server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.log.Debug("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "took", time.Since(start))
	})
}
