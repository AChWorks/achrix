// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"time"
	"unicode/utf8"
)

var canonicalChromaticities = [8]uint32{31270, 32900, 64000, 33000, 30000, 60000, 15000, 6000}

func (p *imageProfile) inspectPNG(ctx context.Context, source []byte) error {
	if len(source) < 33 || !bytes.Equal(source[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return ErrInput
	}
	seen := make(map[string]bool)
	palettes := 0
	metadata, chunks := 0, 0
	idat, idatEnded := false, false
	suggestedPalettes := make(map[string]bool)
	var exif []byte
	for offset := 8; offset < len(source); {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(source)-offset < 12 || chunks >= maxImageSegments {
			return ErrInput
		}
		chunks++
		length := uint64(binary.BigEndian.Uint32(source[offset:]))
		if length > uint64(len(source)-offset-12) {
			return ErrInput
		}
		end := offset + 12 + int(length)
		kind := string(source[offset+4 : offset+8])
		for _, c := range source[offset+4 : offset+8] {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
				return ErrInput
			}
		}
		if source[offset+6] >= 'a' {
			return ErrInput
		} // reserved bit
		data := source[offset+8 : end-4]
		if crc32.ChecksumIEEE(source[offset+4:end-4]) != binary.BigEndian.Uint32(source[end-4:end]) {
			return ErrInput
		}
		if chunks == 1 && kind != "IHDR" {
			return ErrInput
		}
		if kind != "IHDR" && !seen["IHDR"] {
			return ErrInput
		}
		repeat := kind == "IDAT" || kind == "tEXt" || kind == "zTXt" || kind == "iTXt" || kind == "sPLT"
		if seen[kind] && !repeat {
			return ErrInput
		}
		if idat && kind != "IDAT" {
			idatEnded = true
		}
		if kind != "IHDR" && kind != "PLTE" && kind != "IDAT" && kind != "IEND" {
			if len(data)+12 > maxImageMetadata-metadata {
				return ErrInput
			}
			metadata += len(data) + 12
		}
		switch kind {
		case "IHDR":
			if chunks != 1 || len(data) != 13 {
				return ErrInput
			}
			p.width, p.height = int(binary.BigEndian.Uint32(data[:4])), int(binary.BigEndian.Uint32(data[4:8]))
			p.pngDepth, p.pngColor = data[8], data[9]
			if !publicDimensions(p.width, p.height) || data[10] != 0 || data[11] != 0 || data[12] > 1 {
				return ErrInput
			}
			switch p.pngColor {
			case 0, 3:
				if p.pngDepth != 1 && p.pngDepth != 2 && p.pngDepth != 4 && p.pngDepth != 8 {
					return ErrInput
				}
			case 2, 4, 6:
				if p.pngDepth != 8 {
					return ErrInput
				}
			default:
				return ErrInput
			}
			p.jpeg444 = true
			p.jpegModel = "rgb"
			if p.pngColor == 0 || p.pngColor == 4 {
				p.jpegModel = "gray"
			}
		case "PLTE":
			if idat || seen["tRNS"] || seen["bKGD"] || seen["hIST"] || p.pngColor == 0 || p.pngColor == 4 || len(data) == 0 || len(data)%3 != 0 || len(data) > 768 {
				return ErrInput
			}
			palettes = len(data) / 3
			if p.pngColor == 3 && palettes > 1<<p.pngDepth {
				return ErrInput
			}
		case "tRNS":
			if idat {
				return ErrInput
			}
			switch p.pngColor {
			case 0:
				if len(data) != 2 || binary.BigEndian.Uint16(data) >= 1<<p.pngDepth {
					return ErrInput
				}
			case 2:
				if len(data) != 6 {
					return ErrInput
				}
				for i := 0; i < 6; i += 2 {
					if binary.BigEndian.Uint16(data[i:]) > 255 {
						return ErrInput
					}
				}
			case 3:
				if palettes == 0 || len(data) == 0 || len(data) > palettes {
					return ErrInput
				}
			default:
				return ErrInput
			}
		case "IDAT":
			if idatEnded || p.pngColor == 3 && palettes == 0 {
				return ErrInput
			}
			idat = true
		case "IEND":
			if !idat || len(data) != 0 || end != len(source) {
				return ErrInput
			}
			if len(exif) != 0 {
				if err := p.inspectExif(ctx, exif); err != nil {
					return err
				}
			}
			return nil
		case "sRGB":
			if idat || seen["PLTE"] || len(data) != 1 || data[0] > 3 {
				return ErrInput
			}
			p.srgb, p.srgbIntent = true, data[0]
		case "gAMA":
			if idat || seen["PLTE"] || len(data) != 4 || binary.BigEndian.Uint32(data) != 45455 {
				return ErrInput
			}
			p.gamma = true
		case "cHRM":
			if idat || seen["PLTE"] || len(data) != 32 {
				return ErrInput
			}
			for i, v := range canonicalChromaticities {
				if binary.BigEndian.Uint32(data[4*i:]) != v {
					return ErrInput
				}
			}
			p.chromaticity = true
		case "pHYs":
			if idat || len(data) != 9 || data[8] > 1 || binary.BigEndian.Uint32(data[:4]) == 0 || binary.BigEndian.Uint32(data[:4]) != binary.BigEndian.Uint32(data[4:8]) {
				return ErrInput
			}
		case "sBIT":
			if idat || seen["PLTE"] {
				return ErrInput
			}
			count := map[byte]int{0: 1, 2: 3, 3: 3, 4: 2, 6: 4}[p.pngColor]
			if len(data) != count {
				return ErrInput
			}
			upper := p.pngDepth
			if p.pngColor == 3 {
				upper = 8
			}
			for _, v := range data {
				if v == 0 || v > upper {
					return ErrInput
				}
			}
		case "bKGD":
			if idat {
				return ErrInput
			}
			switch p.pngColor {
			case 3:
				if palettes == 0 || len(data) != 1 || int(data[0]) >= palettes {
					return ErrInput
				}
			case 0, 4:
				if len(data) != 2 || binary.BigEndian.Uint16(data) >= 1<<p.pngDepth {
					return ErrInput
				}
			case 2, 6:
				if len(data) != 6 {
					return ErrInput
				}
				for i := 0; i < 6; i += 2 {
					if binary.BigEndian.Uint16(data[i:]) > 255 {
						return ErrInput
					}
				}
			}
		case "hIST":
			if idat || palettes == 0 || len(data) != 2*palettes {
				return ErrInput
			}
		case "sPLT":
			if idat {
				return ErrInput
			}
			name, rest, ok := pngKeyword(data)
			if !ok || suggestedPalettes[name] || len(rest) < 1 {
				return ErrInput
			}
			suggestedPalettes[name] = true
			size := 6
			if rest[0] == 16 {
				size = 10
			} else if rest[0] != 8 {
				return ErrInput
			}
			if len(rest) == 1 || (len(rest)-1)%size != 0 {
				return ErrInput
			}
		case "tIME":
			if len(data) != 7 {
				return ErrInput
			}
			year := int(binary.BigEndian.Uint16(data))
			month, day := int(data[2]), int(data[3])
			date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
			if year < 1 || date.Year() != year || int(date.Month()) != month || date.Day() != day || data[4] > 23 || data[5] > 59 || data[6] > 60 {
				return ErrInput
			}
		case "tEXt", "zTXt", "iTXt":
			if err := inspectPNGText(kind, data); err != nil {
				return err
			}
		case "eXIf":
			if len(data) == 0 {
				return ErrInput
			}
			exif = data
		default:
			return ErrInput
		}
		seen[kind] = true
		offset = end
	}
	return ErrInput
}
func pngKeyword(data []byte) (string, []byte, bool) {
	end := bytes.IndexByte(data, 0)
	if end < 1 || end > 79 || data[0] == ' ' || data[end-1] == ' ' {
		return "", nil, false
	}
	for i, v := range data[:end] {
		if v < 32 || v >= 127 && v <= 160 || v == ' ' && i > 0 && data[i-1] == ' ' {
			return "", nil, false
		}
	}
	return string(data[:end]), data[end+1:], true
}
func inspectPNGText(kind string, data []byte) error {
	keyword, rest, ok := pngKeyword(data)
	if !ok {
		return ErrInput
	}
	switch strings.ToLower(keyword) {
	case "xml:com.adobe.xmp", "raw profile type exif", "raw profile type xmp", "exif":
		return ErrInput
	}
	switch kind {
	case "tEXt":
		if bytes.IndexByte(rest, 0) >= 0 {
			return ErrInput
		}
	case "zTXt":
		if len(rest) < 3 || rest[0] != 0 || !zlibHeader(rest[1:]) {
			return ErrInput
		}
	case "iTXt":
		if len(rest) < 4 || rest[0] > 1 || rest[1] != 0 {
			return ErrInput
		}
		compressed := rest[0] == 1
		rest = rest[2:]
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return ErrInput
		}
		for _, c := range rest[:end] {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return ErrInput
			}
		}
		rest = rest[end+1:]
		end = bytes.IndexByte(rest, 0)
		if end < 0 || !utf8.Valid(rest[:end]) {
			return ErrInput
		}
		rest = rest[end+1:]
		if compressed {
			if !zlibHeader(rest) {
				return ErrInput
			}
		} else if !utf8.Valid(rest) || bytes.IndexByte(rest, 0) >= 0 {
			return ErrInput
		}
	}
	return nil
}
func zlibHeader(data []byte) bool {
	return len(data) >= 2 && data[0]&15 == 8 && data[0]>>4 <= 7 && data[1]&32 == 0 && (uint16(data[0])*256+uint16(data[1]))%31 == 0
}
func pngChunk(kind string, data []byte) []byte {
	result := make([]byte, len(data)+12)
	binary.BigEndian.PutUint32(result, uint32(len(data)))
	copy(result[4:8], kind)
	copy(result[8:], data)
	binary.BigEndian.PutUint32(result[len(result)-4:], crc32.ChecksumIEEE(result[4:len(result)-4]))
	return result
}
func (p imageProfile) pngOutputDeclarations() []byte {
	var result []byte
	if p.srgb || p.exifSRGB && !p.gamma && !p.chromaticity {
		intent := p.srgbIntent
		if !p.srgb {
			intent = 0
		}
		result = append(result, pngChunk("sRGB", []byte{intent})...)
	}
	if p.gamma {
		data := make([]byte, 4)
		binary.BigEndian.PutUint32(data, 45455)
		result = append(result, pngChunk("gAMA", data)...)
	}
	if p.chromaticity {
		data := make([]byte, 32)
		for i, v := range canonicalChromaticities {
			binary.BigEndian.PutUint32(data[4*i:], v)
		}
		result = append(result, pngChunk("cHRM", data)...)
	}
	return result
}
