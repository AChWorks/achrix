// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gabriel-vasile/mimetype"
)

// Format describes upload admission, not complete validity or safe-to-open
// attestation. Extensions are lower-case aliases without a leading dot. PNG and
// JPEG retain their existing byte-selected, extension-independent full decoding;
// the other supported files require a listed extension and recognized type.
type Format struct {
	MIME       string
	Extensions []string
}

type formatSpec struct {
	format   Format
	detected []string
}

// The closed catalog is deliberately data rather than a registration framework.
// Only explicit type aliases are accepted; detector parent/ancestry matching
// cannot promote a generic ZIP or OLE container into a claimed Office document.
var supportedFormats = []formatSpec{
	{Format{"image/png", []string{"png"}}, nil},
	{Format{"image/jpeg", []string{"jpg", "jpeg", "jpe"}}, nil},
	{Format{"image/gif", []string{"gif"}}, nil},
	{Format{"image/webp", []string{"webp"}}, nil},
	{Format{"image/avif", []string{"avif"}}, nil},
	{Format{"image/bmp", []string{"bmp"}}, []string{"image/x-bmp", "image/x-ms-bmp"}},
	{Format{"image/tiff", []string{"tif", "tiff"}}, nil},
	{Format{"image/x-icon", []string{"ico"}}, nil},
	{Format{"application/pdf", []string{"pdf"}}, []string{"application/x-pdf"}},
	{Format{"application/msword", []string{"doc"}}, []string{"application/vnd.ms-word"}},
	{Format{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", []string{"docx"}}, nil},
	{Format{"application/vnd.ms-excel", []string{"xls"}}, []string{"application/msexcel"}},
	{Format{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", []string{"xlsx"}}, nil},
	{Format{"application/vnd.ms-powerpoint", []string{"ppt"}}, []string{"application/mspowerpoint"}},
	{Format{"application/vnd.openxmlformats-officedocument.presentationml.presentation", []string{"pptx"}}, nil},
	{Format{"application/vnd.oasis.opendocument.text", []string{"odt"}}, []string{"application/x-vnd.oasis.opendocument.text"}},
	{Format{"application/vnd.oasis.opendocument.spreadsheet", []string{"ods"}}, []string{"application/x-vnd.oasis.opendocument.spreadsheet"}},
	{Format{"application/vnd.oasis.opendocument.presentation", []string{"odp"}}, []string{"application/x-vnd.oasis.opendocument.presentation"}},
	{Format{"text/plain", []string{"txt"}}, []string{"text/csv"}},
	{Format{"text/csv", []string{"csv"}}, []string{"text/plain"}},
	{Format{"application/zip", []string{"zip"}}, []string{"application/x-zip", "application/x-zip-compressed"}},
	{Format{"application/vnd.rar", []string{"rar"}}, []string{"application/x-rar-compressed", "application/x-rar"}},
	{Format{"application/x-7z-compressed", []string{"7z"}}, nil},
	{Format{"video/mp4", []string{"mp4", "m4v"}}, []string{"video/x-m4v"}},
	{Format{"video/quicktime", []string{"mov"}}, nil},
	{Format{"video/webm", []string{"webm"}}, nil},
	{Format{"video/matroska", []string{"mkv"}}, []string{"video/x-matroska"}},
	{Format{"video/x-msvideo", []string{"avi"}}, []string{"video/avi", "video/msvideo"}},
	{Format{"video/mpeg", []string{"mpeg", "mpg"}}, nil},
	{Format{"video/ogg", []string{"ogv"}}, nil},
	{Format{"audio/mpeg", []string{"mp3"}}, []string{"audio/x-mpeg", "audio/mp3"}},
	{Format{"audio/mp4", []string{"m4a"}}, []string{"audio/x-m4a", "audio/x-mp4a"}},
	{Format{"audio/ogg", []string{"ogg", "oga"}}, nil},
	{Format{"audio/wav", []string{"wav"}}, []string{"audio/x-wav", "audio/vnd.wave", "audio/wave"}},
	{Format{"audio/flac", []string{"flac"}}, nil},
	{Format{"audio/aac", []string{"aac"}}, nil},
}

// SupportedFormats returns a fresh deep copy of the finite owning catalog.
// It is independent of the current upload selection: a product restricting new
// uploads must not make previously retained supported attachments unreadable.
func SupportedFormats() []Format {
	result := make([]Format, 0, len(supportedFormats))
	for _, spec := range supportedFormats {
		result = append(result, Format{spec.format.MIME, slices.Clone(spec.format.Extensions)})
	}
	return result
}

// CommonMIMEs returns a fresh selection of every supported common upload type.
func CommonMIMEs() []string {
	result := make([]string, 0, len(supportedFormats))
	for _, spec := range supportedFormats {
		result = append(result, spec.format.MIME)
	}
	return result
}

// Formats returns a fresh deep copy of this Service's effective upload formats.
func (s *Service) Formats() []Format {
	result := make([]Format, 0, len(supportedFormats))
	for _, spec := range supportedFormats {
		if s.module.allows(spec.format.MIME) {
			result = append(result, Format{spec.format.MIME, slices.Clone(spec.format.Extensions)})
		}
	}
	return result
}

func selectFormats(selection []string) ([]string, error) {
	if selection == nil {
		return nil, nil
	}
	if len(selection) == 0 || len(selection) > len(supportedFormats) {
		return nil, ErrConfiguration
	}
	copy := slices.Clone(selection)
	for i, value := range copy {
		known := false
		for _, spec := range supportedFormats {
			if value == spec.format.MIME {
				known = true
				break
			}
		}
		if !known || slices.Contains(copy[:i], value) {
			return nil, ErrConfiguration
		}
	}
	return copy, nil
}
func (m *Module) allows(value string) bool {
	if m.config.AllowedMIMEs == nil {
		return value == "image/png" || value == "image/jpeg"
	}
	return slices.Contains(m.config.AllowedMIMEs, value)
}

const recognitionBytes = 4096

func (m *Module) validateUpload(ctx context.Context, f *os.File, filename string) (string, int, int, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, 0, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0, 0, err
	}
	var prefix [recognitionBytes]byte
	n, err := io.ReadFull(contextReader{ctx, f}, prefix[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", 0, 0, err
	}
	if n == 0 {
		return "", 0, 0, ErrInput
	}
	raw := prefix[:n]
	// Preserve existing explicit decoders, including APNG and filename-agnostic
	// PNG/JPEG admission. No dependency registry can change these decoders.
	imageType := ""
	if len(raw) >= 8 && string(raw[:8]) == "\x89PNG\r\n\x1a\n" {
		imageType = "image/png"
	} else if len(raw) >= 3 && raw[0] == 0xff && raw[1] == 0xd8 && raw[2] == 0xff {
		imageType = "image/jpeg"
	}
	if imageType != "" {
		if !m.allows(imageType) {
			return "", 0, 0, ErrInput
		}
		return m.validate(ctx, f)
	}
	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	detected, _, err := mime.ParseMediaType(mimetype.Detect(raw).String())
	if err != nil {
		return "", 0, 0, ErrInput
	}
	for _, spec := range supportedFormats {
		if !m.allows(spec.format.MIME) || !slices.Contains(spec.format.Extensions, extension) {
			continue
		}
		if detected != spec.format.MIME && !slices.Contains(spec.detected, detected) {
			return "", 0, 0, ErrInput
		}
		if spec.format.MIME == "text/plain" || spec.format.MIME == "text/csv" {
			if err := validateUTF8Text(ctx, f); err != nil {
				return "", 0, 0, err
			}
		}
		if err := ctx.Err(); err != nil {
			return "", 0, 0, err
		}
		return spec.format.MIME, 0, 0, nil
	}
	return "", 0, 0, ErrInput
}

// Text detection alone examines a prefix and can recognize legacy encodings.
// Check the entire already size-bounded file with a fixed buffer, including
// multibyte sequences crossing buffer boundaries, before publishing UTF-8 text.
func validateUTF8Text(ctx context.Context, f *os.File) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(contextReader{ctx, f}, 32<<10)
	for {
		r, size, err := reader.ReadRune()
		if errors.Is(err, io.EOF) {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if r == utf8.RuneError && size == 1 || unicode.IsControl(r) && r != '\t' && r != '\r' && r != '\n' {
			return ErrInput
		}
	}
}
