// SPDX-License-Identifier: MPL-2.0
package config_test

import (
	"crypto/rand"
	"example.com/achrix-notes/internal/config"
	"testing"
)

func TestConfiguration(t *testing.T) {
	values := map[string]string{"NOTES_DATABASE_URL": "host=/tmp dbname=achrix_test_notes", "NOTES_WRITER_TOKEN": rand.Text() + rand.Text(), "NOTES_READER_TOKEN": rand.Text() + rand.Text()}
	if _, err := config.Load(func(k string) string { return values[k] }); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"NOTES_LISTEN": "0.0.0.0:8080", "NOTES_DATABASE_URL": "host=public.example dbname=notes", "NOTES_WRITER_TOKEN": "short"} {
		old := values[k]
		values[k] = v
		if _, err := config.Load(func(k string) string { return values[k] }); err == nil {
			t.Fatal("invalid config accepted", k)
		}
		values[k] = old
	}
	values["NOTES_READER_TOKEN"] = values["NOTES_WRITER_TOKEN"]
	if _, err := config.Load(func(k string) string { return values[k] }); err == nil {
		t.Fatal("same token accepted")
	}
}
