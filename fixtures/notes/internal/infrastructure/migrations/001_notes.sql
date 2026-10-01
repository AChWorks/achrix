-- SPDX-License-Identifier: MPL-2.0
CREATE TABLE notes.entries (
    id text PRIMARY KEY CHECK (id ~ '^[A-Z2-7]{26,64}$'),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 200 AND length(btrim(body)) > 0),
    created_at timestamptz NOT NULL
);
