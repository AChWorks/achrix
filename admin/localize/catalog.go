// SPDX-License-Identifier: MPL-2.0
// Package localize loads immutable, data-only message catalogs. Products own
// source selection, preference persistence and explicit replacement on reload.
// It imports neither the Admin shell nor Core and performs no lookup-time I/O.
package localize

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/language"
)

// Fixed limits bound startup work, retained catalogs and formatted output.
const (
	MaxSources        = 32
	MaxFiles          = 128
	MaxEntries        = 4096
	MaxFileBytes      = 256 << 10
	MaxTotalBytes     = 4 << 20
	MaxMessages       = 8192
	MaxLocales        = 32
	MaxDomains        = 64
	MaxMessageBytes   = 8 << 10
	MaxOutputBytes    = 32 << 10
	MaxParameters     = 32
	MaxParameterBytes = 8 << 10
)

var (
	ErrCatalog    = errors.New("invalid localization catalog")
	ErrLocale     = errors.New("unsupported localization locale")
	ErrMessage    = errors.New("unknown localization message")
	ErrParameters = errors.New("invalid localization parameters")
	ErrLimit      = errors.New("localization limit exceeded")
)

// Locale is presentation metadata, not a content, calendar or permission policy.
type Locale struct {
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
}

// Forms contains complete messages, not HTML or executable templates. Other is
// required; absent categories use Other. Parameters use {Name}; {{ and }} escape
// literal braces. Plural uses CLDR cardinal categories for integral quantities.
type Forms struct {
	Zero  string `json:"zero,omitempty"`
	One   string `json:"one,omitempty"`
	Two   string `json:"two,omitempty"`
	Few   string `json:"few,omitempty"`
	Many  string `json:"many,omitempty"`
	Other string `json:"other"`
}

// Message is a defensive browser export. Locale is the message's source locale,
// which can differ from the selected locale after whole-message fallback.
type Message struct {
	Locale string `json:"locale"`
	Forms  Forms  `json:"forms"`
}

type document struct {
	Schema    int              `json:"schema"`
	Locale    string           `json:"locale"`
	Name      string           `json:"name"`
	Direction string           `json:"direction"`
	Domain    string           `json:"domain"`
	Messages  map[string]Forms `json:"messages"`
}
type compiledMessage struct {
	forms    Forms
	patterns map[string]pattern
	names    []string
	origin   string
}

// Catalog is immutable after Load and safe for concurrent readers. There is no
// process-global locale and no automatic file watcher. Load a replacement before
// publishing it through the product's own instance/reload lifecycle.
type Catalog struct {
	locales  map[string]Locale
	tags     map[string]language.Tag
	messages map[string]map[string]map[string]*compiledMessage // domain, locale, key
}

// Load reads all .json files below each source root once. Use fs.Sub for embedded
// Module roots and a product-selected fs.FS for installed language packs. Sources
// add domains/messages, not ordered overrides: duplicate identities are errors.
// Symlinks, nonregular JSON files, excessive depth and unsupported directory
// readers are rejected. The owner must also trust the supplied FS root itself.
func Load(sources ...fs.FS) (*Catalog, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("%w: no sources", ErrCatalog)
	}
	if len(sources) > MaxSources {
		return nil, fmt.Errorf("%w: %w: source count", ErrCatalog, ErrLimit)
	}
	c := &Catalog{locales: map[string]Locale{}, tags: map[string]language.Tag{}, messages: map[string]map[string]map[string]*compiledMessage{}}
	files, entries, total, messages := 0, 0, 0, 0
	for i, source := range sources {
		if nilFS(source) {
			return nil, fmt.Errorf("%w: source %d is nil", ErrCatalog, i+1)
		}
		paths, err := catalogPaths(source, &entries)
		if err != nil {
			return nil, fmt.Errorf("%w: source %d: %w", ErrCatalog, i+1, err)
		}
		for _, path := range paths {
			origin := fmt.Sprintf("source %d:%s", i+1, path)
			files++
			if files > MaxFiles {
				return nil, fmt.Errorf("%w: %s: %w: file count", ErrCatalog, origin, ErrLimit)
			}
			data, err := readCatalog(source, path)
			if err != nil {
				return nil, fmt.Errorf("%w: %s: %w", ErrCatalog, origin, err)
			}
			total += len(data)
			if total > MaxTotalBytes {
				return nil, fmt.Errorf("%w: %s: %w: total bytes", ErrCatalog, origin, ErrLimit)
			}
			doc, err := parseDocument(data)
			if err != nil {
				return nil, fmt.Errorf("%w: %s: %w", ErrCatalog, origin, err)
			}
			if err = c.add(doc, origin, &messages); err != nil {
				return nil, err
			}
		}
	}
	if _, ok := c.locales["en"]; !ok {
		return nil, fmt.Errorf("%w: English locale is required", ErrCatalog)
	}
	for domain, locales := range c.messages {
		english := locales["en"]
		if len(english) == 0 {
			return nil, fmt.Errorf("%w: domain %s has no English baseline", ErrCatalog, domain)
		}
		for locale, keys := range locales {
			for key, value := range keys {
				baseline, ok := english[key]
				if !ok {
					return nil, fmt.Errorf("%w: %s: %s/%s has no English baseline", ErrCatalog, value.origin, domain, key)
				}
				if !sameNames(baseline.names, value.names) {
					return nil, fmt.Errorf("%w: %s: %s/%s (%s) placeholders differ from English", ErrCatalog, value.origin, domain, key, locale)
				}
			}
		}
	}
	return c, nil
}

func catalogPaths(source fs.FS, entries *int) ([]string, error) {
	paths := []string{}
	var visit func(string, int) error
	visit = func(path string, depth int) error {
		if depth > 8 {
			return fmt.Errorf("%w: directory depth", ErrLimit)
		}
		f, err := source.Open(path)
		if err != nil {
			return fmt.Errorf("open directory %s: %w", path, err)
		}
		reader, ok := f.(fs.ReadDirFile)
		if !ok {
			_ = f.Close()
			return fmt.Errorf("directory %s does not support bounded reads", path)
		}
		children := []fs.DirEntry{}
		for {
			batch, readErr := reader.ReadDir(64)
			*entries += len(batch)
			if *entries > MaxEntries {
				_ = f.Close()
				return fmt.Errorf("%w: directory entries", ErrLimit)
			}
			children = append(children, batch...)
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = f.Close()
				return fmt.Errorf("read directory %s: %w", path, readErr)
			}
			if len(batch) == 0 {
				_ = f.Close()
				return fmt.Errorf("directory %s made no read progress", path)
			}
		}
		if err = f.Close(); err != nil {
			return fmt.Errorf("close directory %s: %w", path, err)
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			name := child.Name()
			if len(name) > 255 || !fs.ValidPath(name) || strings.Contains(name, "/") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
				return fmt.Errorf("unsafe directory entry in %s", path)
			}
			next := name
			if path != "." {
				next = path + "/" + name
			}
			if child.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("symlink %s is not supported", next)
			}
			if child.IsDir() {
				if err := visit(next, depth+1); err != nil {
					return err
				}
				continue
			}
			if strings.HasSuffix(name, ".json") {
				if !child.Type().IsRegular() {
					return fmt.Errorf("catalog %s is not a regular file", next)
				}
				paths = append(paths, next)
				if len(paths) > MaxFiles {
					return fmt.Errorf("%w: file count", ErrLimit)
				}
			}
		}
		return nil
	}
	if err := visit(".", 0); err != nil {
		return nil, err
	}
	return paths, nil
}

func readCatalog(source fs.FS, path string) ([]byte, error) {
	f, err := source.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("not a regular file")
	}
	if info.Size() > MaxFileBytes {
		_ = f.Close()
		return nil, fmt.Errorf("%w: file bytes", ErrLimit)
	}
	data, readErr := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("%w: file bytes", ErrLimit)
	}
	return data, nil
}

func parseDocument(data []byte) (document, error) {
	var doc document
	if !utf8.Valid(data) {
		return doc, errors.New("invalid UTF-8")
	}
	if err := unicodeEscapes(data); err != nil {
		return doc, err
	}
	// encoding/json otherwise replaces invalid UTF-8 and silently accepts duplicate
	// keys and matches struct fields case-insensitively. The bounded token pass
	// rejects duplicates and requires exact schema field names before decoding.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := uniqueValue(decoder, 0, documentObject); err != nil {
		return doc, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return doc, errors.New("trailing JSON data")
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return doc, errors.New("unsupported catalog structure or field type")
	}
	return doc, nil
}

type objectKind uint8

const (
	valueObject objectKind = iota
	documentObject
	messagesObject
	formsObject
	stringValue
	numberValue
)

// Message identifiers remain arbitrary case-sensitive map keys; only the known
// document and Forms schema fields are restricted. Decoded keys make escaped
// spellings obey the same exact-name and duplicate rules.
func childObject(kind objectKind, name string) (objectKind, error) {
	switch kind {
	case documentObject:
		switch name {
		case "schema":
			return numberValue, nil
		case "locale", "name", "direction", "domain":
			return stringValue, nil
		case "messages":
			return messagesObject, nil
		default:
			return valueObject, errors.New("unsupported catalog field")
		}
	case messagesObject:
		return formsObject, nil
	case formsObject:
		switch name {
		case "zero", "one", "two", "few", "many", "other":
			return stringValue, nil
		}
		return valueObject, errors.New("unsupported plural form")
	}
	return valueObject, nil
}

func uniqueValue(decoder *json.Decoder, depth int, kind objectKind) error {
	if depth > 8 {
		return fmt.Errorf("%w: JSON depth", ErrLimit)
	}
	token, err := decoder.Token()
	if err != nil {
		return errors.New("malformed JSON")
	}
	switch kind {
	case documentObject, messagesObject, formsObject:
		if token != json.Delim('{') {
			return errors.New("catalog structure requires an object")
		}
	case stringValue:
		if _, ok := token.(string); !ok {
			return errors.New("catalog field requires a string")
		}
	case numberValue:
		if _, ok := token.(json.Number); !ok {
			return errors.New("schema requires a number")
		}
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return errors.New("malformed JSON object")
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if seen[name] {
				return errors.New("duplicate JSON object key")
			}
			seen[name] = true
			child, err := childObject(kind, name)
			if err != nil {
				return err
			}
			if err = uniqueValue(decoder, depth+1, child); err != nil {
				return err
			}
		}
		close, err := decoder.Token()
		if err != nil || close != json.Delim('}') {
			return errors.New("malformed JSON object")
		}
	case '[':
		for decoder.More() {
			if err := uniqueValue(decoder, depth+1, valueObject); err != nil {
				return err
			}
		}
		close, err := decoder.Token()
		if err != nil || close != json.Delim(']') {
			return errors.New("malformed JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func (c *Catalog) add(doc document, origin string, count *int) error {
	fail := func(reason string) error { return fmt.Errorf("%w: %s: %s", ErrCatalog, origin, reason) }
	if doc.Schema != 1 {
		return fail("unsupported schema")
	}
	if len(doc.Locale) > 64 {
		return fail("invalid locale")
	}
	tag, err := language.Parse(doc.Locale)
	if err != nil || tag.String() != doc.Locale || tag == language.Und {
		return fail("locale must be a canonical BCP 47 tag")
	}
	if strings.TrimSpace(doc.Name) == "" || len(doc.Name) > 128 || strings.IndexFunc(doc.Name, unicode.IsControl) >= 0 {
		return fail("invalid display name")
	}
	if doc.Direction != "ltr" && doc.Direction != "rtl" {
		return fail("direction must be ltr or rtl")
	}
	if !identifier(doc.Domain, 64) || len(doc.Messages) == 0 {
		return fail("invalid domain or empty messages")
	}
	metadata := Locale{doc.Locale, doc.Name, doc.Direction}
	if old, ok := c.locales[doc.Locale]; ok && old != metadata {
		return fail("conflicting locale metadata")
	}
	c.locales[doc.Locale] = metadata
	c.tags[doc.Locale] = tag
	if len(c.locales) > MaxLocales {
		return fmt.Errorf("%w: %s: %w: locales", ErrCatalog, origin, ErrLimit)
	}
	if c.messages[doc.Domain] == nil {
		c.messages[doc.Domain] = map[string]map[string]*compiledMessage{}
	}
	if len(c.messages) > MaxDomains {
		return fmt.Errorf("%w: %s: %w: domains", ErrCatalog, origin, ErrLimit)
	}
	if c.messages[doc.Domain][doc.Locale] == nil {
		c.messages[doc.Domain][doc.Locale] = map[string]*compiledMessage{}
	}
	target := c.messages[doc.Domain][doc.Locale]
	for key, forms := range doc.Messages {
		if !identifier(key, 128) {
			return fail("invalid message key in domain " + doc.Domain)
		}
		if _, ok := target[key]; ok {
			return fail("duplicate " + doc.Domain + "/" + key + " (" + doc.Locale + ")")
		}
		*count++
		if *count > MaxMessages {
			return fmt.Errorf("%w: %s: %w: messages", ErrCatalog, origin, ErrLimit)
		}
		compiled, err := compile(forms)
		if err != nil {
			return fmt.Errorf("%w: %s: %s/%s (%s): %w", ErrCatalog, origin, doc.Domain, key, doc.Locale, err)
		}
		compiled.origin = origin
		target[key] = compiled
	}
	return nil
}

// Locales returns a sorted defensive copy of loaded metadata. Products decide
// which loaded languages they expose and how they persist a user's selection.
func (c *Catalog) Locales() []Locale {
	if c == nil {
		return nil
	}
	result := make([]Locale, 0, len(c.locales))
	for _, locale := range c.locales {
		result = append(result, locale)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Tag < result[j].Tag })
	return result
}

// For selects one explicitly loaded locale. Empty selects English. Unsupported
// input is an error rather than a silent change to the product's preference.
func (c *Catalog) For(tag string) (*Localizer, error) {
	if tag == "" {
		tag = "en"
	}
	if len(tag) > 64 || c == nil {
		return nil, ErrLocale
	}
	parsed, err := language.Parse(tag)
	if err != nil {
		return nil, ErrLocale
	}
	metadata, ok := c.locales[parsed.String()]
	if !ok {
		return nil, ErrLocale
	}
	return &Localizer{catalog: c, locale: metadata}, nil
}

func identifier(value string, max int) bool {
	if len(value) == 0 || len(value) > max {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		letter := ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
		if !letter && (i == 0 || !(ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-')) {
			return false
		}
	}
	return true
}

// Only startup needs reflection: a typed-nil implementation is a configuration
// error, not permission to invoke methods on a nil provider.
func nilFS(source fs.FS) bool {
	if source == nil {
		return true
	}
	value := reflect.ValueOf(source)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	}
	return false
}

// JSON permits escaped UTF-16, but encoding/json silently substitutes U+FFFD
// for lone surrogates. Reject those before decoding rather than rewriting a
// translator's text; a real U+FFFD and valid supplementary pairs remain valid.
func unicodeEscapes(data []byte) error {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != 92 {
			continue
		}
		i++
		if i >= len(data) {
			return fmt.Errorf("malformed JSON escape")
		}
		if data[i] != 'u' {
			continue
		}
		unit, ok := hexUnit(data, i+1)
		if !ok {
			return fmt.Errorf("malformed Unicode escape")
		}
		i += 4
		if unit >= 0xDC00 && unit <= 0xDFFF {
			return fmt.Errorf("unpaired Unicode surrogate")
		}
		if unit >= 0xD800 && unit <= 0xDBFF {
			if i+6 >= len(data) || data[i+1] != 92 || data[i+2] != 'u' {
				return fmt.Errorf("unpaired Unicode surrogate")
			}
			low, ok := hexUnit(data, i+3)
			if !ok || low < 0xDC00 || low > 0xDFFF {
				return fmt.Errorf("unpaired Unicode surrogate")
			}
			i += 6
		}
	}
	return nil
}
func hexUnit(data []byte, at int) (uint16, bool) {
	if at+4 > len(data) {
		return 0, false
	}
	var result uint16
	for _, ch := range data[at : at+4] {
		result <<= 4
		switch {
		case ch >= '0' && ch <= '9':
			result += uint16(ch - '0')
		case ch >= 'a' && ch <= 'f':
			result += uint16(ch - 'a' + 10)
		case ch >= 'A' && ch <= 'F':
			result += uint16(ch - 'A' + 10)
		default:
			return 0, false
		}
	}
	return result, true
}
