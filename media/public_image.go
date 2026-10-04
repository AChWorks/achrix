// SPDX-License-Identifier: MPL-2.0
package media

import (
 "bytes"
 "context"
 "crypto/sha256"
 "encoding/hex"
 "hash"
 "image"
 "image/color"
 "image/jpeg"
 "image/png"
 "io"
 "time"

 "github.com/AChWorks/achrix"
)

const (
 PreparePublicImage = "achrix.media.prepare-public-image"
 PublicImageProfile = "achrix-public-image-v1"
 MaxPublicImageBytes int64 = 40 << 20
 maxImageMetadata = 256 << 10
 maxImageSegments = 4096
)

// PreparePublicImageRequest binds preparation to one known ready revision.
type PreparePublicImageRequest struct {
 AssetID string
 ExpectedRevision int64
}

// PublicImage identifies a complete private output and its verified source
// snapshot. It grants neither publication nor future freshness/authorization.
type PublicImage struct {
 SourceAssetID string
 SourceRevision int64
 SourceSHA256 string
 Profile string
 ColorBasis string
 MIME string
 Size int64
 SHA256 string
 Width, Height int
}

// PreparePublicImage writes only to a trusted PRIVATE destination. Products must
// discard partial output on every error and activate only complete results after
// their own publication/freshness checks. It does not close the destination.
// Trusted synchronous I/O must honor actual transport/caller deadlines; a
// context cannot forcibly interrupt arbitrary Write or native codec work.
func (s *Service) PreparePublicImage(parent context.Context, actor achrix.Principal, request PreparePublicImageRequest, destination io.Writer) (PublicImage, error) {
 if !validID(request.AssetID) || request.ExpectedRevision < 1 || destination == nil { return PublicImage{}, ErrInput }
 ctx, cancel := deadline(parent, 15*time.Second)
 defer cancel()
 if err := s.app.Authorize(ctx, actor, PreparePublicImage, request.AssetID); err != nil { return PublicImage{}, err }
 return s.module.preparePublicImage(ctx, request, destination)
}

func (m *Module) preparePublicImage(parent context.Context, request PreparePublicImageRequest, destination io.Writer) (PublicImage, error) {
 ctx, pool, storage, finish, err := m.acquire(parent)
 if err != nil { return PublicImage{}, err }
 defer finish()
 if err = ctx.Err(); err != nil { return PublicImage{}, err }
 // Admission precedes even the source allocation; uploads and SVG share these
 // two slots, regardless of the separately configured general operation budget.
 select {
 case m.decoders <- struct{}{}: defer func() { <-m.decoders }()
 default: return PublicImage{}, ErrLimited
 }
 releaseStorage, err := storage.lock(true)
 if err != nil { return PublicImage{}, m.failure(ctx, "prepare_storage_lock", err) }
 defer releaseStorage()
 connection, releaseAsset, err := lockAsset(ctx, pool, request.AssetID, true)
 if err != nil { return PublicImage{}, m.failure(ctx, "prepare_asset_lock", err) }
 defer releaseAsset()
 asset, err := findAsset(ctx, connection, request.AssetID)
 if err != nil { return PublicImage{}, m.failure(ctx, "prepare_source", err) }
 if asset.State != "ready" { return PublicImage{}, ErrNotFound }
 if asset.Revision != request.ExpectedRevision { return PublicImage{}, ErrConflict }
 if asset.MIME != "image/png" && asset.MIME != "image/jpeg" { return PublicImage{}, ErrInput }
 if asset.Size < 1 || asset.Size > MaxUploadBytes || !publicDimensions(asset.Width, asset.Height) { return PublicImage{}, m.failure(ctx, "prepare_source_metadata", ErrUnavailable) }
 file, err := storage.open(asset.ID)
 if err != nil { return PublicImage{}, m.failure(ctx, "prepare_source_storage", err) }
 defer func() { _ = file.Close() }()
 info, err := file.Stat()
 if err != nil || info.Size() != asset.Size { return PublicImage{}, m.failure(ctx, "prepare_source_size", ErrUnavailable) }
 snapshot := make([]byte, int(asset.Size))
 if _, err = io.ReadFull(contextReader{ctx, file}, snapshot); err != nil { return PublicImage{}, m.failure(ctx, "prepare_snapshot", err) }
 var extra [1]byte
 if n, e := (contextReader{ctx, file}).Read(extra[:]); n != 0 || e != io.EOF { return PublicImage{}, m.failure(ctx, "prepare_snapshot_size", ErrUnavailable) }
 sourceHash := sha256.Sum256(snapshot)
 if hex.EncodeToString(sourceHash[:]) != asset.SHA256 { return PublicImage{}, m.failure(ctx, "prepare_source_integrity", ErrUnavailable) }
 profile, err := inspectPublicImage(ctx, snapshot, asset.MIME)
 if err != nil { return PublicImage{}, err }
 if profile.width != asset.Width || profile.height != asset.Height { return PublicImage{}, m.failure(ctx, "prepare_source_dimensions", ErrUnavailable) }
 width, height, size, outputHash, err := encodePublicImage(ctx, snapshot, profile, destination)
 if err != nil { return PublicImage{}, m.failure(ctx, "prepare_output", err) }
 return PublicImage{SourceAssetID: asset.ID, SourceRevision: asset.Revision, SourceSHA256: asset.SHA256, Profile: PublicImageProfile, ColorBasis: profile.colorBasis(), MIME: asset.MIME, Size: size, SHA256: outputHash, Width: width, Height: height}, nil
}

type imageProfile struct {
 mime string
 width, height int
 orientation int
 pngDepth, pngColor byte
 srgb bool
 srgbIntent byte
 gamma, chromaticity bool
 exifSRGB bool
 jpegModel string
 jpeg444 bool
}
func (p imageProfile) colorBasis() string {
 if p.srgb { return "declared-srgb" }
 if p.gamma || p.chromaticity { return "legacy-canonical-png" }
 if p.exifSRGB { return "declared-srgb" }
 return "assumed-untagged"
}
func publicDimensions(width, height int) bool {
 return width > 0 && height > 0 && width <= MaxDimension && height <= MaxDimension && int64(width)*int64(height) <= MaxPixels
}
func inspectPublicImage(ctx context.Context, source []byte, mime string) (imageProfile, error) {
 if len(source) == 0 || int64(len(source)) > MaxUploadBytes { return imageProfile{}, ErrInput }
 p := imageProfile{mime: mime, orientation: 1}
 var err error
 switch mime {
 case "image/png": err = p.inspectPNG(ctx, source)
 case "image/jpeg": err = p.inspectJPEG(ctx, source)
 default: err = ErrInput
 }
 if err != nil { return imageProfile{}, err }
 return p, nil
}

func encodePublicImage(ctx context.Context, source []byte, profile imageProfile, destination io.Writer) (int, int, int64, string, error) {
 if err := ctx.Err(); err != nil { return 0, 0, 0, "", err }
 var pixels image.Image
 var err error
 switch profile.mime {
 case "image/png": pixels, err = png.Decode(bytes.NewReader(source))
 case "image/jpeg": pixels, err = jpeg.Decode(bytes.NewReader(source))
 default: err = ErrInput
 }
 if err != nil { return 0, 0, 0, "", ErrInput }
 if pixels.Bounds().Dx() != profile.width || pixels.Bounds().Dy() != profile.height { return 0, 0, 0, "", ErrInput }
 pixels, err = normalizePublicPixels(ctx, pixels, profile)
 if err != nil { return 0, 0, 0, "", err }
 counter := &publicImageWriter{ctx: ctx, destination: destination, digest: sha256.New(), limit: MaxPublicImageBytes}
 header := &imageHeaderWriter{destination: counter}
 if profile.mime == "image/png" {
  header.prefixSize = 33 // signature and the complete native IHDR chunk
  header.insert = profile.pngOutputDeclarations()
  err = png.Encode(header, pixels)
 } else {
  header.prefixSize = 2 // native SOI
  header.insert = profile.jpegOutputDeclarations()
  err = jpeg.Encode(header, pixels, &jpeg.Options{Quality: 90})
 }
 if err == nil { err = ctx.Err() }
 if err != nil { return 0, 0, 0, "", err }
 return pixels.Bounds().Dx(), pixels.Bounds().Dy(), counter.size, hex.EncodeToString(counter.digest.Sum(nil)), nil
}

// Only native decoded concrete models admitted by preflight reach this function.
// Typed row/pixel access avoids an interface allocation per pixel. PNG receives
// straight NRGBA, with fully transparent RGB canonicalized to zero.
func normalizePublicPixels(ctx context.Context, source image.Image, p imageProfile) (image.Image, error) {
 if p.mime == "image/jpeg" && p.orientation == 1 { return source, ctx.Err() }
 if sourceNRGBA, ok := source.(*image.NRGBA); ok && p.orientation == 1 {
  for y := 0; y < p.height; y++ {
   if err := ctx.Err(); err != nil { return nil, err }
   row := sourceNRGBA.Pix[y*sourceNRGBA.Stride:y*sourceNRGBA.Stride+4*p.width]
   for x := 0; x < len(row); x += 4 { if row[x+3] == 0 { row[x], row[x+1], row[x+2] = 0, 0, 0 } }
  }
  return sourceNRGBA, nil
 }
 width, height := p.width, p.height
 if p.orientation >= 5 { width, height = height, width }
 if gray, ok := source.(*image.Gray); ok && p.mime == "image/jpeg" {
  result := image.NewGray(image.Rect(0, 0, width, height))
  for y := 0; y < p.height; y++ {
   if err := ctx.Err(); err != nil { return nil, err }
   for x := 0; x < p.width; x++ { dx, dy := orientedPixel(x, y, p.width, p.height, p.orientation); result.Pix[dy*result.Stride+dx] = gray.Pix[y*gray.Stride+x] }
  }
  return result, nil
 }
 result := image.NewNRGBA(image.Rect(0, 0, width, height))
 var palette []color.NRGBA
 if indexed, ok := source.(*image.Paletted); ok {
  palette = make([]color.NRGBA, len(indexed.Palette))
  for i, c := range indexed.Palette { palette[i] = color.NRGBAModel.Convert(c).(color.NRGBA) }
 }
 for y := 0; y < p.height; y++ {
  if err := ctx.Err(); err != nil { return nil, err }
  for x := 0; x < p.width; x++ {
   var r, g, b, a byte
   switch src := source.(type) {
   case *image.NRGBA: offset := y*src.Stride+4*x; r, g, b, a = src.Pix[offset], src.Pix[offset+1], src.Pix[offset+2], src.Pix[offset+3]
   case *image.RGBA: offset := y*src.Stride+4*x; r, g, b, a = src.Pix[offset], src.Pix[offset+1], src.Pix[offset+2], src.Pix[offset+3]; if a != 0 && a != 255 { r, g, b = byte(uint32(r)*255/uint32(a)), byte(uint32(g)*255/uint32(a)), byte(uint32(b)*255/uint32(a)) }
   case *image.Gray: r = src.Pix[y*src.Stride+x]; g, b, a = r, r, 255
   case *image.Paletted: c := palette[src.Pix[y*src.Stride+x]]; r, g, b, a = c.R, c.G, c.B, c.A
   case *image.YCbCr: yi, ci := src.YOffset(x, y), src.COffset(x, y); r, g, b = color.YCbCrToRGB(src.Y[yi], src.Cb[ci], src.Cr[ci]); a = 255
   default: return nil, ErrInput
   }
   if a == 0 { r, g, b = 0, 0, 0 }
   dx, dy := orientedPixel(x, y, p.width, p.height, p.orientation)
   offset := dy*result.Stride+4*dx
   result.Pix[offset], result.Pix[offset+1], result.Pix[offset+2], result.Pix[offset+3] = r, g, b, a
  }
 }
 return result, nil
}
func orientedPixel(x, y, width, height, orientation int) (int, int) {
 switch orientation {
 case 2: return width-1-x, y
 case 3: return width-1-x, height-1-y
 case 4: return x, height-1-y
 case 5: return y, x
 case 6: return height-1-y, x
 case 7: return height-1-y, width-1-x
 case 8: return y, width-1-x
 default: return x, y
 }
}

type publicImageWriter struct {
 ctx context.Context
 destination io.Writer
 digest hash.Hash
 size, limit int64
}
func (w *publicImageWriter) Write(body []byte) (int, error) {
 if err := w.ctx.Err(); err != nil { return 0, err }
 if int64(len(body)) > w.limit-w.size { return 0, ErrLimited }
 n, err := w.destination.Write(body)
 if n < 0 || n > len(body) { return 0, io.ErrShortWrite }
 if n != 0 { _, _ = w.digest.Write(body[:n]); w.size += int64(n) }
 if n != len(body) && err == nil { err = io.ErrShortWrite }
 return n, err
}

// Native encoders are used unchanged; only a small fresh finite header is
// inserted. Prefix buffering is at most33 bytes, never full output buffering.
type imageHeaderWriter struct {
 destination io.Writer
 prefixSize int
 prefix [33]byte
 buffered int
 insert []byte
 inserted bool
}
func (w *imageHeaderWriter) Write(body []byte) (int, error) {
 consumed := 0
 if !w.inserted {
  n := min(len(body), w.prefixSize-w.buffered)
  copy(w.prefix[w.buffered:], body[:n]); w.buffered += n; consumed += n; body = body[n:]
  if w.buffered < w.prefixSize { return consumed, nil }
  if _, err := w.destination.Write(w.prefix[:w.prefixSize]); err != nil { return consumed, err }
  if len(w.insert) != 0 { if _, err := w.destination.Write(w.insert); err != nil { return consumed, err } }
  w.inserted = true
 }
 if len(body) != 0 { n, err := w.destination.Write(body); return consumed+n, err }
 return consumed, nil
}
