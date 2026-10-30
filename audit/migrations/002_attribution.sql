-- SPDX-License-Identifier: MPL-2.0
-- Historical/direct records retain NULL attribution. No record UPDATE or
-- invented request/client/channel backfill is performed.
-- PostgreSQL text already rejects NUL. Explicit C0/DEL/C1 ranges match the
-- API's Unicode control rejection without depending on a locale's POSIX class.
ALTER TABLE audit.records
    ADD COLUMN client_id text,
    ADD COLUMN connection_id text,
    ADD COLUMN channel text,
    ADD COLUMN request_id text,
    ADD COLUMN operation text,
    ADD CONSTRAINT records_attribution_complete CHECK (
        (client_id IS NULL AND connection_id IS NULL AND channel IS NULL
            AND request_id IS NULL AND operation IS NULL)
        OR
        (client_id IS NOT NULL AND channel IS NOT NULL
            AND request_id IS NOT NULL AND operation IS NOT NULL
            AND octet_length(client_id) BETWEEN 1 AND 256
            AND client_id !~ U&'[\0001-\001F\007F-\009F]'
            AND (connection_id IS NULL OR (octet_length(connection_id) BETWEEN 1 AND 256
                AND connection_id !~ U&'[\0001-\001F\007F-\009F]'))
            AND channel ~ '^[A-Za-z0-9._-]{1,32}$'
            AND request_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
            AND operation ~ '^[A-Za-z0-9._-]{1,96}$')
    );
