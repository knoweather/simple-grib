package grib1

import (
	"fmt"

	"github.com/knoweather/simple-grib/internal/octetio"
)

// Bitmap is section 3 (BMS, bitmap section). One bit per grid point:
// 1 = the point has a value, 0 = missing.
//
// GRIB1 defines bitmaps only as "defined in this section" (the table reference in
// octets 5-6 must be 0); other values point at a predefined table this package does
// not carry, so parseBMS fails loudly instead of guessing.
type Bitmap struct {
	TableRef   int // octets 5-6
	UnusedBits int // octet 4: unused bits at the end of the section
	bits       *octetio.Bits
}

// parseBMS parses section 3 (bitmap).
func parseBMS(sec Section) (*Bitmap, error) {
	r := reader{sec.Data}
	if sec.Length < minBMSLen {
		return nil, fmt.Errorf("grib1: section 3 length %d, want >= %d", sec.Length, minBMSLen)
	}
	unused, err := r.u8(4)
	if err != nil {
		return nil, err
	}
	tableRef, err := r.u16(5)
	if err != nil {
		return nil, err
	}
	if tableRef != 0 {
		return nil, fmt.Errorf("grib1: section 3: bitmap table reference %d is not supported (only 0 = bitmap defined in this section)", tableRef)
	}
	body, err := r.slice(7, sec.Length-6)
	if err != nil {
		return nil, err
	}
	if int(unused) > len(body)*8 {
		return nil, fmt.Errorf("grib1: section 3: declares %d unused bits but the bitmap is only %d bits", unused, len(body)*8)
	}
	return &Bitmap{TableRef: int(tableRef), UnusedBits: int(unused), bits: octetio.NewBits(body)}, nil
}

// Present reports whether grid point i has a value. Out-of-range indexes return
// false: callers check the point count first, so an out-of-range index can only
// come from a self-contradictory file.
func (b *Bitmap) Present(i int) bool {
	if i < 0 || i >= b.NumPoints() {
		return false
	}
	return b.bits.Bit(i)
}

// NumPoints returns the number of grid points covered (trailing pad bits excluded).
func (b *Bitmap) NumPoints() int { return b.bits.N() - b.UnusedBits }

// Count returns the number of bits set, i.e. how many values section 4 actually encodes.
func (b *Bitmap) Count() int { return b.bits.Popcount(b.NumPoints()) }
