// SPDX-License-Identifier: MPL-2.0
package media

import (
	"bytes"
	"context"
	"encoding/binary"
)

type exifValue struct {
	kind  uint16
	count uint32
	data  []byte
}
type exifInspector struct {
	ctx                      context.Context
	data                     []byte
	order                    binary.ByteOrder
	profile                  *imageProfile
	visited                  map[uint32]bool
	entries                  int
	resolutionX, resolutionY []byte
}

func (p *imageProfile) inspectExif(ctx context.Context, data []byte) error {
	if len(data) < 8 {
		return ErrInput
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return ErrInput
	}
	if order.Uint16(data[2:4]) != 42 {
		return ErrInput
	}
	inspector := exifInspector{ctx: ctx, data: data, order: order, profile: p, visited: make(map[uint32]bool)}
	first := order.Uint32(data[4:8])
	if first < 8 {
		return ErrInput
	}
	if err := inspector.directory(first, "ifd0", 1); err != nil {
		return err
	}
	if (inspector.resolutionX == nil) != (inspector.resolutionY == nil) {
		return ErrInput
	}
	if inspector.resolutionX != nil {
		x, y := inspector.resolutionX, inspector.resolutionY
		xn, xd, yn, yd := order.Uint32(x), order.Uint32(x[4:]), order.Uint32(y), order.Uint32(y[4:])
		if xn == 0 || xd == 0 || yn == 0 || yd == 0 || uint64(xn)*uint64(yd) != uint64(yn)*uint64(xd) {
			return ErrInput
		}
	}
	return nil
}
func (e *exifInspector) span(offset uint32, size uint64) ([]byte, bool) {
	if uint64(offset) > uint64(len(e.data)) || size > uint64(len(e.data))-uint64(offset) {
		return nil, false
	}
	return e.data[int(offset):int(uint64(offset)+size)], true
}
func (e *exifInspector) directory(offset uint32, mode string, depth int) error {
	if err := e.ctx.Err(); err != nil {
		return err
	}
	if depth > 4 || len(e.visited) >= 8 || offset < 8 || e.visited[offset] {
		return ErrInput
	}
	e.visited[offset] = true
	head, ok := e.span(offset, 2)
	if !ok {
		return ErrInput
	}
	count := int(e.order.Uint16(head))
	if count > 4096-e.entries {
		return ErrInput
	}
	e.entries += count
	body, ok := e.span(offset+2, uint64(count)*12+4)
	if !ok {
		return ErrInput
	}
	values := make(map[uint16]exifValue, count)
	for i := 0; i < count; i++ {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		entry := body[i*12 : i*12+12]
		tag := e.order.Uint16(entry)
		kind := e.order.Uint16(entry[2:])
		n := e.order.Uint32(entry[4:])
		if _, duplicate := values[tag]; duplicate || n == 0 {
			return ErrInput
		}
		var unit uint64
		switch kind {
		case 1, 2, 7, 129:
			unit = 1
		case 3:
			unit = 2
		case 4, 9:
			unit = 4
		case 5, 10:
			unit = 8
		default:
			return ErrInput
		}
		size := unit * uint64(n)
		var value []byte
		if size <= 4 {
			value = entry[8 : 8+int(size)]
		} else {
			var found bool
			value, found = e.span(e.order.Uint32(entry[8:]), size)
			if !found {
				return ErrInput
			}
		}
		if kind == 2 {
			if value[len(value)-1] != 0 {
				return ErrInput
			}
			for _, v := range value {
				if v > 127 {
					return ErrInput
				}
			}
		}
		values[tag] = exifValue{kind: kind, count: n, data: value}
	}
	for tag, value := range values {
		switch tag {
		case 0x8769, 0x8825, 0xa005:
			if value.kind != 4 || value.count != 1 {
				return ErrInput
			}
			child := e.order.Uint32(value.data)
			childMode := "exif"
			if tag == 0x8825 {
				childMode = "gps"
			}
			if tag == 0xa005 {
				childMode = "interop"
			}
			if !(mode == "ifd0" && (tag == 0x8769 || tag == 0x8825) || mode == "exif" && tag == 0xa005) {
				return ErrInput
			}
			if child == 0 {
				return ErrInput
			}
			if err := e.directory(child, childMode, depth+1); err != nil {
				return err
			}
		default:
			if mode == "gps" || mode == "thumbnail" {
				continue
			}
			if err := e.field(mode, tag, value); err != nil {
				return err
			}
		}
	}
	if mode == "thumbnail" {
		if err := e.thumbnailSpans(values); err != nil {
			return err
		}
	}
	next := e.order.Uint32(body[count*12:])
	if next != 0 {
		if mode != "ifd0" && mode != "thumbnail" {
			return ErrInput
		}
		nextMode := "thumbnail"
		if err := e.directory(next, nextMode, depth+1); err != nil {
			return err
		}
	}
	return nil
}
func (e *exifInspector) scalar(value exifValue, kind uint16) (uint32, bool) {
	if value.kind != kind || value.count != 1 {
		return 0, false
	}
	if kind == 3 {
		return uint32(e.order.Uint16(value.data)), true
	}
	if kind == 4 {
		return e.order.Uint32(value.data), true
	}
	return 0, false
}
func (e *exifInspector) dimension(value exifValue, want int) bool {
	if value.kind != 3 && value.kind != 4 {
		return false
	}
	v, ok := e.scalar(value, value.kind)
	return ok && v == uint32(want)
}
func (e *exifInspector) rational(value exifValue, want [][2]uint32) bool {
	if value.kind != 5 || value.count != uint32(len(want)) {
		return false
	}
	for i, pair := range want {
		n, d := e.order.Uint32(value.data[i*8:]), e.order.Uint32(value.data[i*8+4:])
		if d == 0 || uint64(n)*uint64(pair[1]) != uint64(pair[0])*uint64(d) {
			return false
		}
	}
	return true
}
func (e *exifInspector) field(mode string, tag uint16, value exifValue) error {
	p := e.profile
	if mode == "interop" {
		switch tag {
		case 1:
			if value.kind != 2 || value.count != 4 || !bytes.Equal(value.data, []byte{'R', '9', '8', 0}) {
				return ErrInput
			}
		case 2:
			if value.kind != 7 || value.count != 4 || !bytes.Equal(value.data, []byte("0100")) {
				return ErrInput
			}
		case 0x1000:
			if value.kind != 2 {
				return ErrInput
			}
			expected := []byte("JPEG\x00")
			if p.mime == "image/png" {
				expected = []byte("PNG\x00")
			}
			if !bytes.Equal(value.data, expected) {
				return ErrInput
			}
		case 0x1001:
			if !e.dimension(value, p.width) {
				return ErrInput
			}
		case 0x1002:
			if !e.dimension(value, p.height) {
				return ErrInput
			}
		default:
			return ErrInput
		}
		return nil
	}
	if mode != "ifd0" && mode != "exif" {
		return ErrInput
	}
	switch tag {
	case 0x0112:
		v, ok := e.scalar(value, 3)
		if mode != "ifd0" || !ok || v < 1 || v > 8 {
			return ErrInput
		}
		p.orientation = int(v)
	case 0xa001:
		v, ok := e.scalar(value, 3)
		if mode != "exif" || !ok || v != 1 {
			return ErrInput
		}
		p.exifSRGB = true
	case 0xa500, 0x012d, 0x0214:
		return ErrInput
	case 0x013e:
		if mode != "ifd0" || !e.rational(value, [][2]uint32{{3127, 10000}, {3290, 10000}}) {
			return ErrInput
		}
	case 0x013f:
		if mode != "ifd0" || !e.rational(value, [][2]uint32{{6400, 10000}, {3300, 10000}, {3000, 10000}, {6000, 10000}, {1500, 10000}, {600, 10000}}) {
			return ErrInput
		}
	case 0x0211:
		if mode != "ifd0" || !e.rational(value, [][2]uint32{{299, 1000}, {587, 1000}, {114, 1000}}) {
			return ErrInput
		}
	case 0x0213:
		v, ok := e.scalar(value, 3)
		if mode != "ifd0" || !ok || v != 1 && !(v == 2 && p.jpeg444) {
			return ErrInput
		}
	case 0x011a, 0x011b:
		if mode != "ifd0" || value.kind != 5 || value.count != 1 {
			return ErrInput
		}
		if tag == 0x011a {
			e.resolutionX = value.data
		} else {
			e.resolutionY = value.data
		}
	case 0x0128:
		v, ok := e.scalar(value, 3)
		if mode != "ifd0" || !ok || v != 2 && v != 3 {
			return ErrInput
		}
	case 0x9101:
		expected := []byte{1, 2, 3, 0}
		if p.jpegModel == "rgb" {
			expected = []byte{4, 5, 6, 0}
		}
		if p.jpegModel == "gray" {
			expected = []byte{1, 0, 0, 0}
		}
		if mode != "exif" || value.kind != 7 || value.count != 4 || !bytes.Equal(value.data, expected) {
			return ErrInput
		}
	case 0xa002:
		if mode != "exif" || !e.dimension(value, p.width) {
			return ErrInput
		}
	case 0xa003:
		if mode != "exif" || !e.dimension(value, p.height) {
			return ErrInput
		}
	case 0x9000:
		if mode != "exif" || value.kind != 7 || value.count != 4 {
			return ErrInput
		}
		switch string(value.data) {
		case "0200", "0210", "0220", "0221", "0230", "0231", "0232", "0300":
		default:
			return ErrInput
		}
	case 0x0100, 0x0101, 0x0102, 0x0103, 0x0106, 0x0111, 0x0115, 0x0116, 0x0117, 0x011c, 0x0201, 0x0202, 0x0212:
		return ErrInput
	default:
		if mode == "ifd0" {
			switch tag {
			case 0x010e, 0x010f, 0x0110, 0x0131, 0x0132, 0x013b, 0x8298:
				return nil
			}
			return ErrInput
		}
		if !descriptiveExifTag(tag) {
			return ErrInput
		}
	}
	return nil
}
func descriptiveExifTag(tag uint16) bool {
	switch tag {
	case 0x829a, 0x829d, 0x8822, 0x8824, 0x8827, 0x8828, 0x9003, 0x9004, 0x9102, 0x9214, 0x927c, 0x9286, 0x9287, 0xa000, 0xa004, 0xa20b, 0xa20c, 0xa20e, 0xa20f, 0xa210, 0xa214, 0xa215, 0xa217, 0xa420:
		return true
	}
	return tag >= 0x8830 && tag <= 0x8835 || tag >= 0x9010 && tag <= 0x9012 || tag >= 0x9201 && tag <= 0x920a || tag >= 0x9290 && tag <= 0x9292 || tag >= 0x9400 && tag <= 0x9405 || tag >= 0xa300 && tag <= 0xa302 || tag >= 0xa401 && tag <= 0xa412 || tag >= 0xa430 && tag <= 0xa43c || tag >= 0xa460 && tag <= 0xa462
}
func (e *exifInspector) thumbnailSpans(values map[uint16]exifValue) error {
	offset, hasOffset := values[0x0201]
	size, hasSize := values[0x0202]
	if hasOffset != hasSize {
		return ErrInput
	}
	if hasOffset {
		start, ok := e.scalar(offset, 4)
		if !ok {
			return ErrInput
		}
		n, ok := e.scalar(size, 4)
		if !ok || n == 0 {
			return ErrInput
		}
		if _, ok := e.span(start, uint64(n)); !ok {
			return ErrInput
		}
	}
	offsets, hasOffsets := values[0x0111]
	sizes, hasSizes := values[0x0117]
	if hasOffsets != hasSizes {
		return ErrInput
	}
	if hasOffsets {
		if offsets.kind != 3 && offsets.kind != 4 || sizes.kind != 3 && sizes.kind != 4 || offsets.count != sizes.count {
			return ErrInput
		}
		for i := uint32(0); i < offsets.count; i++ {
			var start, n uint32
			if offsets.kind == 3 {
				start = uint32(e.order.Uint16(offsets.data[2*i:]))
			} else {
				start = e.order.Uint32(offsets.data[4*i:])
			}
			if sizes.kind == 3 {
				n = uint32(e.order.Uint16(sizes.data[2*i:]))
			} else {
				n = e.order.Uint32(sizes.data[4*i:])
			}
			if n == 0 {
				return ErrInput
			}
			if _, ok := e.span(start, uint64(n)); !ok {
				return ErrInput
			}
		}
	}
	return nil
}
