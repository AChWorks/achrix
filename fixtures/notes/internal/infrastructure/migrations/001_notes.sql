-- SPDX-License-Identifier: MPL-2.0
CREATE TABLE notes.entries (
    id text PRIMARY KEY CHECK (id ~ '^[A-Z2-7]{26,64}$'),
    -- Match Go strings.TrimSpace's Unicode White_Space set, including ASCII controls.
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 200 AND
        length(btrim(body, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000')) > 0),
    created_at timestamptz NOT NULL
);
