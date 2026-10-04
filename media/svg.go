// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"regexp"
	"unicode/utf8"
)

const (
	maxSVGBytes      = 1 << 20
	maxSVGDepth      = 128
	maxSVGElements   = 100000
	maxSVGAttributes = 128
)

// encoding/xml does not validate the whole XML declaration grammar. Accept
// only a leading XML 1.0 declaration, with UTF-8 and ordinary standalone values.
var svgXMLDeclaration = regexp.MustCompile(`^version[ \t\r\n]*=[ \t\r\n]*(?:"1\.0"|'1\.0')(?:[ \t\r\n]+encoding[ \t\r\n]*=[ \t\r\n]*(?:"[Uu][Tt][Ff]-8"|'[Uu][Tt][Ff]-8'))?(?:[ \t\r\n]+standalone[ \t\r\n]*=[ \t\r\n]*(?:"(?:yes|no)"|'(?:yes|no)'))?[ \t\r\n]*$`)

// validateSVG admits a bounded complete XML document for private original-byte
// storage. It neither renders nor sanitizes SVG, interprets dimensions, fetches
// resources nor attests that the original is safe to open. Scripts and resource
// references inside the document remain opaque. No entity/charset resolver is
// installed; standard predefined/numeric XML character references still work.
func (m *Module) validateSVG(ctx context.Context, f *os.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxSVGBytes {
		return ErrInput
	}
	// SVG parsing shares the existing two nonqueued expensive-validation slots
	// with PNG/JPEG decoding. Hold admission through the bounded read and parse.
	select {
	case m.decoders <- struct{}{}:
		defer func() { <-m.decoders }()
	default:
		return ErrLimited
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, maxSVGBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxSVGBytes {
		return ErrInput
	}
	if err := validateXMLCharacters(ctx, body); err != nil {
		return err
	}
	// XML permits a UTF-8 byte-order mark. It is retained in stored bytes.
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	decoder := xml.NewDecoder(bytes.NewReader(body))
	depth, elements := 0, 0
	rootSeen := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		offset := decoder.InputOffset()
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if !rootSeen || depth != 0 {
				return ErrInput
			}
			return ctx.Err()
		}
		if err != nil {
			return ErrInput
		}
		switch token := token.(type) {
		case xml.StartElement:
			if err := validateSVGAttributeSeparators(ctx, body[offset:decoder.InputOffset()]); err != nil {
				return err
			}
			if depth == 0 {
				if rootSeen || token.Name != (xml.Name{Space: "http://www.w3.org/2000/svg", Local: "svg"}) {
					return ErrInput
				}
				rootSeen = true
			}
			depth++
			elements++
			if depth > maxSVGDepth || elements > maxSVGElements || len(token.Attr) > maxSVGAttributes {
				return ErrInput
			}
			// The standard tokenizer allows duplicate attributes; reject them as
			// malformed XML. This bounded scan creates no unbounded name registry.
			for i, attribute := range token.Attr {
				for _, earlier := range token.Attr[:i] {
					if attribute.Name == earlier.Name {
						return ErrInput
					}
				}
			}
		case xml.EndElement:
			depth-- // Token checks matching names and balanced elements.
		case xml.CharData:
			if depth == 0 && !xmlWhitespace(body[offset:decoder.InputOffset()]) {
				// Only literal XML whitespace is legal outside the root, not
				// CDATA or an entity reference that happens to decode to space.
				return ErrInput
			}
		case xml.ProcInst:
			if offset != 0 || token.Target != "xml" || !svgXMLDeclaration.Match(token.Inst) {
				return ErrInput
			}
		case xml.Directive:
			return ErrInput
		}
	}
}

func xmlWhitespace(body []byte) bool {
	for _, b := range body {
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return false
		}
	}
	return true
}

// encoding/xml checks text/attribute characters but does not check comments.
// Validate UTF-8 and XML 1.0 Char ranges across the entire bounded document.
func validateXMLCharacters(ctx context.Context, body []byte) error {
	nextCheck := 0
	for offset := 0; offset < len(body); {
		if offset >= nextCheck {
			if err := ctx.Err(); err != nil {
				return err
			}
			nextCheck = offset + recognitionBytes
		}
		r, size := utf8.DecodeRune(body[offset:])
		if r == utf8.RuneError && size == 1 || !(r == 0x9 || r == 0xa || r == 0xd ||
			0x20 <= r && r <= 0xd7ff || 0xe000 <= r && r <= 0xfffd || 0x10000 <= r && r <= 0x10ffff) {
			return ErrInput
		}
		offset += size
	}
	return ctx.Err()
}

// Token has already parsed this start tag. Check only the lexical whitespace
// between attributes that encoding/xml's optional space() loop does not enforce.
// Literal opposite quotes and entities inside a quoted value are not separators.
func validateSVGAttributeSeparators(ctx context.Context, tag []byte) error {
	var quote byte
	for i, b := range tag {
		if i%recognitionBytes == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if quote == 0 {
			if b == '\'' || b == '"' {
				quote = b
			}
		} else if b == quote {
			quote = 0
			if i+1 >= len(tag) || tag[i+1] != '/' && tag[i+1] != '>' && !xmlWhitespace(tag[i+1:i+2]) {
				return ErrInput
			}
		}
	}
	return ctx.Err()
}
