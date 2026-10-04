// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"sort"
	"testing"
)

// These complete bounded synthetic codestreams are authored here; no camera,
// personal metadata, third-party generator or production encoder is required.
func publicPNG(t testing.TB, pixels image.Image, chunks ...[]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, pixels); err != nil {
		t.Fatal(err)
	}
	return insertPNG(b.Bytes(), chunks...)
}
func insertPNG(source []byte, chunks ...[]byte) []byte {
	result := append([]byte(nil), source[:33]...)
	for _, chunk := range chunks {
		result = append(result, chunk...)
	}
	return append(result, source[33:]...)
}
func pngKinds(source []byte) []string {
	var result []string
	for offset := 8; offset+12 <= len(source); {
		n := int(binary.BigEndian.Uint32(source[offset:]))
		result = append(result, string(source[offset+4:offset+8]))
		offset += n + 12
	}
	return result
}

type testExifEntry struct {
	tag, kind uint16
	count     uint32
	data      []byte
}
type testExifDirectory struct {
	entries []testExifEntry
	next    int
}

func exifShort(tag uint16, value uint16, order binary.ByteOrder) testExifEntry {
	b := make([]byte, 2)
	order.PutUint16(b, value)
	return testExifEntry{tag, 3, 1, b}
}
func exifLong(tag uint16, value uint32, order binary.ByteOrder) testExifEntry {
	b := make([]byte, 4)
	order.PutUint32(b, value)
	return testExifEntry{tag, 4, 1, b}
}
func syntheticExif(order binary.ByteOrder, dirs ...testExifDirectory) []byte {
	size := 8
	offsets := make([]int, len(dirs))
	for i, d := range dirs {
		offsets[i] = size
		size += 2 + 12*len(d.entries) + 4
	}
	result := make([]byte, size)
	if order == binary.LittleEndian {
		copy(result, "II")
	} else {
		copy(result, "MM")
	}
	order.PutUint16(result[2:], 42)
	order.PutUint32(result[4:], 8)
	for i, d := range dirs {
		entries := append([]testExifEntry(nil), d.entries...)
		sort.Slice(entries, func(i, j int) bool { return entries[i].tag < entries[j].tag })
		offset := offsets[i]
		order.PutUint16(result[offset:], uint16(len(entries)))
		for j, e := range entries {
			at := offset + 2 + 12*j
			order.PutUint16(result[at:], e.tag)
			order.PutUint16(result[at+2:], e.kind)
			order.PutUint32(result[at+4:], e.count)
			value := e.data
			if e.tag == 0x8769 || e.tag == 0x8825 || e.tag == 0xa005 {
				index := int(order.Uint32(value))
				value = make([]byte, 4)
				order.PutUint32(value, uint32(offsets[index]))
			}
			if len(value) <= 4 {
				copy(result[at+8:at+12], value)
			} else {
				order.PutUint32(result[at+8:], uint32(len(result)))
				result = append(result, value...)
			}
		}
		if d.next >= 0 {
			order.PutUint32(result[offset+2+12*len(entries):], uint32(offsets[d.next]))
		}
	}
	return result
}
func orientationExif(orientation int, srgb bool) []byte {
	order := binary.LittleEndian
	first := []testExifEntry{exifShort(0x0112, uint16(orientation), order), {0x010e, 2, 15, []byte("private-camera\x00")}}
	dirs := []testExifDirectory{{entries: first, next: -1}}
	if srgb {
		dirs[0].entries = append(first, exifLong(0x8769, 1, order))
		dirs = append(dirs, testExifDirectory{entries: []testExifEntry{exifShort(0xa001, 1, order)}, next: -1})
	}
	return syntheticExif(order, dirs...)
}
func preparedBytes(t testing.TB, source []byte, mime string) (imageProfile, []byte) {
	t.Helper()
	p, err := inspectPublicImage(context.Background(), source, mime)
	if err != nil {
		t.Fatal("profile", err)
	}
	var b bytes.Buffer
	w, h, n, digest, err := encodePublicImage(context.Background(), source, p, &b)
	if err != nil {
		t.Fatal("encode", err)
	}
	sum := sha256.Sum256(b.Bytes())
	if n != int64(b.Len()) || digest != hex.EncodeToString(sum[:]) || w < 1 || h < 1 {
		t.Fatal("output binding")
	}
	return p, b.Bytes()
}
func TestPublicImageOrientationsAlphaAndMetadata(t *testing.T) {
	pixels := image.NewNRGBA(image.Rect(0, 0, 2, 3))
	copy(pixels.Pix, []byte{9, 8, 7, 0, 21, 22, 23, 1, 31, 32, 33, 64, 41, 42, 43, 128, 51, 52, 53, 254, 61, 62, 63, 255})
	for orientation := 1; orientation <= 8; orientation++ {
		t.Run(fmt.Sprint(orientation), func(t *testing.T) {
			source := publicPNG(t, pixels, pngChunk("eXIf", orientationExif(orientation, true)), pngChunk("tEXt", []byte("Author\x00private-author")))
			before := sha256.Sum256(source)
			p, out := preparedBytes(t, source, "image/png")
			decoded, err := png.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			width, height := 2, 3
			if orientation >= 5 {
				width, height = height, width
			}
			if decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height || p.colorBasis() != "declared-srgb" {
				t.Fatal("orientation/color")
			}
			for y := 0; y < 3; y++ {
				for x := 0; x < 2; x++ {
					dx, dy := orientedPixel(x, y, 2, 3, orientation)
					want := pixels.NRGBAAt(x, y)
					if want.A == 0 {
						want.R, want.G, want.B = 0, 0, 0
					}
					got := color.NRGBAModel.Convert(decoded.At(dx, dy)).(color.NRGBA)
					if got != want {
						t.Fatalf("%d,%d got%v want%v", dx, dy, got, want)
					}
				}
			}
			for _, kind := range pngKinds(out) {
				if kind == "eXIf" || kind == "tEXt" {
					t.Fatal("private metadata retained")
				}
			}
			if sha256.Sum256(source) != before {
				t.Fatal("source changed")
			}
		})
	}
}
func TestPublicImageColorDeclarationPrecedence(t *testing.T) {
	pixels := image.NewGray(image.Rect(0, 0, 2, 2))
	gamma := make([]byte, 4)
	binary.BigEndian.PutUint32(gamma, 45455)
	chroma := make([]byte, 32)
	for i, v := range canonicalChromaticities {
		binary.BigEndian.PutUint32(chroma[4*i:], v)
	}
	cases := []struct {
		name, basis                     string
		chunks                          [][]byte
		wantSRGB, wantGamma, wantChroma bool
	}{
		{"untagged", "assumed-untagged", nil, false, false, false},
		{"srgb", "declared-srgb", [][]byte{pngChunk("sRGB", []byte{3})}, true, false, false},
		{"gamma", "legacy-canonical-png", [][]byte{pngChunk("gAMA", gamma)}, false, true, false},
		{"chroma", "legacy-canonical-png", [][]byte{pngChunk("cHRM", chroma)}, false, false, true},
		{"legacy-and-exif", "legacy-canonical-png", [][]byte{pngChunk("gAMA", gamma), pngChunk("cHRM", chroma), pngChunk("eXIf", orientationExif(1, true))}, false, true, true},
		{"exif-only", "declared-srgb", [][]byte{pngChunk("eXIf", orientationExif(1, true))}, true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, out := preparedBytes(t, publicPNG(t, pixels, c.chunks...), "image/png")
			if p.colorBasis() != c.basis {
				t.Fatal(p.colorBasis())
			}
			fresh, err := inspectPublicImage(context.Background(), out, "image/png")
			if err != nil || fresh.srgb != c.wantSRGB || fresh.gamma != c.wantGamma || fresh.chromaticity != c.wantChroma {
				t.Fatal("fresh declarations", fresh, err)
			}
		})
	}
}
func TestPublicImageRejectsUnsupportedPNGInterpretationAndFraming(t *testing.T) {
	base := imageBytes(t, "png", 2, 2)
	badCRC := append([]byte(nil), base...)
	badCRC[29] ^= 1
	gamma := make([]byte, 4)
	binary.BigEndian.PutUint32(gamma, 100000)
	phys := make([]byte, 9)
	binary.BigEndian.PutUint32(phys, 1)
	binary.BigEndian.PutUint32(phys[4:], 2)
	cases := map[string][]byte{"crc": badCRC, "trailer": append(append([]byte(nil), base...), 0), "truncated": base[:len(base)-12], "gamma": insertPNG(base, pngChunk("gAMA", gamma)), "aspect": insertPNG(base, pngChunk("pHYs", phys)), "duplicate": insertPNG(base, pngChunk("sRGB", []byte{0}), pngChunk("sRGB", []byte{0})), "bad-exif": insertPNG(base, pngChunk("eXIf", []byte("Exif\x00\x00"))), "xmp": insertPNG(base, pngChunk("tEXt", []byte("XML:com.adobe.xmp\x00private")))}
	for _, kind := range []string{"iCCP", "cICP", "mDCV", "cLLI", "acTL", "fcTL", "fdAT", "zzZZ"} {
		cases[kind] = insertPNG(base, pngChunk(kind, nil))
	}
	cases["metadata-bound"] = insertPNG(base, pngChunk("tEXt", append([]byte("Comment\x00"), bytes.Repeat([]byte{'a'}, maxImageMetadata)...)))
	chunks := make([][]byte, maxImageSegments)
	for i := range chunks {
		chunks[i] = pngChunk("tEXt", []byte("K\x00"))
	}
	cases["chunk-bound"] = insertPNG(base, chunks...)
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := inspectPublicImage(context.Background(), source, "image/png"); !errors.Is(err, ErrInput) {
				t.Fatal("accepted", err)
			}
		})
	}
	sixteen := image.NewNRGBA64(image.Rect(0, 0, 2, 2))
	if _, err := inspectPublicImage(context.Background(), publicPNG(t, sixteen), "image/png"); !errors.Is(err, ErrInput) {
		t.Fatal("16bit")
	}
	// A valid framing profile still must survive full decompression.
	corrupt := append([]byte(nil), base...)
	for off := 8; off < len(corrupt); {
		n := int(binary.BigEndian.Uint32(corrupt[off:]))
		if string(corrupt[off+4:off+8]) == "IDAT" {
			copy(corrupt[off:off+n+12], pngChunk("IDAT", make([]byte, n)))
			break
		}
		off += n + 12
	}
	p, err := inspectPublicImage(context.Background(), corrupt, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, _, _, _, err = encodePublicImage(context.Background(), corrupt, p, &out); !errors.Is(err, ErrInput) || out.Len() != 0 {
		t.Fatal("full decode", err)
	}
}

// Minimal complete progressive Huffman JPEG. Every DC difference and AC EOB is
// zero; the decoder nevertheless allocates every admitted coefficient plane.
func progressiveJPEG(width, height int, model string, sampling ...[2]int) []byte {
	h, v := 1, 1
	if len(sampling) != 0 {
		h, v = sampling[0][0], sampling[0][1]
	}
	components := 3
	ids := []byte{1, 2, 3}
	if model == "gray" {
		components = 1
		ids = []byte{1}
	}
	if model == "rgb" {
		ids = []byte{'R', 'G', 'B'}
	}
	result := []byte{255, 216}
	quant := append([]byte{0}, bytes.Repeat([]byte{1}, 64)...)
	result = append(result, jpegSegment(219, quant)...)
	frame := []byte{8, byte(height >> 8), byte(height), byte(width >> 8), byte(width), byte(components)}
	for i, id := range ids {
		sample := byte(0x11)
		if i == 0 {
			sample = byte(h<<4 | v)
		}
		frame = append(frame, id, sample, 0)
	}
	result = append(result, jpegSegment(194, frame)...)
	dc := make([]byte, 18)
	dc[1] = 1
	ac := make([]byte, 18)
	ac[0] = 16
	ac[1] = 1
	result = append(result, jpegSegment(196, append(dc, ac...))...)
	blocks := ((width + 7) / 8) * ((height + 7) / 8)
	chromaBlocks := ((width + 8*h - 1) / (8 * h)) * ((height + 8*v - 1) / (8 * v))
	dcBlocks := blocks
	if components == 3 {
		dcBlocks = chromaBlocks * (h*v + 2)
	}
	entropy := func(bits int) []byte {
		body := make([]byte, (bits+7)/8)
		if bits%8 != 0 {
			body[len(body)-1] = byte((1 << (8 - bits%8)) - 1)
		}
		if len(body) > 0 && body[len(body)-1] == 255 {
			body = append(body, 0)
		}
		return body
	}
	scan := []byte{byte(components)}
	for _, id := range ids {
		scan = append(scan, id, 0)
	}
	scan = append(scan, 0, 0, 0)
	result = append(result, jpegSegment(218, scan)...)
	result = append(result, entropy(dcBlocks)...)
	for i, id := range ids {
		result = append(result, jpegSegment(218, []byte{1, id, 0, 1, 63, 0})...)
		n := blocks
		if i > 0 {
			n = chromaBlocks
		}
		result = append(result, entropy(n)...)
	}
	return append(result, 255, 217)
}
func TestPublicImageJPEGProfilesAndFreshMetadata(t *testing.T) {
	for _, model := range []string{"gray", "rgb", "ycbcr"} {
		t.Run(model, func(t *testing.T) {
			source := progressiveJPEG(16, 8, model)
			source = append(append(append([]byte(nil), source[:2]...), jpegSegment(225, append([]byte("Exif\x00\x00"), orientationExif(6, true)...))...), source[2:]...)
			p, out := preparedBytes(t, source, "image/jpeg")
			fresh, err := inspectPublicImage(context.Background(), out, "image/jpeg")
			if err != nil || fresh.width != 8 || fresh.height != 16 || fresh.orientation != 1 || fresh.colorBasis() != "declared-srgb" || p.jpegModel != model {
				t.Fatal("fresh", fresh, err)
			}
			decoded, err := jpeg.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			if model == "gray" {
				if _, ok := decoded.(*image.Gray); !ok {
					t.Fatal("gray lost")
				}
			}
			if bytes.Contains(out, []byte("private-camera")) {
				t.Fatal("private metadata")
			}
		})
	}
	preparedBytes(t, imageBytes(t, "jpeg", 16, 16), "image/jpeg")
}
func TestPublicImageRejectsJPEGInterpretationAndFraming(t *testing.T) {
	base := imageBytes(t, "jpeg", 16, 16)
	add := func(marker byte, data []byte) []byte {
		return append(append(append([]byte(nil), base[:2]...), jpegSegment(marker, data)...), base[2:]...)
	}
	cases := map[string][]byte{"trailer": append(append([]byte(nil), base...), 0), "missing-end": base[:len(base)-2], "xmp": add(225, []byte("http://ns.adobe.com/xap/1.0/\x00")), "unknown-app0": add(224, []byte("unknown")), "bad-exif": add(225, []byte("Exif\x00\x00invalid"))}
	for _, marker := range []byte{226, 235, 237, 239} {
		cases[fmt.Sprintf("app%d", marker)] = add(marker, []byte("private"))
	}
	adobe := []byte{'A', 'd', 'o', 'b', 'e', 0, 100, 0, 0, 0, 1, 1}
	cases["essential-adobe-flags"] = add(238, adobe)
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := inspectPublicImage(context.Background(), source, "image/jpeg"); !errors.Is(err, ErrInput) {
				t.Fatal("accepted", err)
			}
		})
	}
}
func TestPublicImageExifFiniteInterpretationAndBounds(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		exif := syntheticExif(order, testExifDirectory{entries: []testExifEntry{exifShort(0x0112, 8, order), exifLong(0x8769, 1, order)}, next: -1}, testExifDirectory{entries: []testExifEntry{exifShort(0xa001, 1, order), exifLong(0xa002, 2, order), exifLong(0xa003, 3, order), exifLong(0xa005, 2, order), {0x9000, 7, 4, []byte("0300")}}, next: -1}, testExifDirectory{entries: []testExifEntry{{1, 2, 4, []byte("R98\x00")}, {2, 7, 4, []byte("0100")}}, next: -1})
		p := imageProfile{mime: "image/jpeg", width: 2, height: 3, jpegModel: "ycbcr", jpeg444: true}
		if err := p.inspectExif(context.Background(), exif); err != nil || p.orientation != 8 || !p.exifSRGB {
			t.Fatal("finite exif", err)
		}
	}
	order := binary.LittleEndian
	for _, entry := range []testExifEntry{exifShort(0x0112, 9, order), exifShort(0xa001, 0xffff, order), {0xa500, 5, 1, make([]byte, 8)}, exifLong(0xa002, 99, order), {0x9000, 7, 4, []byte("0310")}, {0xa005, 4, 1, make([]byte, 4)}, exifShort(0xffff, 1, order)} {
		mode := "exif"
		if entry.tag == 0x0112 {
			mode = "ifd0"
		}
		p := imageProfile{mime: "image/jpeg", width: 2, height: 3, jpegModel: "ycbcr"}
		e := exifInspector{profile: &p, order: order}
		if err := e.field(mode, entry.tag, exifValue{entry.kind, entry.count, entry.data}); !errors.Is(err, ErrInput) {
			t.Fatalf("accepted tag%x", entry.tag)
		}
	}
	good := orientationExif(1, false)
	badOffset := append([]byte(nil), good...)
	order.PutUint32(badOffset[4:], 0xffffffff)
	cycle := syntheticExif(order, testExifDirectory{next: 0})
	duplicate := syntheticExif(order, testExifDirectory{entries: []testExifEntry{exifShort(0x0112, 1, order), exifShort(0x0112, 2, order)}, next: -1})
	for _, data := range [][]byte{nil, []byte("MM\x00"), badOffset, cycle, duplicate} {
		p := imageProfile{width: 2, height: 3}
		if err := p.inspectExif(context.Background(), data); !errors.Is(err, ErrInput) {
			t.Fatal("accepted malformed Exif", err)
		}
	}
}

type shortPublicWriter struct{}

func (shortPublicWriter) Write(body []byte) (int, error) { return len(body) / 2, nil }

type failedPublicWriter struct{}

func (failedPublicWriter) Write([]byte) (int, error) { return 0, errors.New("private writer detail") }
func TestPublicImageStreamBoundsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := inspectPublicImage(ctx, imageBytes(t, "png", 2, 2), "image/png"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, destination := range []io.Writer{shortPublicWriter{}, failedPublicWriter{}} {
		p := imageProfile{mime: "image/png", width: 2, height: 2, orientation: 1}
		if _, _, n, h, err := encodePublicImage(context.Background(), imageBytes(t, "png", 2, 2), p, destination); err == nil || n != 0 || h != "" {
			t.Fatal("partial result accepted")
		}
	}
	var out bytes.Buffer
	w := publicImageWriter{ctx: context.Background(), destination: &out, digest: sha256.New(), limit: 3}
	if n, err := w.Write([]byte("abc")); err != nil || n != 3 {
		t.Fatal(err)
	}
	if n, err := w.Write([]byte("d")); !errors.Is(err, ErrLimited) || n != 0 || out.String() != "abc" {
		t.Fatal("output limit", err)
	}
}

// Adam7 RGBA8 with complete zlib payload and each pass's distinct geometry.
func adam7PNG(t testing.TB, width, height int) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	passes := [][4]int{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}}
	for _, p := range passes {
		pw, ph := 0, 0
		if width > p[0] {
			pw = (width - p[0] + p[2] - 1) / p[2]
		}
		if height > p[1] {
			ph = (height - p[1] + p[3] - 1) / p[3]
		}
		if pw == 0 || ph == 0 {
			continue
		}
		row := make([]byte, 1+4*pw)
		for x := 0; x < pw; x++ {
			copy(row[1+4*x:], []byte{21, 42, 63, 128})
		}
		for y := 0; y < ph; y++ {
			if _, err := zw.Write(row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr, uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(height))
	ihdr[8], ihdr[9], ihdr[12] = 8, 6, 1
	out := append([]byte("\x89PNG\r\n\x1a\n"), pngChunk("IHDR", ihdr)...)
	out = append(out, pngChunk("IDAT", compressed.Bytes())...)
	return append(out, pngChunk("IEND", nil)...)
}
func TestPublicImageAdam7CompleteDecode(t *testing.T) {
	p, out := preparedBytes(t, adam7PNG(t, 13, 9), "image/png")
	if p.width != 13 || p.height != 9 {
		t.Fatal("dimensions")
	}
	decoded, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if c := color.NRGBAModel.Convert(decoded.At(12, 8)).(color.NRGBA); c != (color.NRGBA{21, 42, 63, 128}) {
		t.Fatal(c)
	}
}

func jpegTransformSegments(source []byte, change func(byte, []byte) ([]byte, bool)) []byte {
	out := append([]byte(nil), source[:2]...)
	for offset := 2; offset < len(source); {
		if source[offset] != 255 || offset+1 >= len(source) {
			return source
		}
		marker := source[offset+1]
		if marker == 217 {
			return append(out, 255, 217)
		}
		length := int(binary.BigEndian.Uint16(source[offset+2:]))
		end := offset + 2 + length
		body := append([]byte(nil), source[offset+4:end]...)
		replacement, keep := change(marker, body)
		if keep {
			out = append(out, jpegSegment(marker, replacement)...)
		}
		offset = end
		if marker == 218 {
			start := offset
			for offset < len(source) {
				if source[offset] == 255 && offset+1 < len(source) && source[offset+1] != 0 {
					break
				}
				if source[offset] == 255 {
					offset += 2
				} else {
					offset++
				}
			}
			if keep {
				out = append(out, source[start:offset]...)
			}
		}
	}
	return out
}
func TestPublicImageJPEGTableAndProgressionValidity(t *testing.T) {
	base := progressiveJPEG(16, 8, "ycbcr")
	changed := false
	cases := map[string][]byte{}
	cases["missing-quantization"] = jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) { return body, marker != 219 })
	cases["zero-quantization"] = jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) {
		if marker == 219 {
			body[1] = 0
		}
		return body, true
	})
	cases["16bit-quantization"] = jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) {
		if marker == 219 {
			body = make([]byte, 129)
			body[0] = 16
			for i := 1; i < 129; i += 2 {
				body[i+1] = 1
			}
		}
		return body, true
	})
	cases["missing-huffman"] = jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) { return body, marker != 196 })
	cases["all-ones-huffman"] = jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) {
		if marker == 196 {
			body[1] = 2
			body = append(body[:17], 0, 1)
		}
		return body, true
	})
	cases["oversubscribed-huffman"] = jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) {
		if marker == 196 {
			body[1] = 3
			body = append(body[:17], 0, 1, 2)
		}
		return body, true
	})
	cases["ac-before-dc"] = jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) {
		if marker == 218 && body[len(body)-3] == 0 {
			return body, false
		}
		return body, true
	})
	cases["initial-band-overlap"] = jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) {
		if marker == 218 && body[len(body)-3] != 0 {
			body[1] = 1
		}
		return body, true
	})
	cases["refinement-without-history"] = jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) {
		if marker == 218 && body[len(body)-3] != 0 {
			body[len(body)-1] = 0x10
		}
		return body, true
	})
	cases["quantization-change-after-scans"] = append(append(append([]byte(nil), base[:len(base)-2]...), jpegSegment(219, append([]byte{0}, bytes.Repeat([]byte{2}, 64)...))...), 255, 217)
	changed = false
	cases["missing-component-dc"] = jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) {
		if marker == 218 && !changed {
			changed = true
			return []byte{1, 1, 0, 0, 0, 0}, true
		}
		return body, marker != 218
	})
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := inspectPublicImage(context.Background(), source, "image/jpeg"); !errors.Is(err, ErrInput) {
				t.Fatal("invalid JPEG accepted", err)
			}
		})
	}
	// A legal partial progressive representation may omit AC entirely. Missing
	// bands mean zero; neither all 63 coefficients nor full precision is required.
	dcOnly := jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) { return body, marker != 218 || body[len(body)-3] == 0 })
	preparedBytes(t, dcOnly, "image/jpeg")
}

func TestPublicImageJPEGCanonicalSubsampling(t *testing.T) {
	for _, sample := range [][2]int{{1, 1}, {1, 2}, {2, 1}, {2, 2}, {4, 1}, {4, 2}} {
		t.Run(fmt.Sprint(sample), func(t *testing.T) { preparedBytes(t, progressiveJPEG(32, 32, "ycbcr", sample), "image/jpeg") })
	}
	for _, version := range []uint16{100, 101} {
		for _, model := range []string{"rgb", "ycbcr"} {
			source := progressiveJPEG(16, 16, model)
			app := []byte{'A', 'd', 'o', 'b', 'e', byte(version >> 8), byte(version), 0, 1, 0, 0, 1}
			if model == "rgb" {
				app[11] = 0
			}
			source = append(append(append([]byte(nil), source[:2]...), jpegSegment(238, app)...), source[2:]...)
			preparedBytes(t, source, "image/jpeg")
		}
	}
}
func TestPublicImagePNGLowDepthGrayAndIndexed(t *testing.T) {
	for _, depth := range []byte{1, 2, 4, 8} {
		for _, kind := range []byte{0, 3} {
			width, height := 8, 3
			ihdr := make([]byte, 13)
			binary.BigEndian.PutUint32(ihdr, uint32(width))
			binary.BigEndian.PutUint32(ihdr[4:], uint32(height))
			ihdr[8], ihdr[9] = depth, kind
			var compressed bytes.Buffer
			zw := zlib.NewWriter(&compressed)
			row := make([]byte, 1+int(depth))
			for i := 1; i < len(row); i++ {
				row[i] = 255
			}
			for y := 0; y < height; y++ {
				if _, err := zw.Write(row); err != nil {
					t.Fatal(err)
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			source := append([]byte("\x89PNG\r\n\x1a\n"), pngChunk("IHDR", ihdr)...)
			if kind == 3 {
				palette := make([]byte, 3*(1<<depth))
				for i := 0; i < len(palette); i += 3 {
					palette[i], palette[i+1], palette[i+2] = byte(i/3), 42, 63
				}
				source = append(source, pngChunk("PLTE", palette)...)
			}
			source = append(source, pngChunk("IDAT", compressed.Bytes())...)
			source = append(source, pngChunk("IEND", nil)...)
			preparedBytes(t, source, "image/png")
		}
	}
}

func TestPublicImageJPEGValidRefinementAndSOF1(t *testing.T) {
	base := progressiveJPEG(16, 8, "ycbcr")
	initial := jpegTransformSegments(base, func(marker byte, body []byte) ([]byte, bool) {
		if marker == 218 {
			body[len(body)-1] = 1
		}
		return body, true
	})
	out := append([]byte(nil), initial[:len(initial)-2]...)
	out = append(out, jpegSegment(218, []byte{3, 1, 0, 2, 0, 3, 0, 0, 0, 0x10})...)
	out = append(out, 3) // Six zero DC refinement bits and two one pad bits.
	for _, id := range []byte{1, 2, 3} {
		out = append(out, jpegSegment(218, []byte{1, id, 0, 1, 63, 0x10})...)
		out = append(out, 0x3f)
	} // Two EOB bits, six pad bits.
	out = append(out, 255, 217)
	preparedBytes(t, out, "image/jpeg")
	sequential := imageBytes(t, "jpeg", 16, 16)
	for i := 0; i < len(sequential)-1; i++ {
		if sequential[i] == 255 && sequential[i+1] == 192 {
			sequential[i+1] = 193
			break
		}
	}
	preparedBytes(t, sequential, "image/jpeg")
}
func TestPublicImageMetadataEntryAndSegmentLimits(t *testing.T) {
	data := make([]byte, 10)
	copy(data, []byte{'I', 'I', 42, 0, 8, 0, 0, 0})
	binary.LittleEndian.PutUint16(data[8:], 4097)
	p := imageProfile{width: 2, height: 2}
	if err := p.inspectExif(context.Background(), data); !errors.Is(err, ErrInput) {
		t.Fatal("entry count", err)
	}
	base := imageBytes(t, "jpeg", 16, 16)
	segments := append([]byte(nil), base[:2]...)
	for i := 0; i < maxImageSegments; i++ {
		segments = append(segments, jpegSegment(254, nil)...)
	}
	segments = append(segments, base[2:]...)
	if _, err := inspectPublicImage(context.Background(), segments, "image/jpeg"); !errors.Is(err, ErrInput) {
		t.Fatal("segment count", err)
	}
	metadata := append([]byte(nil), base[:2]...)
	for i := 0; i < 5; i++ {
		metadata = append(metadata, jpegSegment(254, bytes.Repeat([]byte{'a'}, 60000))...)
	}
	metadata = append(metadata, base[2:]...)
	if _, err := inspectPublicImage(context.Background(), metadata, "image/jpeg"); !errors.Is(err, ErrInput) {
		t.Fatal("metadata size", err)
	}
}
