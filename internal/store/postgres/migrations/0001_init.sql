-- Copyright 2022 Mehmet ERGULCU
-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE meeting (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    planner    VARCHAR(320) NOT NULL,
    subject    VARCHAR(100) NOT NULL,
    guests     TEXT[]       NOT NULL DEFAULT '{}',
    -- Wall clock times without a zone, as the API sends them.
    begin_date TIMESTAMP    NOT NULL,
    end_date   TIMESTAMP    NOT NULL,
    status     VARCHAR(10)  NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'FINISHED', 'CANCELLED')),
    CHECK (end_date > begin_date)
);

CREATE INDEX meeting_planner_begin ON meeting (planner, begin_date);
