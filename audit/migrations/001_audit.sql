-- SPDX-License-Identifier: MPL-2.0
CREATE SCHEMA audit;
CREATE TABLE audit.schema_migrations (
    version integer PRIMARY KEY CHECK (version > 0),
    checksum text NOT NULL CHECK (checksum ~ '^[a-f0-9]{64}$')
);
CREATE TABLE audit.records (
    seq bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id text NOT NULL UNIQUE CHECK (id ~ '^[a-z2-7]{26}$'),
    actor text NOT NULL CHECK (actor ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
    action text NOT NULL CHECK (length(action) <= 96 AND action ~ '^[a-z][a-z0-9]*([.-][a-z0-9]+)*$'),
    target text NOT NULL CHECK (target ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
    authority text NOT NULL CHECK (length(authority) <= 96 AND authority ~ '^[a-z][a-z0-9]*([.-][a-z0-9]+)*$'),
    outcome text NOT NULL CHECK (outcome = 'succeeded'),
    occurred_at timestamptz NOT NULL
);
CREATE INDEX records_target_sequence ON audit.records(target, seq);
CREATE FUNCTION audit.reject_record_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'audit records are immutable';
END;
$$;
CREATE TRIGGER records_reject_mutation BEFORE UPDATE OR DELETE ON audit.records
    FOR EACH ROW EXECUTE FUNCTION audit.reject_record_mutation();
CREATE TRIGGER records_reject_truncate BEFORE TRUNCATE ON audit.records
    FOR EACH STATEMENT EXECUTE FUNCTION audit.reject_record_mutation();
