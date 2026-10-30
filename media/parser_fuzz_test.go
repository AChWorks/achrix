// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

// This is a mutation-campaign bound, not a replacement for the larger production
// limits exercised by the existing deterministic resource tests. These targets
// inspect owned framing/metadata; they do not fuzz full native image decoding.
const parserFuzzBytes = 64 << 10

func FuzzPublicPNGFraming(f *testing.F) {
	base := imageBytes(f, "png", 2, 2)
	badCRC := bytes.Clone(base)
	badCRC[29] ^= 1
	header := bytes.Clone(base[16:29])
	zeroWidth := bytes.Clone(header)
	binary.BigEndian.PutUint32(zeroWidth, 0)
	for i, seed := range []struct {
		mode   byte
		body   []byte
		accept bool
	}{
		{0, base, true},
		{0, badCRC, false},
		{0, base[:len(base)-12], false},
		{0, append(bytes.Clone(base), 0), false},
		{1, []byte("Comment\x00ordinary text"), true},
		{1, []byte("XML:com.adobe.xmp\x00owned-private-marker"), false},
		{2, []byte("Comment\x00\x00\x00\x00\x00ordinary text"), true},
		{2, []byte("Comment\x00\x02\x00\x00\x00ordinary text"), false},
		{3, []byte{'P', 0, 8, 0, 0, 0, 255, 0, 1}, true},
		{3, []byte{'P', 0, 8}, false},
		{4, orientationExif(6, true), true},
		{5, header, true},
		{5, zeroWidth, false},
	} {
		_, err := inspectPublicImage(context.Background(), pngFuzzSource(base, seed.mode, seed.body), "image/png")
		if (err == nil) != seed.accept || err != nil && !errors.Is(err, ErrInput) {
			f.Fatalf("PNG seed %d has unexpected category: %v", i, err)
		}
		f.Add(seed.mode, seed.body)
	}
	f.Fuzz(func(t *testing.T, mode byte, body []byte) {
		if len(body) > parserFuzzBytes || mode%6 != 0 && len(body) > parserFuzzBytes-len(base)-12 {
			t.Skip()
		}
		before := sha256.Sum256(body)
		checkImageInspection(t, pngFuzzSource(base, mode, body), "image/png")
		if sha256.Sum256(body) != before {
			t.Fatal("PNG inspection changed its input")
		}
	})
}

// Raw mode reaches length/order/CRC failures. The other small explicit modes
// recompute CRC to reach owned metadata rules instead of stopping at a checksum.
func pngFuzzSource(base []byte, mode byte, body []byte) []byte {
	var kind string
	switch mode % 6 {
	case 0:
		return body
	case 1:
		kind = "tEXt"
	case 2:
		kind = "iTXt"
	case 3:
		kind = "sPLT"
	case 4:
		kind = "eXIf"
	case 5:
		result := append(bytes.Clone(base[:8]), pngChunk("IHDR", body)...)
		return append(result, base[33:]...)
	}
	return insertPNG(base, pngChunk(kind, body))
}

func FuzzPublicJPEGFraming(f *testing.F) {
	base := imageBytes(f, "jpeg", 2, 2)
	progressive := progressiveJPEG(16, 8, "ycbcr")
	dcOnly := jpegTransformSegments(progressive, func(marker byte, body []byte) ([]byte, bool) {
		return body, marker != 218 || body[len(body)-3] == 0
	})
	badRefinement := jpegTransformSegments(progressive, func(marker byte, body []byte) ([]byte, bool) {
		if marker == 218 && body[len(body)-3] != 0 {
			body[len(body)-1] = 0x10
		}
		return body, true
	})
	for i, seed := range []struct {
		body   []byte
		accept bool
	}{
		{base, true},
		{progressive, true},
		{progressiveJPEG(16, 8, "gray"), true},
		{progressiveJPEG(16, 8, "rgb"), true},
		{dcOnly, true},
		{badRefinement, false},
		{base[:len(base)-2], false},
		{append(bytes.Clone(base), 0), false},
		{[]byte{255, 216, 255, 254, 0, 1, 255, 217}, false},
		{nil, false},
	} {
		_, err := inspectPublicImage(context.Background(), seed.body, "image/jpeg")
		if (err == nil) != seed.accept || err != nil && !errors.Is(err, ErrInput) {
			f.Fatalf("JPEG seed %d has unexpected category: %v", i, err)
		}
		f.Add(seed.body)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > parserFuzzBytes {
			t.Skip()
		}
		checkImageInspection(t, body, "image/jpeg")
	})
}

func checkImageInspection(t testing.TB, source []byte, mime string) {
	t.Helper()
	before := sha256.Sum256(source)
	profile, err := inspectPublicImage(context.Background(), source, mime)
	if err != nil {
		if !errors.Is(err, ErrInput) || profile != (imageProfile{}) {
			t.Fatalf("image rejection lost its category or returned a partial profile: %v", err)
		}
	} else {
		if profile.mime != mime || profile.width < 1 || profile.height < 1 ||
			profile.width > MaxDimension || profile.height > MaxDimension ||
			int64(profile.width)*int64(profile.height) > MaxPixels || profile.orientation < 1 || profile.orientation > 8 {
			t.Fatal("admitted image profile exceeds its interpretation bounds")
		}
		if mime == "image/png" {
			switch profile.pngColor {
			case 0, 3:
				if profile.pngDepth != 1 && profile.pngDepth != 2 && profile.pngDepth != 4 && profile.pngDepth != 8 {
					t.Fatal("admitted PNG depth is outside the profile")
				}
			case 2, 4, 6:
				if profile.pngDepth != 8 {
					t.Fatal("admitted PNG depth is outside the profile")
				}
			default:
				t.Fatal("admitted PNG color type is outside the profile")
			}
			if profile.srgb && profile.srgbIntent > 3 {
				t.Fatal("admitted PNG rendering intent is outside the profile")
			}
		} else if profile.jpegModel != "gray" && profile.jpegModel != "rgb" && profile.jpegModel != "ycbcr" {
			t.Fatal("admitted JPEG model is outside the profile")
		}
		switch profile.colorBasis() {
		case "declared-srgb", "legacy-canonical-png", "assumed-untagged":
		default:
			t.Fatal("admitted image has an unknown color basis")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if rejected, err := inspectPublicImage(ctx, source, mime); !errors.Is(err, context.Canceled) || rejected != (imageProfile{}) {
			t.Fatal("accepted framing did not honor cancellation")
		}
	}
	again, againErr := inspectPublicImage(context.Background(), source, mime)
	if again != profile || (againErr == nil) != (err == nil) || againErr != nil && !errors.Is(againErr, ErrInput) {
		t.Fatal("image inspection changed its result for identical input")
	}
	if sha256.Sum256(source) != before {
		t.Fatal("image inspection changed its input")
	}
}

func FuzzPublicExif(f *testing.F) {
	good := orientationExif(8, true)
	badOffset := bytes.Clone(good)
	binary.LittleEndian.PutUint32(badOffset[4:], ^uint32(0))
	badCount := bytes.Clone(good)
	binary.LittleEndian.PutUint32(badCount[14:], ^uint32(0))
	big := binary.BigEndian
	bigEndian := syntheticExif(big,
		testExifDirectory{entries: []testExifEntry{exifShort(0x0112, 6, big), exifLong(0x8769, 1, big)}, next: -1},
		testExifDirectory{entries: []testExifEntry{exifShort(0xa001, 1, big), exifLong(0xa002, 2, big), exifLong(0xa003, 3, big)}, next: -1})
	for i, seed := range []struct {
		body   []byte
		accept bool
	}{
		{good, true},
		{bigEndian, true},
		{syntheticExif(binary.LittleEndian, testExifDirectory{next: -1}), true},
		{nil, false},
		{[]byte("MM\x00"), false},
		{badOffset, false},
		{badCount, false},
		{syntheticExif(binary.LittleEndian, testExifDirectory{next: 0}), false},
		{syntheticExif(big, testExifDirectory{entries: []testExifEntry{exifShort(0x0112, 1, big), exifShort(0x0112, 2, big)}, next: -1}), false},
		{orientationExif(9, false), false},
	} {
		profile := imageProfile{mime: "image/jpeg", width: 2, height: 3, orientation: 1, jpegModel: "ycbcr", jpeg444: true}
		err := profile.inspectExif(context.Background(), seed.body)
		if (err == nil) != seed.accept || err != nil && !errors.Is(err, ErrInput) {
			f.Fatalf("Exif seed %d has unexpected category: %v", i, err)
		}
		f.Add(seed.body)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > parserFuzzBytes {
			t.Skip()
		}
		before := sha256.Sum256(body)
		initial := imageProfile{mime: "image/jpeg", width: 2, height: 3, orientation: 1, jpegModel: "ycbcr", jpeg444: true}
		profile, again := initial, initial
		err := profile.inspectExif(context.Background(), body)
		againErr := again.inspectExif(context.Background(), body)
		if err != nil && !errors.Is(err, ErrInput) || againErr != nil && !errors.Is(againErr, ErrInput) || (err == nil) != (againErr == nil) {
			t.Fatal("Exif inspection lost its category or determinism")
		}
		if err == nil {
			want := initial
			want.orientation, want.exifSRGB = profile.orientation, profile.exifSRGB
			if profile != want || profile != again || profile.orientation < 1 || profile.orientation > 8 {
				t.Fatal("Exif changed the image interpretation outside its supported fields")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := initial.inspectExif(ctx, body); !errors.Is(err, context.Canceled) {
				t.Fatal("accepted Exif did not honor cancellation")
			}
		}
		// A rejected private profile may be partially populated; the outer
		// inspectPublicImage boundary owns discarding it, checked above.
		if sha256.Sum256(body) != before {
			t.Fatal("Exif inspection changed its input")
		}
	})
}

func FuzzSVGStructure(f *testing.F) {
	for i, seed := range []struct {
		body   string
		accept bool
	}{
		{svgOpen + svgClose, true},
		{`<s:svg xmlns:s="http://www.w3.org/2000/svg"><s:g/></s:svg>`, true},
		{"\ufeff" + svgOriginal, true},
		{svgOpen + strings.Repeat("<g>", maxSVGDepth-1) + strings.Repeat("</g>", maxSVGDepth-1) + svgClose, true},
		{svgOpen + strings.Repeat("<g>", maxSVGDepth) + strings.Repeat("</g>", maxSVGDepth) + svgClose, false},
		{"", false},
		{`<svg xmlns="urn:wrong"/>`, false},
		{svgOpen + "<g>", false},
		{svgOpen + svgClose + svgOpen + svgClose, false},
		{"<!DOCTYPE svg>" + svgOpen + svgClose, false},
		{`<svg xmlns="http://www.w3.org/2000/svg" a="one"b="two"/>`, false},
		{svgOpen + "\xff" + svgClose, false},
	} {
		err := svgModule().validateSVG(context.Background(), validationFile(f, []byte(seed.body)))
		if (err == nil) != seed.accept || err != nil && !errors.Is(err, ErrInput) {
			f.Fatalf("SVG seed %d has unexpected category: %v", i, err)
		}
		f.Add([]byte(seed.body))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > parserFuzzBytes {
			t.Skip()
		}
		before := sha256.Sum256(body)
		file := validationFile(t, body)
		module := svgModule()
		err := module.validateSVG(context.Background(), file)
		againErr := module.validateSVG(context.Background(), file)
		if err != nil && !errors.Is(err, ErrInput) || againErr != nil && !errors.Is(againErr, ErrInput) || (err == nil) != (againErr == nil) {
			t.Fatal("SVG inspection lost its category or determinism")
		}
		if len(module.decoders) != 0 {
			t.Fatal("SVG inspection leaked decoder admission")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := module.validateSVG(ctx, file); !errors.Is(err, context.Canceled) || len(module.decoders) != 0 {
			t.Fatal("SVG cancellation lost its category or leaked admission")
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		stored, err := io.ReadAll(io.LimitReader(file, parserFuzzBytes+1))
		if err != nil || !bytes.Equal(stored, body) || sha256.Sum256(body) != before {
			t.Fatal("SVG inspection changed its private original")
		}
	})
}

func TestSVGRejectionKeepsActionablePrivateBoundary(t *testing.T) {
	const private = "owned-private-payload-and-filename"
	module, diagnostics := diagnosticModule(t)
	module.config.AllowedMIMEs = []string{"image/svg+xml"}
	module.decoders = make(chan struct{}, 2)
	storage := testStorage(t)
	for _, body := range []string{
		`<g xmlns="http://www.w3.org/2000/svg">` + private + `</g>`,
		svgOpen + "<title>" + private + "</title>",
	} {
		_, err := storage.write(context.Background(), module, Asset{ID: newID(), Filename: private + ".svg"}, strings.NewReader(body))
		var input *InputError
		if !errors.Is(err, ErrInput) || !errors.As(err, &input) {
			t.Fatalf("structural SVG rejection lost its input category: %v", err)
		}
		if input.Field() != "file" || input.Reason() != "invalid_format" ||
			!strings.Contains(err.Error(), "enabled format") || !strings.Contains(err.Error(), "content and image limits") {
			t.Fatalf("rejection lost useful field, reason or next-action guidance: %v", err)
		}
		if strings.Contains(err.Error(), private) || len(module.decoders) != 0 {
			t.Fatal("rejection disclosed private input or leaked decoder admission")
		}
	}
	if diagnostics.String() != "" || module.FailureCount() != 0 {
		t.Fatal("expected parser rejections generated operational logs")
	}
}
