// SPDX-License-Identifier: MPL-2.0
package localize

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

func catalogFile(locale, name, direction, domain string, messages map[string]Forms) []byte {
	data, err := json.Marshal(document{1, locale, name, direction, domain, messages})
	if err != nil {
		panic(err)
	}
	return data
}
func source(data []byte) fs.FS { return fstest.MapFS{"catalog.json": &fstest.MapFile{Data: data}} }
func english(messages map[string]Forms) fs.FS {
	return source(catalogFile("en", "English", "ltr", "test", messages))
}
func loadTest(t testing.TB, sources ...fs.FS) *Catalog {
	t.Helper()
	c, err := Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func selectTest(t testing.TB, c *Catalog, tag string) *Localizer {
	t.Helper()
	l, err := c.For(tag)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestDefaultsFallbackParametersAndSafeData(t *testing.T) {
	c := loadTest(t, english(map[string]Forms{"greet": {Other: "Hello {Name}: {{literal}}"}, "only.en": {Other: "English fallback"}}), source(catalogFile("fa", "فارسی", "rtl", "test", map[string]Forms{"greet": {Other: "{{literal}}: سلام {Name}"}})))
	en := selectTest(t, c, "")
	if en.Locale().Tag != "en" || en.Locale().Direction != "ltr" {
		t.Fatal(en.Locale())
	}
	fa := selectTest(t, c, "fa")
	if fa.Locale().Direction != "rtl" {
		t.Fatal(fa.Locale())
	}
	unsafe := "<script>alert(1)</script>"
	got, err := fa.Text("test", "greet", map[string]string{"Name": unsafe})
	if err != nil || got != "{literal}: سلام "+unsafe {
		t.Fatal(got, err)
	}
	var rendered bytes.Buffer
	if err := template.Must(template.New("text").Parse(`{{.}}`)).Execute(&rendered, got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.String(), "<script>") || !strings.Contains(rendered.String(), "&lt;script&gt;") {
		t.Fatal("normal HTML escaping was lost", rendered.String())
	}
	if got, err := fa.Text("test", "only.en", nil); err != nil || got != "English fallback" {
		t.Fatal(got, err)
	}
	table, err := fa.Messages("test")
	if err != nil || table["only.en"].Locale != "en" || table["greet"].Locale != "fa" {
		t.Fatal(table, err)
	}
	serialized, err := json.Marshal(table)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, []byte("<script>")) {
		t.Fatal("JSON script data was not escaped")
	}
	// Public exports cannot mutate the catalog retained by the server.
	table["greet"] = Message{Locale: "broken", Forms: Forms{Other: "changed"}}
	locales := c.Locales()
	locales[0].Direction = "broken"
	got, err = fa.Text("test", "greet", map[string]string{"Name": "A"})
	if err != nil || got != "{literal}: سلام A" {
		t.Fatal(got, err)
	}
	if en.Locale().Direction != "ltr" {
		t.Fatal("metadata export mutated catalog")
	}
	if _, err = c.For("de"); !errors.Is(err, ErrLocale) {
		t.Fatal(err)
	}
	if _, err = fa.Text("test", "absent", nil); !errors.Is(err, ErrMessage) {
		t.Fatal(err)
	}
	if _, err = fa.Messages("missing"); !errors.Is(err, ErrMessage) {
		t.Fatal(err)
	}
}

func TestCLDRPluralsAndFallbackSourceLocale(t *testing.T) {
	baseline := english(map[string]Forms{"items": {One: "{Count} item", Other: "{Count} items"}, "translated": {Other: "Translation anchor"}})
	c := loadTest(t, baseline,
		source(catalogFile("fa", "فارسی", "rtl", "test", map[string]Forms{"items": {One: "{Count} مورد", Other: "{Count} موارد"}})),
		source(catalogFile("fr", "Français", "ltr", "test", map[string]Forms{"translated": {Other: "Ancre"}})),
		source(catalogFile("ar", "العربية", "rtl", "test", map[string]Forms{"items": {Zero: "{Count} zero", One: "{Count} one", Two: "{Count} two", Few: "{Count} few", Many: "{Count} many", Other: "{Count} other"}})))
	for _, test := range []struct {
		locale string
		count  int64
		want   string
	}{
		{"en", 1, "1 item"}, {"en", 0, "0 items"}, {"en", 2, "2 items"}, {"en", -1, "-1 item"},
		{"fa", 0, "0 مورد"}, {"fa", 1, "1 مورد"}, {"fa", 2, "2 موارد"},
		{"fr", 0, "0 items"}, // French would select one; whole fallback must use English.
		{"ar", 0, "0 zero"}, {"ar", 1, "1 one"}, {"ar", 2, "2 two"}, {"ar", 3, "3 few"}, {"ar", 11, "11 many"}, {"ar", 100, "100 other"},
		{"en", math.MinInt64, "-9223372036854775808 items"}, {"en", math.MaxInt64, "9223372036854775807 items"},
	} {
		got, err := selectTest(t, c, test.locale).Plural("test", "items", test.count, nil)
		if err != nil || got != test.want {
			t.Fatalf("%s %d: %q %v", test.locale, test.count, got, err)
		}
	}
	fr := selectTest(t, c, "fr")
	table, _ := fr.Messages("test")
	if table["items"].Locale != "en" {
		t.Fatal(table)
	}
	// A present locale's missing CLDR category uses that message's Other form.
	c = loadTest(t, baseline, source(catalogFile("ar", "العربية", "rtl", "test", map[string]Forms{"items": {Other: "{Count} fallback"}})))
	got, err := selectTest(t, c, "ar").Plural("test", "items", 2, nil)
	if err != nil || got != "2 fallback" {
		t.Fatal(got, err)
	}
}

func TestStrictCatalogValidation(t *testing.T) {
	valid := catalogFile("en", "English", "ltr", "test", map[string]Forms{"ok": {Other: "Hello {Name}"}})
	invalid := map[string][]byte{
		"malformed":            []byte(`{"schema":`),
		"trailing":             append(append([]byte{}, valid...), []byte(` {}`)...),
		"duplicate document":   []byte(`{"schema":1,"schema":1,"locale":"en","name":"English","direction":"ltr","domain":"test","messages":{"ok":{"other":"Hello"}}}`),
		"duplicate message":    []byte(`{"schema":1,"locale":"en","name":"English","direction":"ltr","domain":"test","messages":{"ok":{"other":"Hello"},"ok":{"other":"Again"}}}`),
		"duplicate form":       []byte(`{"schema":1,"locale":"en","name":"English","direction":"ltr","domain":"test","messages":{"ok":{"other":"Hello","other":"Again"}}}`),
		"unknown field":        bytes.Replace(valid, []byte(`"schema":1`), []byte(`"schema":1,"unknown":true`), 1),
		"unknown form":         bytes.Replace(valid, []byte(`"other":`), []byte(`"bogus":`), 1),
		"wrong type":           bytes.Replace(valid, []byte(`"schema":1`), []byte(`"schema":"1"`), 1),
		"schema":               bytes.Replace(valid, []byte(`"schema":1`), []byte(`"schema":2`), 1),
		"invalid UTF8":         append(append([]byte{}, valid...), 0xff),
		"high surrogate":       bytes.Replace(valid, []byte("Hello {Name}"), []byte(`\ud800 {Name}`), 1),
		"low surrogate":        bytes.Replace(valid, []byte("Hello {Name}"), []byte(`\udc00 {Name}`), 1),
		"mismatched surrogate": bytes.Replace(valid, []byte("Hello {Name}"), []byte(`\ud800\u0041 {Name}`), 1),
		"locale alias":         catalogFile("en_US", "English", "ltr", "test", map[string]Forms{"ok": {Other: "Hello"}}),
		"unknown locale":       catalogFile("zz", "Unknown", "ltr", "test", map[string]Forms{"ok": {Other: "Hello"}}),
		"undefined locale":     catalogFile("und", "Unknown", "ltr", "test", map[string]Forms{"ok": {Other: "Hello"}}),
		"direction":            catalogFile("en", "English", "auto", "test", map[string]Forms{"ok": {Other: "Hello"}}),
		"blank name":           catalogFile("en", " ", "ltr", "test", map[string]Forms{"ok": {Other: "Hello"}}),
		"control name":         catalogFile("en", "English\n", "ltr", "test", map[string]Forms{"ok": {Other: "Hello"}}),
		"domain":               catalogFile("en", "English", "ltr", "../test", map[string]Forms{"ok": {Other: "Hello"}}),
		"key":                  catalogFile("en", "English", "ltr", "test", map[string]Forms{"__proto__": {Other: "Hello"}}),
		"missing other":        catalogFile("en", "English", "ltr", "test", map[string]Forms{"ok": {One: "Hello"}}),
		"bad braces":           catalogFile("en", "English", "ltr", "test", map[string]Forms{"ok": {Other: "Hello {Name"}}),
		"invalid parameter":    catalogFile("en", "English", "ltr", "test", map[string]Forms{"ok": {Other: "Hello {Name.Method}"}}),
		"form placeholders":    catalogFile("en", "English", "ltr", "test", map[string]Forms{"ok": {One: "Hello {Name}", Other: "Hello {Other}"}}),
	}
	for name, data := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(source(data)); !errors.Is(err, ErrCatalog) {
				t.Fatal(err)
			}
		})
	}
	pair := bytes.Replace(valid, []byte("Hello {Name}"), []byte(`\ud83d\ude00 {Name}`), 1)
	c := loadTest(t, source(pair))
	got, err := selectTest(t, c, "en").Text("test", "ok", map[string]string{"Name": "N"})
	if err != nil || got != "😀 N" {
		t.Fatal(got, err)
	}
	for _, extra := range []fs.FS{
		source(valid), // same exact identity
		source(catalogFile("en", "Different", "ltr", "other", map[string]Forms{"ok": {Other: "Hello"}})),
		source(catalogFile("fa", "فارسی", "rtl", "test", map[string]Forms{"new": {Other: "Missing English"}})),
		source(catalogFile("fa", "فارسی", "rtl", "test", map[string]Forms{"ok": {Other: "سلام {Other}"}})),
	} {
		if _, err := Load(source(valid), extra); !errors.Is(err, ErrCatalog) {
			t.Fatal(err)
		}
	}
	if _, err := Load(source(catalogFile("fa", "فارسی", "rtl", "test", map[string]Forms{"ok": {Other: "سلام"}}))); !errors.Is(err, ErrCatalog) {
		t.Fatal(err)
	}
	var typedNil *countingFS
	if _, err := Load(typedNil); !errors.Is(err, ErrCatalog) {
		t.Fatal(err)
	}
	if _, err := Load(); !errors.Is(err, ErrCatalog) {
		t.Fatal(err)
	}
	if _, err := Load(nil); !errors.Is(err, ErrCatalog) {
		t.Fatal(err)
	}
}

func TestCatalogAndOutputBounds(t *testing.T) {
	for name, filesystem := range map[string]fs.FS{
		"oversized file":    source(make([]byte, MaxFileBytes+1)),
		"oversized message": english(map[string]Forms{"ok": {Other: strings.Repeat("x", MaxMessageBytes+1)}}),
		"symlink":           fstest.MapFS{"link.json": &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("x")}},
		"nonregular":        fstest.MapFS{"pipe.json": &fstest.MapFile{Mode: fs.ModeNamedPipe}},
		"deep directory":    fstest.MapFS{strings.Repeat("d/", 10) + "en.json": &fstest.MapFile{Data: catalogFile("en", "English", "ltr", "test", map[string]Forms{"ok": {Other: "X"}})}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(filesystem); !errors.Is(err, ErrCatalog) {
				t.Fatal(err)
			}
		})
	}
	tooMany := make([]fs.FS, MaxSources+1)
	if _, err := Load(tooMany...); !errors.Is(err, ErrLimit) || !errors.Is(err, ErrCatalog) {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for i := 0; i < MaxFiles+1; i++ {
		files[fmt.Sprintf("%03d.json", i)] = &fstest.MapFile{Data: []byte(`{}`)}
	}
	if _, err := Load(files); !errors.Is(err, ErrLimit) || !errors.Is(err, ErrCatalog) {
		t.Fatal(err)
	}
	// Count total bytes independently from per-file/message limits.
	bigMessages := map[string]Forms{}
	for i := 0; i < 28; i++ {
		bigMessages[fmt.Sprintf("k%d", i)] = Forms{Other: strings.Repeat("x", MaxMessageBytes)}
	}
	files = fstest.MapFS{}
	for i := 0; i < 20; i++ {
		files[fmt.Sprintf("%03d.json", i)] = &fstest.MapFile{Data: catalogFile("en", "English", "ltr", fmt.Sprintf("d%d", i), bigMessages)}
	}
	if _, err := Load(files); !errors.Is(err, ErrLimit) || !errors.Is(err, ErrCatalog) {
		t.Fatal(err)
	}
	c := loadTest(t, english(map[string]Forms{"inflate": {Other: "{Name}{Name}{Name}{Name}{Name}"}, "simple": {Other: "Hello {Name}"}, "literal": {Other: "Plain"}}))
	l := selectTest(t, c, "en")
	if _, err := l.Text("test", "inflate", map[string]string{"Name": strings.Repeat("x", MaxParameterBytes)}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	for _, params := range []map[string]string{nil, {"bad.name": "x", "Name": "N"}, {"Name": strings.Repeat("x", MaxParameterBytes+1)}, {"Name": string([]byte{0xff})}} {
		if _, err := l.Text("test", "simple", params); !errors.Is(err, ErrParameters) {
			t.Fatal(err)
		}
	}
	many := map[string]string{}
	for i := 0; i < MaxParameters+1; i++ {
		many[fmt.Sprintf("p%d", i)] = "x"
	}
	if _, err := l.Text("test", "literal", many); !errors.Is(err, ErrParameters) {
		t.Fatal(err)
	}
	if _, err := l.Plural("test", "literal", 1, map[string]string{"Count": "99"}); !errors.Is(err, ErrParameters) {
		t.Fatal(err)
	}
	if got, err := l.Text("test", "literal", map[string]string{"Extra": "accepted"}); err != nil || got != "Plain" {
		t.Fatal(got, err)
	}
}

type countingFS struct {
	source    fs.FS
	opens     atomic.Int64
	forbidden atomic.Bool
}

func (c *countingFS) Open(name string) (fs.File, error) {
	if c.forbidden.Load() {
		return nil, errors.New("unexpected lookup-time I/O")
	}
	c.opens.Add(1)
	return c.source.Open(name)
}
func TestNoLookupIOAndConcurrentImmutableReaders(t *testing.T) {
	tracked := &countingFS{source: english(map[string]Forms{"ok": {Other: "Hello {Name}"}})}
	c := loadTest(t, tracked)
	l := selectTest(t, c, "en")
	before := tracked.opens.Load()
	tracked.forbidden.Store(true)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				got, err := l.Text("test", "ok", map[string]string{"Name": "N"})
				if err != nil || got != "Hello N" {
					t.Error(got, err)
				}
			}
		}()
	}
	wg.Wait()
	if tracked.opens.Load() != before {
		t.Fatal("lookup touched filesystem")
	}
}

func BenchmarkCatalogLoad(b *testing.B) {
	filesystem := english(map[string]Forms{"items": {One: "{Count} item", Other: "{Count} items"}, "hello": {Other: "Hello {Name}"}})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Load(filesystem); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkStaticText(b *testing.B) {
	l := selectTest(b, loadTest(b, english(map[string]Forms{"hello": {Other: "Hello"}})), "en")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := l.Text("test", "hello", nil); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkText(b *testing.B) {
	l := selectTest(b, loadTest(b, english(map[string]Forms{"hello": {Other: "Hello {Name}"}})), "en")
	params := map[string]string{"Name": "AChrix"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := l.Text("test", "hello", params); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkPlural(b *testing.B) {
	l := selectTest(b, loadTest(b, english(map[string]Forms{"items": {One: "{Count} item", Other: "{Count} items"}})), "en")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := l.Plural("test", "items", 2, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func FuzzCatalogParser(f *testing.F) {
	f.Add(catalogFile("en", "English", "ltr", "test", map[string]Forms{"ok": {Other: "Hello {Name}"}}))
	f.Add([]byte(`{"messages":{"a":{"other":"\ud800"}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxFileBytes {
			return
		}
		_, _ = parseDocument(data)
	})
}

func BenchmarkCatalogLoad100(b *testing.B) {
	en, fa := map[string]Forms{}, map[string]Forms{}
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("message.%03d", i)
		en[key] = Forms{Other: "Hello {Name}. Choose a permitted operation."}
		fa[key] = Forms{Other: "سلام {Name}. یک عملیات مجاز را انتخاب کنید."}
	}
	sources := []fs.FS{english(en), source(catalogFile("fa", "فارسی", "rtl", "test", fa))}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Load(sources...); err != nil {
			b.Fatal(err)
		}
	}
}

func TestAggregateMessageAndDirectoryBounds(t *testing.T) {
	messages := map[string]Forms{}
	for i := 0; i < 256; i++ {
		messages[fmt.Sprintf("m%03d", i)] = Forms{Other: "Hello"}
	}
	files := fstest.MapFS{}
	for i := 0; i < 33; i++ {
		files[fmt.Sprintf("%03d.json", i)] = &fstest.MapFile{Data: catalogFile("en", "English", "ltr", fmt.Sprintf("d%d", i), messages)}
	}
	if _, err := Load(files); !errors.Is(err, ErrCatalog) || !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	files = fstest.MapFS{}
	for i := 0; i < MaxEntries+1; i++ {
		files[fmt.Sprintf("%04d.txt", i)] = &fstest.MapFile{Data: nil}
	}
	if _, err := Load(files); !errors.Is(err, ErrCatalog) || !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	l := selectTest(t, loadTest(t, english(map[string]Forms{"hello": {Other: "Hello"}})), "en")
	if _, err := l.catalog.For(strings.Repeat("e", 65)); !errors.Is(err, ErrLocale) {
		t.Fatal(err)
	}
	if _, err := l.Text(strings.Repeat("d", 65), "hello", nil); !errors.Is(err, ErrMessage) {
		t.Fatal(err)
	}
	if _, err := l.Messages(strings.Repeat("d", 65)); !errors.Is(err, ErrMessage) {
		t.Fatal(err)
	}
}

func TestSchemaFieldNamesAreExactButMessageIDsAreCaseSensitive(t *testing.T) {
	valid := catalogFile("en", "English", "ltr", "test", map[string]Forms{"greeting": {Other: "Hello"}})
	// encoding/json's struct decoder accepts case-insensitive names by default;
	// catalogs must reject aliases even when they occur without a collision.
	for _, field := range []string{"schema", "locale", "name", "direction", "domain", "messages"} {
		alias := strings.ToUpper(field[:1]) + field[1:]
		for _, collision := range []bool{false, true} {
			t.Run(field+fmt.Sprint(collision), func(t *testing.T) {
				var data []byte
				if collision {
					data = append([]byte(`{"`+alias+`":null,`), valid[1:]...)
				} else {
					data = bytes.Replace(valid, []byte(`"`+field+`":`), []byte(`"`+alias+`":`), 1)
				}
				if _, err := Load(source(data)); !errors.Is(err, ErrCatalog) {
					t.Fatal("case alias accepted", err)
				}
			})
		}
	}
	for _, form := range []string{"zero", "one", "two", "few", "many", "other"} {
		for _, collision := range []bool{false, true} {
			t.Run(form+fmt.Sprint(collision), func(t *testing.T) {
				alias := strings.ToUpper(form[:1]) + form[1:]
				content := `"` + alias + `":"Hello"`
				if form != "other" || collision {
					content += `,"other":"Hello"`
				}
				if collision && form != "other" {
					content += `,"` + form + `":"Hello"`
				}
				data := []byte(`{"schema":1,"locale":"en","name":"English","direction":"ltr","domain":"test","messages":{"greeting":{` + content + `}}}`)
				if _, err := Load(source(data)); !errors.Is(err, ErrCatalog) {
					t.Fatal("case alias accepted", err)
				}
			})
		}
	}
	for _, data := range [][]byte{
		[]byte(`{"schema":1,"s\u0063hema":1,"locale":"en","name":"English","direction":"ltr","domain":"test","messages":{"greeting":{"other":"Hello"}}}`),
		[]byte(`{"schema":1,"locale":"en","name":"English","direction":"ltr","domain":"test","messages":{"greeting":{"other":"Hello","o\u0074her":"Again"}}}`),
	} {
		if _, err := Load(source(data)); !errors.Is(err, ErrCatalog) {
			t.Fatal("escaped duplicate accepted", err)
		}
	}
	// Escaped spellings denote the same logical valid field when not duplicated.
	escaped := bytes.Replace(valid, []byte(`"schema":`), []byte(`"s\u0063hema":`), 1)
	loadTest(t, source(escaped))
	c := loadTest(t, english(map[string]Forms{"greeting": {Other: "Lowercase"}, "Greeting": {Other: "Uppercase"}}))
	l := selectTest(t, c, "en")
	for key, want := range map[string]string{"greeting": "Lowercase", "Greeting": "Uppercase"} {
		got, err := l.Text("test", key, nil)
		if err != nil || got != want {
			t.Fatal(key, got, err)
		}
	}
}

func TestSuppliedSchemaValuesRejectNullAndWrongTypes(t *testing.T) {
	valid := catalogFile("en", "English", "ltr", "test", map[string]Forms{"greeting": {Other: "Hello"}})
	for _, field := range []string{"schema", "locale", "name", "direction", "domain", "messages"} {
		t.Run("null "+field, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(valid, &fields); err != nil {
				t.Fatal(err)
			}
			fields[field] = json.RawMessage("null")
			data, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Load(source(data)); !errors.Is(err, ErrCatalog) {
				t.Fatal("null field accepted", err)
			}
		})
	}
	for _, form := range []string{"zero", "one", "two", "few", "many", "other"} {
		for _, value := range []string{"null", "1", "false", "[]", "{}"} {
			t.Run(form+value, func(t *testing.T) {
				content := `"` + form + `":` + value
				if form != "other" {
					content += `,"other":"Hello"`
				}
				data := []byte(`{"schema":1,"locale":"en","name":"English","direction":"ltr","domain":"test","messages":{"greeting":{` + content + `}}}`)
				if _, err := Load(source(data)); !errors.Is(err, ErrCatalog) {
					t.Fatal("nonstring form accepted", err)
				}
			})
		}
	}
	for _, value := range []string{"null", "1", "false", "[]", "\"text\""} {
		data := []byte(`{"schema":1,"locale":"en","name":"English","direction":"ltr","domain":"test","messages":{"greeting":` + value + `}}`)
		if _, err := Load(source(data)); !errors.Is(err, ErrCatalog) {
			t.Fatal("nonobject message accepted", value, err)
		}
	}
}
