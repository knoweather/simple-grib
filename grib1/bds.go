package grib1

import (
	"fmt"
	"math"

	"github.com/knoweather/simple-grib/internal/octetio"
)

// Section 4 (BDS) flag bits in the high nibble of octet 4 (WMO table 11; bit 1 = MSB).
const (
	bdsFlagSpectral   = 0x80 // bit 1: spherical harmonics
	bdsFlagComplex    = 0x40 // bit 2: complex/second-order packing
	bdsFlagInteger    = 0x20 // bit 3: values represented as integers
	bdsFlagAdditional = 0x10 // bit 4: additional flag bits at octet 14
)

// Data is the parsed header of section 4 (BDS, binary data section).
type Data struct {
	Flags             uint8   // octet 4 high nibble (WMO table 11)
	BinaryScale       int     // octets 5-6: binary scale factor E (sign-magnitude)
	Reference         float64 // octets 7-10: reference value R (IBM float32)
	BitsPerValue      int     // octet 11
	Payload           []byte  // coded data starting at octet 12
	EncodedUnusedBits int     // unused bits declared in octet 4's low nibble (diagnostic: the recomputed count is authoritative)
	PackingName       string  // human-readable packing name (only grid_simple is implemented)
}

// parseBDS decodes the section 4 header. It does not decode values; that needs the
// grid point count and bitmap, see unpack.
//
// Octet 4: per the WMO spec the high nibble holds the table 11 flags and the low
// nibble the number of unused bits at the end of the section. Some GRIBEX-era
// encoders write the two nibbles swapped, so like ecCodes this package does not
// trust the declared nibble: the pad-bit count is recomputed as
// payload length × 8 − values × bitsPerValue, and the declared value is kept only
// as a diagnostic field.
func parseBDS(sec Section) (*Data, error) {
	r := reader{sec.Data}
	if sec.Length < minBDSLen {
		return nil, fmt.Errorf("grib1: section 4 length %d, want >= %d", sec.Length, minBDSLen)
	}
	flagByte, err := r.u8(4)
	if err != nil {
		return nil, err
	}
	d := &Data{
		Flags:             flagByte & 0xF0,
		EncodedUnusedBits: int(flagByte & 0x0F),
	}

	if d.Flags != 0 {
		return nil, fmt.Errorf("grib1: section 4: unsupported data flags %#02x (%s); only plain grid-point data with simple packing is implemented",
			d.Flags, describeDataFlags(d.Flags))
	}

	if d.BinaryScale, err = r.smag16(5); err != nil {
		return nil, err
	}
	if d.Reference, err = r.ibm32(7); err != nil {
		return nil, err
	}
	bits, err := r.u8(11)
	if err != nil {
		return nil, err
	}
	d.BitsPerValue = int(bits)
	if d.BitsPerValue > 32 {
		return nil, fmt.Errorf("grib1: section 4: bitsPerValue %d out of range [0,32]", d.BitsPerValue)
	}
	if d.Payload, err = r.slice(12, sec.Length-11); err != nil {
		return nil, err
	}
	d.PackingName = "grid_simple"
	return d, nil
}

// describeDataFlags renders the flag bits as readable text for error messages, so
// the caller sees which unsupported feature (spherical harmonics, second-order
// packing,...) the file requested instead of a bare "unsupported".
func describeDataFlags(flags uint8) string {
	var parts []string
	if flags&bdsFlagSpectral != 0 {
		parts = append(parts, "spherical harmonics")
	}
	if flags&bdsFlagComplex != 0 {
		parts = append(parts, "complex/second-order packing")
	}
	if flags&bdsFlagInteger != 0 {
		parts = append(parts, "integer values")
	}
	if flags&bdsFlagAdditional != 0 {
		parts = append(parts, "additional flags at octet 14")
	}
	if len(parts) == 0 {
		return "none"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += ", " + p
	}
	return out
}

// numCodedValues computes how many values are encoded in section 4:
//
//	numCoded = (payload bits − unused bits) / bitsPerValue
//
// The payload bit count must cover the whole payload, including the byte appended
// to make the section length even (GRIB1 pads section lengths to a multiple of 2;
// see ecCodes DataG1SimplePacking). Those 8 extra bits fall inside the unused-bit
// count and cancel out, so both sides of the subtraction must use the same ruler.
// For example an ECMWF field with bitsPerValue 12 gives
// (61371×8 − 12)/12 = 40913, exactly the grid's 251×163 points.
//
// bitsPerValue == 0 is legal (constant field): no bits are encoded and the count is
// fixed by the grid or bitmap; -1 signals "count determined externally".
func (d *Data) numCodedValues() (int, error) {
	if d.BitsPerValue == 0 {
		return -1, nil
	}
	bits := len(d.Payload)*8 - d.EncodedUnusedBits
	if bits < 0 {
		return 0, fmt.Errorf("grib1: section 4: declares %d unused bits but the payload is only %d bits",
			d.EncodedUnusedBits, len(d.Payload)*8)
	}
	if bits%d.BitsPerValue != 0 {
		return 0, fmt.Errorf("grib1: section 4: payload is %d bits minus %d unused = %d, not a multiple of bitsPerValue %d",
			len(d.Payload)*8, d.EncodedUnusedBits, bits, d.BitsPerValue)
	}
	return bits / d.BitsPerValue, nil
}

// unpack reads n coded values from the simple-packed bit stream (bitmap not
// expanded) and converts each X to a physical value with the section 4 formula:
//
//	Y = (R + X · 2^E) · 10^(−D)
//
// where R is the IBM float32 reference value, E the binary scale factor and D the
// decimal scale factor from the PDS.
func (d *Data) unpack(n int, decimalScale int) ([]float64, error) {
	out := make([]float64, n)
	if d.BitsPerValue == 0 {
		// constant field: no X values, every point equals R·10^(−D).
		v := d.Reference * math.Pow(10, float64(-decimalScale))
		for i := range out {
			out[i] = v
		}
		return out, nil
	}

	// Hoist the scale factors out of the bit-stream loop to avoid a Pow call per point.
	binScale := math.Pow(2, float64(d.BinaryScale))
	decScale := math.Pow(10, float64(-decimalScale))

	br := octetio.NewBitReader(d.Payload)
	for i := 0; i < n; i++ {
		x, err := br.ReadU(d.BitsPerValue)
		if err != nil {
			return nil, fmt.Errorf("grib1: section 4: reading coded value %d of %d: %w", i, n, err)
		}
		out[i] = (d.Reference + float64(x)*binScale) * decScale
	}
	return out, nil
}
