// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AChWorks/achrix/admin/localize"
)

// The executable is built exactly once with ordinary pinned module resolution.
// The third locale is written only after that build and the initial executions.
func TestLocalizationConsumerAddsFileAfterBuild(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "localization-proof")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", executable, "./cmd/localizationproof")
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build public localization consumer: %v\n%s", err, output)
	}
	digest := func() [32]byte {
		t.Helper()
		file, err := os.Open(executable)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		h := sha256.New()
		if _, err = io.Copy(h, file); err != nil {
			t.Fatal(err)
		}
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		return sum
	}
	originalDigest := digest()
	directory := filepath.Join(root, "locales")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(language string) ([]localize.Locale, string) {
		t.Helper()
		runCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		command := exec.CommandContext(runCtx, executable, "-catalog-dir", directory, "-language", language, "-name", "ACh <script>نام</script>", "-count", "0")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("run unchanged consumer (%q): %v\n%s", language, err, output)
		}
		var report struct {
			Locales []localize.Locale `json:"locales"`
			HTML    string            `json:"html"`
		}
		if err := json.Unmarshal(output, &report); err != nil {
			t.Fatalf("consumer output: %v\n%s", err, output)
		}
		if strings.Contains(report.HTML, "<script>نام</script>") || !strings.Contains(report.HTML, "&lt;script&gt;نام&lt;/script&gt;") {
			t.Fatal("named user content was not kept as escaped plain text")
		}
		return report.Locales, report.HTML
	}
	locales, english := run("")
	if len(locales) != 2 || !strings.Contains(english, `lang="en" dir="ltr"`) || !strings.Contains(english, "Hello ACh") || !strings.Contains(english, "0 notes") {
		t.Fatal("English default, discovery or plural rendering failed")
	}
	_, persian := run("fa")
	if !strings.Contains(persian, `lang="fa" dir="rtl"`) || !strings.Contains(persian, "سلام ACh") {
		t.Fatal("selectable Persian presentation failed")
	}
	third := `{"schema":1,"locale":"ar","name":"Arabic proof","direction":"rtl","domain":"notes","messages":{"title":{"other":"Third locale proof"},"greeting":{"other":"{Name} — third locale"}}}`
	if err := os.WriteFile(filepath.Join(directory, "ar.json"), []byte(third), 0600); err != nil {
		t.Fatal(err)
	}
	locales, arabic := run("ar")
	if len(locales) != 3 || !strings.Contains(arabic, `lang="ar" dir="rtl"`) || !strings.Contains(arabic, "Third locale proof") || !strings.Contains(arabic, "&lt;/script&gt; — third locale") || !strings.Contains(arabic, "0 notes") {
		t.Fatal("runtime third locale discovery, direction, reordered parameter or English fallback failed")
	}
	var found bool
	for _, locale := range locales {
		if locale.Tag == "ar" && locale.Direction == "rtl" {
			found = true
		}
	}
	if !found {
		t.Fatal("external language missing from available locales")
	}
	if digest() != originalDigest {
		t.Fatal("consumer executable changed after adding catalog")
	}
	t.Log("one public consumer build; en default, fa selection and post-build ar data-only catalog verified")
}
