// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

const svgOpen = `<svg xmlns="http://www.w3.org/2000/svg">`
const svgClose = `</svg>`
const svgOriginal = `<?xml version="1.0" encoding="UTF-8"?>
<!-- owned private original -->
<svg xmlns="http://www.w3.org/2000/svg" width="untrusted" height="999999999">
  <title>نمونه &amp; original</title><script>/* opaque private bytes */</script>
  <image href="urn:achrix:owned-fixture"/>
</svg>
`

func svgModule() *Module {
	return &Module{config: Config{AllowedMIMEs: []string{"image/svg+xml"}}, decoders: make(chan struct{}, 2)}
}

func TestSVGExplicitSelectionAndPrivateOriginal(t *testing.T) {
	selection := []string{"image/svg+xml"}
	m, err := NewPostgres("host=/tmp dbname=unused", Config{StorageRoot: "/tmp", AllowedMIMEs: selection}, nil)
	if err != nil {
		t.Fatal(err)
	}
	selection[0] = "image/png"
	s := &Service{module: m}
	formats := s.Formats()
	if len(formats) != 1 || formats[0].MIME != "image/svg+xml" || len(formats[0].Extensions) != 1 || formats[0].Extensions[0] != "svg" {
		t.Fatal("explicit effective SVG inventory", formats)
	}
	formats[0].Extensions[0] = "changed"
	if s.Formats()[0].Extensions[0] != "svg" {
		t.Fatal("SVG inventory aliases caller")
	}
	body := []byte(svgOriginal)
	for _, name := range []string{"original.svg", "original.SVG"} {
		f := validationFile(t, body)
		got, w, h, err := m.validateUpload(context.Background(), f, name)
		if err != nil || got != "image/svg+xml" || w != 0 || h != 0 {
			t.Fatal("explicit structural SVG", got, w, h, err)
		}
	}
	for _, allowed := range [][]string{nil, CommonMIMEs()} {
		disabled := &Module{config: Config{AllowedMIMEs: allowed}}
		if _, _, _, err := disabled.validateUpload(context.Background(), validationFile(t, body), "original.svg"); !errors.Is(err, ErrInput) {
			t.Fatal("SVG joined default/common admission", err)
		}
	}
	for _, name := range []string{"original", "original.txt", "original.svgz"} {
		if _, _, _, err := m.validateUpload(context.Background(), validationFile(t, body), name); !errors.Is(err, ErrInput) {
			t.Fatal("SVG missing exact enabled extension", name, err)
		}
	}
	// Existing image byte selection still wins even when the name ends in .svg.
	images := &Module{decoders: make(chan struct{}, 2)}
	if got, w, h, err := images.validateUpload(context.Background(), validationFile(t, imageBytes(t, "png", 3, 2)), "original.svg"); err != nil || got != "image/png" || w != 3 || h != 2 {
		t.Fatal("SVG extension changed PNG byte selection", got, err)
	}
	storage := testStorage(t)
	a, err := storage.write(context.Background(), m, Asset{ID: newID(), Filename: "original.svg"}, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if a.MIME != "image/svg+xml" || a.Width != 0 || a.Height != 0 || a.Size != int64(len(body)) || a.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("SVG original metadata", a)
	}
	f, err := storage.open(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	output, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(output, body) {
		t.Fatal("SVG stored bytes were rewritten", err)
	}
}

func TestSVGCompleteXMLProfile(t *testing.T) {
	valid := []string{
		svgOpen + svgClose,
		`<s:svg xmlns:s="http://www.w3.org/2000/svg"><s:g/></s:svg>`,
		" \t\r\n<!--before-->" + svgOpen + `<![CDATA[<ordinary text>]]>` + svgClose + "\n<!--after-->",
		`<?xml version='1.0' encoding='utf-8' standalone='yes'?>` + svgOpen + `&#32;&lt;` + svgClose,
		"\ufeff" + svgOriginal,
		svgOriginal,
	}
	m := svgModule()
	for i, body := range valid {
		if _, _, _, err := m.validateUpload(context.Background(), validationFile(t, []byte(body)), "owned.svg"); err != nil {
			t.Fatal("valid complete SVG", i, err)
		}
	}
	invalid := map[string]string{
		"empty": "", "whitespace": " \n", "namespace missing": `<svg/>`,
		"wrong namespace": `<svg xmlns="urn:wrong"/>`, "wrong root": `<g xmlns="http://www.w3.org/2000/svg"/>`,
		"uppercase root": `<SVG xmlns="http://www.w3.org/2000/svg"/>`,
		"truncated":      svgOpen + `<g>`, "mismatch": svgOpen + `</g>`,
		"extra root":   svgOpen + svgClose + svgOpen + svgClose,
		"leading text": `text` + svgOpen + svgClose, "trailing text": svgOpen + svgClose + `text`,
		"outside unicode space":       "\u00a0" + svgOpen + svgClose,
		"outside CDATA":               `<![CDATA[ ]]>` + svgOpen + svgClose,
		"outside reference":           svgOpen + svgClose + `&#32;`,
		"DTD":                         `<!DOCTYPE svg>` + svgOpen + svgClose,
		"directive in root":           svgOpen + `<!anything>` + svgClose,
		"external entity declaration": `<!DOCTYPE svg SYSTEM "urn:achrix:owned-fixture">` + svgOpen + svgClose,
		"undeclared entity":           svgOpen + `&unknown;` + svgClose,
		"PI before":                   `<?owned fixture?>` + svgOpen + svgClose,
		"PI inside":                   svgOpen + `<?owned fixture?>` + svgClose,
		"PI after":                    svgOpen + svgClose + `<?owned fixture?>`,
		"second declaration":          `<?xml version="1.0"?><?xml version="1.0"?>` + svgOpen + svgClose,
		"late declaration":            ` <!--before--><?xml version="1.0"?>` + svgOpen + svgClose,
		"invalid declaration":         `<?xml extra="ignored"?>` + svgOpen + svgClose,
		"invalid declaration order":   `<?xml encoding="UTF-8" version="1.0"?>` + svgOpen + svgClose,
		"unsupported version":         `<?xml version="1.1"?>` + svgOpen + svgClose,
		"unsupported encoding":        `<?xml version="1.0" encoding="ISO-8859-1"?>` + svgOpen + svgClose,
		"UTF16":                       "\xff\xfe<\x00s\x00v\x00g\x00", "invalid UTF8": svgOpen + strings.Repeat("a", recognitionBytes) + "\xff" + svgClose,
		"NUL":                 svgOpen + "\x00" + svgClose,
		"duplicate attribute": `<svg xmlns="http://www.w3.org/2000/svg" a="1" a="2"/>`,
	}
	for name, body := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := m.validateUpload(context.Background(), validationFile(t, []byte(body)), "owned.svg"); !errors.Is(err, ErrInput) {
				t.Fatal("invalid complete XML admitted", err)
			}
			if len(m.decoders) != 0 {
				t.Fatal("failed parse leaked expensive-validation admission")
			}
		})
	}
}

func TestSVGResourceBounds(t *testing.T) {
	attributes := func(n int) string {
		var body strings.Builder
		body.WriteString(svgOpen + `<g`)
		for i := range n {
			fmt.Fprintf(&body, ` a%d="v"`, i)
		}
		body.WriteString(`/>` + svgClose)
		return body.String()
	}
	for _, pair := range []struct{ name, valid, invalid string }{
		{"bytes", svgOpen + strings.Repeat(" ", maxSVGBytes-len(svgOpen)-len(svgClose)) + svgClose, svgOpen + strings.Repeat(" ", maxSVGBytes-len(svgOpen)-len(svgClose)+1) + svgClose},
		{"depth", svgOpen + strings.Repeat(`<g>`, maxSVGDepth-1) + strings.Repeat(`</g>`, maxSVGDepth-1) + svgClose, svgOpen + strings.Repeat(`<g>`, maxSVGDepth) + strings.Repeat(`</g>`, maxSVGDepth) + svgClose},
		{"elements", svgOpen + strings.Repeat(`<g/>`, maxSVGElements-1) + svgClose, svgOpen + strings.Repeat(`<g/>`, maxSVGElements) + svgClose},
		{"attributes", attributes(maxSVGAttributes), attributes(maxSVGAttributes + 1)},
	} {
		t.Run(pair.name, func(t *testing.T) {
			m := svgModule()
			if _, _, _, err := m.validateUpload(context.Background(), validationFile(t, []byte(pair.valid)), "owned.svg"); err != nil {
				t.Fatal("inclusive bound rejected", err)
			}
			if _, _, _, err := m.validateUpload(context.Background(), validationFile(t, []byte(pair.invalid)), "owned.svg"); !errors.Is(err, ErrInput) {
				t.Fatal("exceeded bound admitted", err)
			}
		})
	}
}

// Deterministic cancellation during token processing, without timing a goroutine
// against a synchronous parser. The implementation must poll between tokens.
type svgCancelContext struct {
	context.Context
	remaining int
}

func (c *svgCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestSVGSharedAdmissionAndCancellation(t *testing.T) {
	m := svgModule()
	f := validationFile(t, []byte(svgOriginal))
	m.decoders <- struct{}{}
	m.decoders <- struct{}{}
	if _, _, _, err := m.validateUpload(context.Background(), f, "owned.svg"); !errors.Is(err, ErrLimited) {
		t.Fatal("SVG queued behind expensive validation", err)
	}
	// PNG uses the same slots while SVG is selected alongside it.
	m.config.AllowedMIMEs = []string{"image/svg+xml", "image/png"}
	if _, _, _, err := m.validateUpload(context.Background(), validationFile(t, imageBytes(t, "png", 2, 2)), "owned.png"); !errors.Is(err, ErrLimited) {
		t.Fatal("image did not share SVG admission", err)
	}
	<-m.decoders
	<-m.decoders
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, test := range []struct {
		ctx  context.Context
		want error
	}{
		{canceled, context.Canceled}, {expired, context.DeadlineExceeded},
		{&svgCancelContext{Context: context.Background(), remaining: 10}, context.Canceled},
	} {
		if _, _, _, err := m.validateUpload(test.ctx, f, "owned.svg"); !errors.Is(err, test.want) {
			t.Fatal("SVG cancellation became input failure or success", err)
		}
		if len(m.decoders) != 0 {
			t.Fatal("cancellation leaked admission")
		}
	}
	if _, _, _, err := m.validateUpload(context.Background(), f, "owned.svg"); err != nil {
		t.Fatal("admission not recovered", err)
	}
}

func TestSVGXMLCharacterRanges(t *testing.T) {
	m := svgModule()
	for _, r := range []rune{0, 1, 8, 11, 12, 0x1f, 0xfffe, 0xffff} {
		comment := "<!--owned " + string(r) + " comment-->"
		for i, body := range []string{comment + svgOpen + svgClose, svgOpen + comment + svgClose, svgOpen + svgClose + comment} {
			t.Run(fmt.Sprintf("illegal-%U-position-%d", r, i), func(t *testing.T) {
				if _, _, _, err := m.validateUpload(context.Background(), validationFile(t, []byte(body)), "owned.svg"); !errors.Is(err, ErrInput) {
					t.Fatal("illegal XML character in comment admitted", err)
				}
				if len(m.decoders) != 0 {
					t.Fatal("invalid character leaked expensive-validation admission")
				}
			})
		}
	}
	// XML 1.0 permits these exact range boundaries, including C1 characters.
	// Avoid a broad Unicode-control/noncharacter filter that changes the profile.
	for _, r := range []rune{9, 10, 13, 0x20, 0x7f, 0x85, 0xd7ff, 0xe000, 0xfffd, 0x10000, 0x10ffff} {
		comment := "<!--owned " + string(r) + " comment-->"
		body := comment + svgOpen + comment + svgClose + comment
		t.Run(fmt.Sprintf("legal-%U", r), func(t *testing.T) {
			if _, _, _, err := m.validateUpload(context.Background(), validationFile(t, []byte(body)), "owned.svg"); err != nil {
				t.Fatal("legal XML character rejected", err)
			}
		})
	}
}

func TestSVGAttributeWhitespace(t *testing.T) {
	m := svgModule()
	for _, tag := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"a="1"/>`,
		`<svg xmlns='http://www.w3.org/2000/svg'a='1'/>`,
		`<g a="1"b="2"/>`, `<g a='1'b='2'/>`,
		`<g a="1"b='2'/>`, `<g a='1'b="2"/>`,
		`<g a="first &quot; value"b="second"/>`,
		`<g xmlns:s="urn:owned"s:a="1"/>`,
	} {
		body := tag
		if strings.HasPrefix(tag, "<g") {
			body = svgOpen + tag + svgClose
		}
		t.Run(tag, func(t *testing.T) {
			if _, _, _, err := m.validateUpload(context.Background(), validationFile(t, []byte(body)), "owned.svg"); !errors.Is(err, ErrInput) {
				t.Fatal("attributes without required XML whitespace admitted", err)
			}
			if len(m.decoders) != 0 {
				t.Fatal("invalid attribute spacing leaked admission")
			}
		})
	}
	for i, whitespace := range []string{" ", "\t", "\r", "\n", "\r\n"} {
		// Both quote styles, whitespace around =, literal opposite quotes and >,
		// escaped quotes, namespace attributes and directly closed tags are valid.
		body := `<svg xmlns = 'http://www.w3.org/2000/svg'` + whitespace + `a = "literal ' and >"` + whitespace + `b='literal " and &apos;'>` +
			`<!--a="1"b="2", &#0; and &unknown; are ordinary comment text-->` +
			`<g xmlns:s="urn:owned"` + whitespace + `s:a='value'/>` +
			`<g a="&quot;"` + whitespace + `b='&apos;'></g>` + svgClose
		t.Run(fmt.Sprintf("valid-whitespace-%d", i), func(t *testing.T) {
			if _, _, _, err := m.validateUpload(context.Background(), validationFile(t, []byte(body)), "owned.svg"); err != nil {
				t.Fatal("valid quotes/attribute whitespace/comment rejected", err)
			}
		})
	}
}

func TestSVGXMLCharacterScanCancellation(t *testing.T) {
	ctx := &svgCancelContext{Context: context.Background(), remaining: 2}
	if err := validateXMLCharacters(ctx, []byte(strings.Repeat("a", recognitionBytes*2))); !errors.Is(err, context.Canceled) {
		t.Fatal("whole-document XML character scan ignored cancellation", err)
	}
}
