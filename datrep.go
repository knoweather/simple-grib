package grib

import (
	"fmt"

	"github.com/knoweather/simple-grib/grid"
	"github.com/knoweather/simple-grib/internal/octetio"
	"github.com/knoweather/simple-grib/pack"
	"github.com/knoweather/simple-grib/spec"
)

// DataRep is the parsed section 5: the expected value count and the data
// representation template number, plus (for WMO-defined templates) a Walk
// over the template fields. Fields is nil for local/unknown templates; in
// that case Values() fails in the pack layer with a clear error.
type DataRep struct {
	TemplateNumber int // octets 10-11
	NumValues      int // octets 6-9: expected output length after the bitmap
	Fields         *spec.Walk
}

// DataRep parses section 5 (missing → *ErrSectionAbsent). A section too short
// to carry its fixed header (numberOfValues at octets 6-9, the template
// number at octets 10-11) is rejected here instead of silently yielding zero
// values (bug.md §2.4).
func (m *Message) DataRep() (*DataRep, error) {
	if m.g1 != nil {
		return dataRepFromGRIB1(m.g1), nil
	}
	sec, err := m.Section(5)
	if err != nil {
		return nil, err
	}
	r := octetio.New(sec.Data)
	nv, err := r.U32(5) // octets 6-9
	if err != nil {
		return nil, fmt.Errorf("grib: section 5 too short for numberOfValues (octets 6-9): %w", err)
	}
	tmplNo, err := r.U16(9) // octets 10-11
	if err != nil {
		return nil, fmt.Errorf("grib: section 5 too short for the data representation template number (octets 10-11): %w", err)
	}

	dr := &DataRep{TemplateNumber: int(tmplNo), NumValues: int(nv)}
	if def, ok := spec.Template(5, int(tmplNo)); ok {
		w, err := spec.NewWalk(def, sec.Data)
		if err != nil {
			return nil, err
		}
		dr.Fields = w
	}
	return dr, nil
}

// ErrUnsupportedBitmap reports a section 6 bitmap indicator the reader cannot
// handle: indicators 1-253 (a bitmap predetermined by the centre, not present
// in the section) and 254 (bitmap defined earlier in the same message). Only
// indicator 0 (bitmap right here) and 255 (no bitmap) are supported.
type ErrUnsupportedBitmap struct{ Indicator int }

func (e *ErrUnsupportedBitmap) Error() string {
	return fmt.Sprintf("grib: section 6: unsupported bitmap indicator %d (only 0 = bitmap in this section and 255 = no bitmap are supported)", e.Indicator)
}

// Values decodes all data values of the message in scan order. It ties
// together the grid point count, the section 5 value count, an optional
// section 6 bitmap, and the pack registry:
//
//   - the grid point count comes from the section 3 definition;
//   - with a bitmap (indicator 0), its popcount over the first GridPoints
//     bits must equal NumValues, otherwise it is an error;
//   - without a bitmap (no section 6, or indicator 255), NumValues must equal
//     the grid point count, otherwise it is an error;
//   - the section 7 payload (from octet 6) is handed to pack.Unpack.
//
// Messages that carry the local-use WebP image template (5.49152) have no
// numeric values: Values returns *pack.ErrNotNumeric — use WebP, WebPInfo or
// WebPImage instead.
func (m *Message) Values() ([]float32, error) {
	if m.g1 != nil {
		return m.g1.Values()
	}
	g, err := m.Grid()
	if err != nil {
		return nil, err
	}
	dr, err := m.DataRep()
	if err != nil {
		return nil, err
	}
	if dr.TemplateNumber == pack.TemplateWebP {
		// 5.49152 是图像载荷：像素不是 GRIB 数值。早于 numberOfValues/网格
		// 交叉校验返回，给出可操作的错误（图像走 Message.WebP/WebPImage）。
		// 5.49152 carries an image: pixels are not GRIB values. Return before
		// the numberOfValues/grid cross-check so the error is actionable (read
		// the image through Message.WebP/WebPImage instead).
		return nil, &pack.ErrNotNumeric{Template: dr.TemplateNumber, Format: "WebP image"}
	}
	gridPoints := g.NumPoints()
	numValues := dr.NumValues

	// Cross-check the authoritative declared point count (section 3 octets
	// 7-10, added by WMO EC-2025 amendments) against the grid-derived size.
	if declared := g.NumPointsDeclared(); declared > 0 && declared != gridPoints {
		return nil, fmt.Errorf("grib: section 3 declares %d data points but the grid definition computes %d",
			declared, gridPoints)
	}

	var bitmap *octetio.Bits
	if sec6, ok := m.byNum[6]; ok {
		ind, err := octetio.New(sec6.Data).U8(5) // octet 6：指示符（表 6.0）
		if err != nil {
			return nil, fmt.Errorf("grib: section 6 too short for the bitmap indicator: %w", err)
		}
		switch {
		case ind == 0:
			bitmap = octetio.NewBits(sec6.Data[6:]) // octets 7+
			if got := bitmap.Popcount(gridPoints); got != numValues {
				return nil, fmt.Errorf("grib: section 6: bitmap popcount over %d grid points is %d, want numberOfValues %d",
					gridPoints, got, numValues)
			}
		case ind == 255:
			// No bitmap applies; nothing to validate here.
		default:
			return nil, &ErrUnsupportedBitmap{Indicator: int(ind)}
		}
	}
	if bitmap == nil && numValues != gridPoints {
		return nil, fmt.Errorf("grib: numberOfValues %d != grid point count %d (no bitmap to reconcile)",
			numValues, gridPoints)
	}

	sec7, err := m.Section(7)
	if err != nil {
		return nil, err
	}
	ctx := &pack.Context{
		TemplateNumber: dr.TemplateNumber,
		Fields:         dr.Fields,
		Data:           sec7.Data[5:], // octet 6 起：载荷
		NumValues:      numValues,
		Bitmap:         bitmap,
		GridPoints:     gridPoints,
	}
	vals, err := pack.Unpack(ctx)
	if err != nil && dr.Fields == nil {
		// No spec definition and no registered unpacker: an unknown
		// (unregistered local-use) template — fail with a structured error
		// carrying the registration hint (ErrUnknownTemplate).
		return nil, &ErrUnknownTemplate{Section: 5, Number: dr.TemplateNumber}
	}
	return vals, err
}

// Grid parses the section 3 grid definition (delegated to the grid package).
func (m *Message) Grid() (*grid.Definition, error) {
	if m.g1 != nil {
		return m.g1.Grid()
	}
	sec, err := m.Section(3)
	if err != nil {
		return nil, err
	}
	return grid.New(sec.Data)
}

// Values64 returns the data values at float64 precision.
//
// For GRIB1 this is native precision: the reference value is an IBM float
// (24-bit mantissa) and the formula (R + X·2^E)·10^(−D) is evaluated in
// float64 — a direct downcast to float32 would lose significant digits. For
// GRIB2 the values are already IEEE binary32; widening them here neither adds
// nor removes precision (use Values for native precision).
func (m *Message) Values64() ([]float64, error) {
	if m.g1 != nil {
		return m.g1.Values64()
	}
	v, err := m.Values()
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = float64(x)
	}
	return out, nil
}
