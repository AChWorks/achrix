// SPDX-License-Identifier: MPL-2.0
package localize

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/feature/plural"
)

type part struct{ literal, name string }
type pattern struct {
	parts []part
	names []string
}

func compile(forms Forms) (*compiledMessage, error) {
	if forms.Other == "" {
		return nil, fmt.Errorf("other message is required")
	}
	result := &compiledMessage{forms: forms, patterns: map[string]pattern{}}
	values := map[string]string{"zero": forms.Zero, "one": forms.One, "two": forms.Two, "few": forms.Few, "many": forms.Many, "other": forms.Other}
	for category, value := range values {
		if value == "" {
			continue
		}
		if len(value) > MaxMessageBytes {
			return nil, fmt.Errorf("%w: message bytes", ErrLimit)
		}
		parsed, err := parsePattern(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", category, err)
		}
		result.patterns[category] = parsed
	}
	result.names = result.patterns["other"].names
	for category, parsed := range result.patterns {
		if !sameNames(result.names, parsed.names) {
			return nil, fmt.Errorf("%s placeholders differ from other", category)
		}
	}
	return result, nil
}

func parsePattern(value string) (pattern, error) {
	result := pattern{}
	var literal strings.Builder
	names := map[string]bool{}
	flush := func() {
		if literal.Len() > 0 {
			result.parts = append(result.parts, part{literal: literal.String()})
			literal.Reset()
		}
	}
	for i := 0; i < len(value); {
		switch value[i] {
		case '{':
			if i+1 < len(value) && value[i+1] == '{' {
				literal.WriteByte('{')
				i += 2
				continue
			}
			end := strings.IndexByte(value[i+1:], '}')
			if end < 0 {
				return result, fmt.Errorf("unclosed parameter")
			}
			end += i + 1
			name := value[i+1 : end]
			if !parameterName(name) {
				return result, fmt.Errorf("invalid parameter name")
			}
			flush()
			result.parts = append(result.parts, part{name: name})
			names[name] = true
			i = end + 1
		case '}':
			if i+1 >= len(value) || value[i+1] != '}' {
				return result, fmt.Errorf("unescaped closing brace")
			}
			literal.WriteByte('}')
			i += 2
		default:
			literal.WriteByte(value[i])
			i++
		}
	}
	flush()
	if len(names) > MaxParameters {
		return result, fmt.Errorf("%w: parameter count", ErrLimit)
	}
	result.names = make([]string, 0, len(names))
	for name := range names {
		result.names = append(result.names, name)
	}
	sort.Strings(result.names)
	return result, nil
}
func parameterName(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		letter := ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
		if !letter && (i == 0 || !(ch >= '0' && ch <= '9' || ch == '_')) {
			return false
		}
	}
	return true
}
func sameNames(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i, name := range left {
		if name != right[i] {
			return false
		}
	}
	return true
}

// Localizer is an immutable per-selection view. It does not store preferences or
// alter the original content. Returned text remains untrusted display data and
// must use the output context's normal escaping, including html/template or DOM
// textContent. Never cast it to trusted HTML or execute it as a template/script.
type Localizer struct {
	catalog *Catalog
	locale  Locale
}

func (l *Localizer) Locale() Locale {
	if l == nil {
		return Locale{}
	}
	return l.locale
}

func (l *Localizer) lookup(domain, key string) (*compiledMessage, string, error) {
	if l == nil || l.catalog == nil {
		return nil, "", ErrLocale
	}
	if !identifier(domain, 64) || !identifier(key, 128) {
		return nil, "", ErrMessage
	}
	locales := l.catalog.messages[domain]
	if locales != nil {
		if message := locales[l.locale.Tag][key]; message != nil {
			return message, l.locale.Tag, nil
		}
		if message := locales["en"][key]; message != nil {
			return message, "en", nil
		}
	}
	return nil, "", fmt.Errorf("%w: %s/%s", ErrMessage, domain, key)
}

// Text formats the complete Other message. Missing translations fall back as a
// whole message to English; unknown keys or missing parameters return errors.
// Valid extra parameters are accepted to allow a shared caller-owned parameter
// set. Values are strings, not executable templates or locale-formatting policy.
func (l *Localizer) Text(domain, key string, params map[string]string) (string, error) {
	message, _, err := l.lookup(domain, key)
	if err != nil {
		return "", err
	}
	value, err := render(message.patterns["other"], params, nil)
	if err != nil {
		return "", fmt.Errorf("%s/%s: %w", domain, key, err)
	}
	return value, nil
}

// Plural selects a cardinal form for an integral count using maintained CLDR
// rules. It injects the decimal Count parameter without changing calendar,
// number/currency formatting or stored values. Params must not supply Count.
func (l *Localizer) Plural(domain, key string, count int64, params map[string]string) (string, error) {
	message, source, err := l.lookup(domain, key)
	if err != nil {
		return "", err
	}
	if _, exists := params["Count"]; exists {
		return "", fmt.Errorf("%w: Count is supplied by Plural", ErrParameters)
	}
	decimal := strconv.FormatInt(count, 10)
	magnitude := strings.TrimPrefix(decimal, "-")
	var storage [20]byte
	digits := storage[:len(magnitude)]
	for i, ch := range []byte(magnitude) {
		digits[i] = ch - '0'
	}
	category := plural.Cardinal.MatchDigits(l.catalog.tags[source], digits, len(digits), 0)
	name := "other"
	switch category {
	case plural.Zero:
		name = "zero"
	case plural.One:
		name = "one"
	case plural.Two:
		name = "two"
	case plural.Few:
		name = "few"
	case plural.Many:
		name = "many"
	}
	selected, ok := message.patterns[name]
	if !ok {
		selected = message.patterns["other"]
	}
	value, err := render(selected, params, &decimal)
	if err != nil {
		return "", fmt.Errorf("%s/%s: %w", domain, key, err)
	}
	return value, nil
}

func render(selected pattern, params map[string]string, count *string) (string, error) {
	if len(params) > MaxParameters {
		return "", fmt.Errorf("%w: parameter count", ErrParameters)
	}
	for name, value := range params {
		if !parameterName(name) || len(value) > MaxParameterBytes || !utf8.ValidString(value) {
			return "", ErrParameters
		}
	}
	if len(selected.parts) == 1 && selected.parts[0].name == "" {
		return selected.parts[0].literal, nil
	}
	valueFor := func(part part) (string, bool) {
		if part.name == "" {
			return part.literal, true
		}
		if part.name == "Count" && count != nil {
			return *count, true
		}
		value, exists := params[part.name]
		return value, exists
	}
	size := 0
	for _, part := range selected.parts {
		value, exists := valueFor(part)
		if !exists {
			return "", fmt.Errorf("%w: missing %s", ErrParameters, part.name)
		}
		size += len(value)
		if size > MaxOutputBytes {
			return "", fmt.Errorf("%w: expanded message bytes", ErrLimit)
		}
	}
	var result strings.Builder
	result.Grow(size)
	for _, part := range selected.parts {
		value, _ := valueFor(part)
		result.WriteString(value)
	}

	return result.String(), nil
}

// Messages returns a defensive, English-fallback-completed browser message
// table for one domain. It contains data-only patterns and the source locale of
// each complete message. Client plural selection must use Message.Locale, not
// necessarily the document locale; absent categories use Forms.Other. Serialize
// with normal encoding/json escaping and consume values through safe DOM text.
func (l *Localizer) Messages(domain string) (map[string]Message, error) {
	if l == nil || l.catalog == nil {
		return nil, ErrLocale
	}
	if !identifier(domain, 64) {
		return nil, ErrMessage
	}
	locales := l.catalog.messages[domain]
	if locales == nil {
		return nil, ErrMessage
	}
	result := make(map[string]Message, len(locales["en"]))
	for key := range locales["en"] {
		message, source, err := l.lookup(domain, key)
		if err != nil {
			return nil, err
		}
		result[key] = Message{source, message.forms}
	}
	return result, nil
}
