-- SPDX-License-Identifier: MPL-2.0
CREATE SCHEMA media;
CREATE TABLE media.schema_migrations (
 version integer PRIMARY KEY CHECK (version > 0),
 checksum text NOT NULL CHECK (length(checksum) = 64)
);
CREATE TABLE media.assets (
 id text PRIMARY KEY CHECK (id ~ '^[A-Z2-7]{26}$'),
 state text NOT NULL CHECK (state IN ('pending','ready','deleting','deleted')),
 revision bigint NOT NULL CHECK (revision > 0),
 filename text NOT NULL CHECK (octet_length(filename) <= 256),
 mime text NOT NULL CHECK (mime IN ('','image/png','image/jpeg')),
 size bigint NOT NULL CHECK (size BETWEEN 0 AND 10485760),
 width integer NOT NULL CHECK (width BETWEEN 0 AND 4096),
 height integer NOT NULL CHECK (height BETWEEN 0 AND 4096),
 sha256 text NOT NULL CHECK (sha256 = '' OR sha256 ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL,
 CHECK (width::bigint*height::bigint <= 8388608),
 CHECK (state <> 'ready' OR (mime <> '' AND size > 0 AND width > 0 AND height > 0 AND length(sha256)=64)),
 CHECK (state <> 'deleted' OR (filename='' AND mime='' AND size=0 AND width=0 AND height=0 AND sha256=''))
);
CREATE INDEX assets_ready_list ON media.assets(id) WHERE state='ready';
CREATE INDEX assets_unfinished ON media.assets(id) WHERE state IN ('pending','deleting');
