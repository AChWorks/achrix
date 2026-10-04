// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func formatFixture(t testing.TB, format Format) []byte {
	t.Helper()
	for _, extension := range format.Extensions {
		body, err := os.ReadFile(filepath.Join("testdata", "sample."+extension))
		if err == nil {
			return body
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	t.Fatal("missing complete fixture for", format.MIME)
	return nil
}
func validationFile(t testing.TB, body []byte) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "recognition")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if _, err := f.Write(body); err != nil {
		t.Fatal(err)
	}
	return f
}
func TestCommonFormatsAndEveryExtension(t *testing.T) {
	m := &Module{config: Config{AllowedMIMEs: CommonMIMEs()}, decoders: make(chan struct{}, 2)}
	if len(SupportedFormats()) != 37 || len(CommonMIMEs()) != 36 || slices.Contains(CommonMIMEs(), "image/svg+xml") {
		t.Fatal("finite catalog changed without fixture review")
	}
	for _, format := range SupportedFormats() {
		if !slices.Contains(CommonMIMEs(), format.MIME) {
			continue
		}
		t.Run(format.MIME, func(t *testing.T) {
			body := formatFixture(t, format)
			f := validationFile(t, body)
			for _, extension := range format.Extensions {
				got, w, h, err := m.validateUpload(context.Background(), f, "sample."+strings.ToUpper(extension))
				if err != nil || got != format.MIME {
					t.Fatalf("%s: %q %v", extension, got, err)
				}
				if format.MIME == "image/png" || format.MIME == "image/jpeg" {
					if w != 32 || h != 32 {
						t.Fatal("decoded dimensions", w, h)
					}
				} else if w != 0 || h != 0 {
					t.Fatal("opaque dimensions", w, h)
				}
			}
		})
	}
}
func TestFiniteSelectionAndInventoryOwnership(t *testing.T) {
	for _, input := range [][]string{{}, {""}, {"application/octet-stream"}, {"image/png", "image/png"}, append(CommonMIMEs(), "image/png")} {
		if _, err := selectFormats(input); !errors.Is(err, ErrConfiguration) {
			t.Fatal("invalid selection", input, err)
		}
	}
	selected := []string{"application/pdf", "image/png"}
	m, err := NewPostgres("host=/tmp dbname=unused", Config{StorageRoot: "/tmp", AllowedMIMEs: selected}, nil)
	if err != nil {
		t.Fatal(err)
	}
	selected[0] = "application/zip"
	if !m.allows("application/pdf") || m.allows("application/zip") {
		t.Fatal("caller changed configured selection")
	}
	s := &Service{module: m}
	inventory := s.Formats()
	inventory[0].MIME = "changed"
	inventory[0].Extensions[0] = "changed"
	if got := s.Formats(); len(got) != 2 || got[0].MIME != "image/png" || got[0].Extensions[0] != "png" {
		t.Fatal("effective inventory aliases caller")
	}
	all := SupportedFormats()
	all[0].MIME = "changed"
	all[0].Extensions[0] = "changed"
	if got := SupportedFormats(); got[0].MIME != "image/png" || got[0].Extensions[0] != "png" {
		t.Fatal("supported inventory aliases caller")
	}
	common := CommonMIMEs()
	common[0] = "changed"
	if CommonMIMEs()[0] != "image/png" {
		t.Fatal("common selection aliases caller")
	}
	if defaults, err := selectFormats(nil); err != nil || defaults != nil {
		t.Fatal("nil default")
	}
	if !(&Module{}).allows("image/png") || (&Module{}).allows("application/pdf") {
		t.Fatal("nil compatibility profile")
	}
	if !slices.Equal(m.config.AllowedMIMEs, []string{"application/pdf", "image/png"}) {
		t.Fatal("configuration copy")
	}
}
func TestDisabledRecognitionAndOpaqueDecodeResources(t *testing.T) {
	body := bytes.Repeat([]byte("ordinary text "), 1024)
	f := validationFile(t, body)
	m := &Module{} // no decoder slots; disabled formats cannot use them
	for _, name := range []string{"sample.pdf", "sample.txt", "sample.exe", "sample.svg", "sample"} {
		if _, _, _, err := m.validateUpload(context.Background(), f, name); !errors.Is(err, ErrInput) {
			t.Fatal("disabled", name, err)
		}
		position, err := f.Seek(0, io.SeekCurrent)
		if err != nil || position != 8 {
			t.Fatal("disabled path read beyond historical sniff", position, err)
		}
	}
	enabled := &Module{config: Config{AllowedMIMEs: []string{"application/pdf"}}}
	pdf := validationFile(t, formatFixture(t, Format{MIME: "application/pdf", Extensions: []string{"pdf"}}))
	if got, w, h, err := enabled.validateUpload(context.Background(), pdf, "sample.pdf"); err != nil || got != "application/pdf" || w != 0 || h != 0 {
		t.Fatal("opaque path required decoders", got, err)
	}
	image := validationFile(t, imageBytes(t, "png", 3, 2))
	if _, _, _, err := enabled.validateUpload(context.Background(), image, "sample.pdf"); !errors.Is(err, ErrInput) {
		t.Fatal("disabled image invoked decoder", err)
	}
	defaultImages := &Module{decoders: make(chan struct{}, 2)}
	if got, w, h, err := defaultImages.validateUpload(context.Background(), image, "unknown.exe"); err != nil || got != "image/png" || w != 3 || h != 2 {
		t.Fatal("historical filename independence", got, err)
	}
}
func TestOpaqueInputAndWholeUTF8Bounds(t *testing.T) {
	m := &Module{config: Config{AllowedMIMEs: CommonMIMEs()}}
	zip := formatFixture(t, Format{MIME: "application/zip", Extensions: []string{"zip"}})
	for _, input := range []struct {
		name string
		body []byte
	}{
		{"claimed.docx", zip}, {"claimed.xlsx", zip}, {"claimed.pptx", zip},
		{"claimed.doc", append([]byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}, make([]byte, 4088)...)},
		{"claimed.pdf", zip}, {"claimed.zip", []byte("<html>active</html>")},
		{"x.svg", []byte("<svg/>")}, {"x.html", []byte("<html>active</html>")},
		{"x.txt", append(bytes.Repeat([]byte("a"), recognitionBytes+50), 0xff)},
		{"x.csv", append(bytes.Repeat([]byte("a"), recognitionBytes+50), 0)},
		{"x.txt", []byte("legacy encoding \xe9")}, {"x.csv", []byte("a,b\n1,\x01\n")},
	} {
		f := validationFile(t, input.body)
		if _, _, _, err := m.validateUpload(context.Background(), f, input.name); !errors.Is(err, ErrInput) {
			t.Fatal("uncertain/mismatched/active/invalid input accepted", input.name, err)
		}
	}
	for _, extension := range []string{"doc", "xls", "ppt"} {
		body, err := os.ReadFile("testdata/directory-beyond-prefix." + extension)
		if err != nil {
			t.Fatal(err)
		}
		f := validationFile(t, body)
		if _, _, _, err := m.validateUpload(context.Background(), f, "sample."+extension); !errors.Is(err, ErrInput) {
			t.Fatal("beyond-prefix legacy subtype must fail closed", extension, err)
		}
	}
	valid := append(bytes.Repeat([]byte("a"), (32<<10)-1), []byte("متن\n\ufffd")...)
	f := validationFile(t, valid)
	if got, _, _, err := m.validateUpload(context.Background(), f, "x.txt"); err != nil || got != "text/plain" {
		t.Fatal("UTF8 buffer boundary", got, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := m.validateUpload(canceled, f, "x.txt"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled recognition", err)
	}
}
func TestConcurrentInventoryAndRecognition(t *testing.T) {
	m := &Module{config: Config{AllowedMIMEs: []string{"application/pdf"}}}
	body := formatFixture(t, Format{MIME: "application/pdf", Extensions: []string{"pdf"}})
	s := &Service{module: m}
	var wg sync.WaitGroup
	for range 4 {
		f := validationFile(t, body)
		wg.Go(func() {
			for range 25 {
				inventory := s.Formats()
				inventory[0].Extensions[0] = "mutated"
				inventory[0].MIME = "mutated"
				if got, _, _, err := m.validateUpload(context.Background(), f, "x.pdf"); err != nil || got != "application/pdf" {
					t.Error("concurrent admission", got, err)
					return
				}
			}
		})
	}
	wg.Wait()
}
func BenchmarkCommonRecognition(b *testing.B) {
	for _, mimeType := range []string{"application/pdf", "application/zip", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "video/webm", "audio/mpeg"} {
		var format Format
		for _, candidate := range SupportedFormats() {
			if candidate.MIME == mimeType {
				format = candidate
			}
		}
		body := formatFixture(b, format)
		b.Run(mimeType, func(b *testing.B) {
			for _, enabled := range []bool{false, true} {
				label := "disabled"
				selection := []string{"image/png"}
				if enabled {
					label = "enabled"
					selection = []string{mimeType}
				}
				b.Run(label, func(b *testing.B) {
					m := &Module{config: Config{AllowedMIMEs: selection}}
					f := validationFile(b, body)
					b.ReportAllocs()
					readBytes := min(len(body), 8)
					if enabled {
						readBytes = min(len(body), recognitionBytes)
					}
					b.SetBytes(int64(readBytes))
					b.ResetTimer()
					for b.Loop() {
						_, _, _, err := m.validateUpload(context.Background(), f, "sample."+format.Extensions[0])
						if enabled && err != nil || !enabled && !errors.Is(err, ErrInput) {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

// UTF-8 text admission also scans the complete already size-bounded file.
// Report that separate maximum-size cost, rather than implying prefix-only work.
func BenchmarkWholeUTF8Admission(b *testing.B) {
	for _, format := range []string{"text/plain", "text/csv"} {
		b.Run(format, func(b *testing.B) {
			line := []byte("plain text\n")
			extension := "txt"
			if format == "text/csv" {
				line = []byte("name,value\nalpha,1\n")
				extension = "csv"
			}
			body := bytes.Repeat(line, int(MaxUploadBytes)/len(line)+1)[:MaxUploadBytes]
			// Avoid truncating a multi-byte rune; both authored benchmark lines are ASCII.
			f := validationFile(b, body)
			m := &Module{config: Config{AllowedMIMEs: []string{format}}}
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for b.Loop() {
				got, _, _, err := m.validateUpload(context.Background(), f, "sample."+extension)
				if err != nil || got != format {
					b.Fatal(got, err)
				}
			}
		})
	}
}
