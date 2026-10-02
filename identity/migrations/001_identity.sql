-- SPDX-License-Identifier: MPL-2.0
CREATE SCHEMA identity;
CREATE TABLE identity.schema_migrations (
 version integer PRIMARY KEY CHECK (version > 0),
 checksum text NOT NULL CHECK (length(checksum) = 64)
);
CREATE TABLE identity.accounts (
 id text PRIMARY KEY CHECK (id ~ '^[A-Z2-7]{26}$'),
 login text NOT NULL UNIQUE CHECK (login ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
 enabled boolean NOT NULL,
 revision bigint NOT NULL CHECK (revision > 0),
 created_at timestamptz NOT NULL
);
CREATE TABLE identity.credentials (
 account_id text PRIMARY KEY REFERENCES identity.accounts(id),
 password_hash text NOT NULL CHECK (length(password_hash) <= 256),
 changed_at timestamptz NOT NULL
);
CREATE TABLE identity.sessions (
 token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
 account_id text NOT NULL REFERENCES identity.accounts(id),
 account_revision bigint NOT NULL CHECK (account_revision > 0),
 csrf_hash bytea NOT NULL CHECK (octet_length(csrf_hash) = 32),
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 CHECK (expires_at > created_at)
);
CREATE INDEX sessions_account ON identity.sessions(account_id);
CREATE INDEX sessions_expiry ON identity.sessions(expires_at);
