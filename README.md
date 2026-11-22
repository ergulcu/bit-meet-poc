# BitMeet POC

A small meeting planner API: a planner creates meetings with guests, and the service refuses meetings that
overlap another meeting of the same planner, end before they begin, or have a forbidden word in the subject.
Written in Go, with the same API as the earlier Spring Boot version.

## Run

```bash
go run ./cmd/bitmeet                 # meetings in memory, http://localhost:8088
docker compose up --build            # meetings in PostgreSQL
```

- API: http://localhost:8088/meetings
- Swagger UI: http://localhost:8088/docs
- OpenAPI document: http://localhost:8088/openapi.yaml
- Health (including the database): http://localhost:8088/healthz

| Variable | |
|---|---|
| `BITMEET_ADDR` | listen address, `:8088` |
| `BITMEET_DATABASE_URL` | PostgreSQL URL, e.g. `postgres://bitmeet:secret@localhost:5432/bitmeet`; without it the meetings are kept in memory and are gone after a restart (as with H2 in the Java version) |
| `BITMEET_BAD_WORDS` | file with the forbidden words, one per line; the [built-in list](internal/meeting/badwords.txt) otherwise |
| `BITMEET_LOG_LEVEL` | `debug`, `info`, `warn` or `error` |

The schema is created on the first start. Several instances can share one database.

## API

Dates are `dd-MM-yyyy HH:mm` (24-hour clock).

```bash
curl -X POST localhost:8088/meetings -H 'Content-Type: application/json' -d '{
  "planner": "ali@example.com",
  "subject": "Sprint planning",
  "guests": ["veli@example.com"],
  "beginDate": "10-03-2026 14:00",
  "endDate": "10-03-2026 15:00"
}'
```

| | |
|---|---|
| `POST /meetings` | create; 201 with the meeting |
| `GET /meetings/{planner}` | the planner's meetings by date |
| `PUT /meetings/update/{id}` | change subject, guests and dates (the planner stays) |
| `PUT /meetings/cancel/{id}` | cancel |

Errors are [problem details](https://www.rfc-editor.org/rfc/rfc9457): 400 for invalid input (planner and
guests must be e-mail addresses, the subject 5 to 100 characters; `errors` lists every invalid field), 409
when a rule is broken, 404 for an unknown meeting.

Rules:

- Two meetings of a planner overlap when each starts before the other ends. A meeting that ends at 10:00
  and one that begins at 10:00 do not overlap. Cancelled meetings do not count.
- The forbidden words are matched as whole words, ignoring case with Turkish rules (`GİZLİ` matches `gizli`).

## Differences from the Java version

- Two requests for the same planner at the same moment could both pass the overlap check. Now the check and
  the write run under a lock on the planner (an advisory lock in PostgreSQL), so only one of them wins.
- The meetings can be kept in PostgreSQL; the Java version had only the in-memory H2 database.
- A validation error lists every invalid field in `errors`.

## Tests

```bash
go test ./...          # the PostgreSQL tests start PostgreSQL 18 with Testcontainers; skipped without Docker
go test -short ./...   # without them
```

`BITMEET_TEST_POSTGRES` can point the tests at an existing server instead. On Docker Desktop for Windows set
`TESTCONTAINERS_RYUK_DISABLED=true` if the Testcontainers reaper fails to start.

## License

Copyright 2022 Mehmet ERGULCU. Licensed under the [Apache License 2.0](LICENSE).
