package grib1

import (
	"fmt"

	"github.com/knoweather/simple-grib/internal/octetio"
)

// reader is the GRIB1 byte reader.
//
// Key convention: every offset parameter is a WMO octet number (1-based), matching
// the spec documents verbatim. This is deliberate — the pitfalls in GRIB1 are
// almost all offset bugs, so it matters more that the code mirrors the spec (and
// can be checked line by line against the WMO tables) than that one subtraction is
// saved.
//
// Scalar decoding itself (IBM float, sign-magnitude) lives in internal/octetio and
// is shared with other packages; this type only fetches bytes by octet number with
// bounds checks.
type reader struct{ b []byte }

// at returns an n-byte view starting at the given octet; out of range yields an
// error that names the requested octets.
func (r reader) at(octet, n int) ([]byte, error) {
	i := octet - 1
	if i < 0 || n < 0 || i+n > len(r.b) {
		return nil, fmt.Errorf("grib1: need octets %d-%d, section has %d bytes", octet, octet+n-1, len(r.b))
	}
	return r.b[i : i+n], nil
}

// u8 reads one unsigned byte (a single octet).
func (r reader) u8(octet int) (uint8, error) {
	b, err := r.at(octet, 1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// u16 reads a 2-byte unsigned integer (big endian).
func (r reader) u16(octet int) (uint16, error) {
	b, err := r.at(octet, 2)
	if err != nil {
		return 0, err
	}
	return uint16(b[0])<<8 | uint16(b[1]), nil
}

// u24 reads a 3-byte unsigned integer (big endian). GRIB1 uses it for section
// lengths and Di/Dj.
func (r reader) u24(octet int) (uint32, error) {
	b, err := r.at(octet, 3)
	if err != nil {
		return 0, err
	}
	return octetio.U24(b), nil
}

// smag16 reads a 2-byte sign-magnitude signed integer (GRIB1 scale factors).
func (r reader) smag16(octet int) (int, error) {
	b, err := r.at(octet, 2)
	if err != nil {
		return 0, err
	}
	return octetio.Smag16(b), nil
}

// ibm32 reads a 4-byte IBM single-precision float (GRIB1 reference values).
func (r reader) ibm32(octet int) (float64, error) {
	b, err := r.at(octet, 4)
	if err != nil {
		return 0, err
	}
	return octetio.IBMFloat32(b), nil
}

// slice returns n bytes starting at the given octet (a view into the underlying
// array; callers must treat it as read-only).
func (r reader) slice(octet, n int) ([]byte, error) { return r.at(octet, n) }
