// SPDX-License-Identifier: MPL-2.0
package config

import (
	"errors"
	"net"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct{ DSN, Listen, WriterToken, ReaderToken string }

// Load reads values only through the caller's runtime secret/configuration source.
func Load(get func(string) string) (Config, error) {
	c := Config{DSN: get("NOTES_DATABASE_URL"), Listen: get("NOTES_LISTEN"), WriterToken: get("NOTES_WRITER_TOKEN"), ReaderToken: get("NOTES_READER_TOKEN")}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return Config{}, errors.New("invalid listen address")
	}
	ip := net.ParseIP(host)
	p, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || p < 1 || p > 65535 {
		return Config{}, errors.New("loopback listener required")
	}
	if len(c.WriterToken) < 32 || len(c.WriterToken) > 128 || len(c.ReaderToken) < 32 || len(c.ReaderToken) > 128 || c.WriterToken == c.ReaderToken {
		return Config{}, errors.New("distinct bounded runtime tokens required")
	}
	if err := ValidateDSN(c.DSN); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Only local fixtures are supported. A remote deployment/authentication/TLS profile
// is deliberately not implied by the proving consumer.
func ValidateDSN(dsn string) error {
	if dsn == "" {
		return errors.New("database configuration required")
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	check := func(host string) bool {
		return len(host) > 0 && host[0] == '/' || host == "localhost" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
	}
	if !check(c.ConnConfig.Host) {
		return errors.New("local database required")
	}
	for _, f := range c.ConnConfig.Fallbacks {
		if !check(f.Host) {
			return errors.New("local database fallback required")
		}
	}
	return nil
}
