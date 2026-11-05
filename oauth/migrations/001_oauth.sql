-- SPDX-License-Identifier: MPL-2.0
-- OAuth data is owned by this optional component; tokens and CSRF are never stored verbatim.
CREATE TABLE oauth.grants (
  id text PRIMARY KEY CHECK(length(id) = 43),
  subject text NOT NULL,
  client_id text NOT NULL,
  issuer text NOT NULL,
  resource text NOT NULL,
  scopes text[] NOT NULL,
  created_at timestamptz NOT NULL,
  revoked_at timestamptz
);
CREATE TABLE oauth.ceremonies (
  id_hash bytea PRIMARY KEY CHECK(octet_length(id_hash) = 32),
  csrf_hash bytea NOT NULL CHECK(octet_length(csrf_hash) = 32),
  session_hash bytea NOT NULL CHECK(octet_length(session_hash) = 32),
  subject text NOT NULL,
  client_id text NOT NULL,
  redirect_uri text NOT NULL,
  issuer text NOT NULL,
  resource text NOT NULL,
  scopes text[] NOT NULL,
  client_state text NOT NULL,
  state_present boolean NOT NULL,
  challenge text NOT NULL,
  expires_at timestamptz NOT NULL,
  UNIQUE(issuer,resource,session_hash)
);
CREATE TABLE oauth.codes (
  code_hash bytea PRIMARY KEY CHECK(octet_length(code_hash) = 32),
  grant_id text NOT NULL REFERENCES oauth.grants(id),
  client_id text NOT NULL,
  redirect_uri text NOT NULL,
  challenge text NOT NULL,
  expires_at timestamptz NOT NULL
);
CREATE TABLE oauth.access_tokens (
  token_hash bytea PRIMARY KEY CHECK(octet_length(token_hash) = 32),
  grant_id text NOT NULL REFERENCES oauth.grants(id),
  expires_at timestamptz NOT NULL
);
CREATE TABLE oauth.refresh_tokens (
  token_hash bytea PRIMARY KEY CHECK(octet_length(token_hash) = 32),
  grant_id text NOT NULL REFERENCES oauth.grants(id),
  expires_at timestamptz NOT NULL,
  used_at timestamptz
);
CREATE INDEX oauth_ceremonies_expiry ON oauth.ceremonies (expires_at);
CREATE INDEX oauth_codes_expiry ON oauth.codes (expires_at);
CREATE INDEX oauth_access_expiry ON oauth.access_tokens (expires_at);
CREATE INDEX oauth_refresh_expiry ON oauth.refresh_tokens (expires_at);
CREATE INDEX oauth_access_grant ON oauth.access_tokens (grant_id);
CREATE INDEX oauth_refresh_grant ON oauth.refresh_tokens (grant_id);
