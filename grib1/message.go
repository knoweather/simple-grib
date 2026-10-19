package grib1

import (
	"fmt"
	"math"

	"github.com/knoweather/simple-grib/grid"
)

// Message is a parsed GRIB1 message: raw bytes, validated sections, and the parsed
// PDS, bitmap and data-representation headers.
//
// Relation to grib.Message in the root package: grib.Message is the unified facade
// that dispatches by edition to this or the GRIB2 parsing path. This type describes
// GRIB1 only and does no edition checks.
//
// Concurrency: every field is read-only after construction, so a Message can be
// shared between goroutines. Grid()/Values() re-parse on each call (same policy as
// the GRIB2 side), so no locking is needed.
type Message struct {
	index int
	off   int64
	size  int
	data  []byte
	secs  []Section
	byNum map[int]*Section

	id   *Identification
	prod *Product
	gds  *Section
	bms  *Bitmap
	bds  *Data
}

// Parse parses exactly one complete GRIB1 message (data starts at section 0 octet 1,
// and its length must equal the total length declared there). index and off are
// caller-supplied location information, returned unchanged.
//
// Consistent with the rest of the library: any structural contradiction returns a
// contextual error, never a panic.
func Parse(index int, off int64, data []byte) (*Message, error) {
	secs, err := splitSections(data)
	if err != nil {
		return nil, err
	}
	m := &Message{
		index: index,
		off:   off,
		size:  len(data),
		data:  data,
		secs:  secs,
		byNum: make(map[int]*Section, len(secs)),
	}
	for i := range m.secs {
		m.byNum[m.secs[i].Number] = &m.secs[i]
	}

	pds, ok := m.byNum[SectionPDS]
	if !ok {
		return nil, fmt.Errorf("grib1: message %d: section 1 (PDS) absent", index)
	}
	m.id, m.prod, err = parsePDS(*pds)
	if err != nil {
		return nil, fmt.Errorf("grib1: message %d: %w", index, err)
	}

	if s, ok := m.byNum[SectionGDS]; ok {
		m.gds = s
		// The grid definition is parsed once here: it determines the point count,
		// which must be known before section 4 can be validated. The result serves
		// only this check; the real Definition is rebuilt by Grid() on every call
		// (same as GRIB2).
		if _, err := grid.NewGRIB1(s.Data); err != nil {
			return nil, fmt.Errorf("grib1: message %d: %w", index, err)
		}
	}

	if s, ok := m.byNum[SectionBMS]; ok {
		m.bms, err = parseBMS(*s)
		if err != nil {
			return nil, fmt.Errorf("grib1: message %d: %w", index, err)
		}
	}

	bds, ok := m.byNum[SectionBDS]
	if !ok {
		return nil, fmt.Errorf("grib1: message %d: section 4 (BDS) absent", index)
	}
	m.bds, err = parseBDS(*bds)
	if err != nil {
		return nil, fmt.Errorf("grib1: message %d: %w", index, err)
	}
	return m, nil
}

// Index is the 0-based position of this message in the file.
func (m *Message) Index() int { return m.index }

// Offset is the byte offset of the message start within the file.
func (m *Message) Offset() int64 { return m.off }

// Size is the total message length in bytes (from section 0 octets 5-7).
func (m *Message) Size() int { return m.size }

// Edition is always 1.
func (m *Message) Edition() int { return Edition }

// Identification returns the "who and when" part of section 1.
func (m *Message) Identification() *Identification { return m.id }

// Product returns the "what field, which level, which time range" part of section 1.
func (m *Message) Product() *Product { return m.prod }

// DataRep returns the section 4 data header (scale factors, reference value, bits
// per value, payload).
func (m *Message) DataRep() *Data { return m.bds }

// CodedValues reports how many values section 4 actually encodes (bitmap not
// expanded); constant fields (bitsPerValue 0) return -1, meaning the count is fixed
// by the grid or bitmap. The root package surfaces this as DataRep.NumValues.
func (m *Message) CodedValues() (int, error) { return m.bds.numCodedValues() }

// Bitmap returns the section 3 bitmap; nil when there is no bitmap section.
func (m *Message) Bitmap() *Bitmap { return m.bms }

// HasGDS reports whether the message carries a grid description section. GRIB1
// allows omitting the GDS (the grid is then identified by PDS octet 7 pointing at a
// predefined grid catalogue); without one this package cannot produce coordinates,
// and Grid() fails loudly.
func (m *Message) HasGDS() bool { return m.gds != nil }

// Sections lists the section numbers that actually occur, in message order
// (0=IS 1=PDS 2=GDS 3=BMS 4=BDS 5=end marker).
func (m *Message) Sections() []int {
	out := make([]int, len(m.secs))
	for i := range m.secs {
		out[i] = m.secs[i].Number
	}
	return out
}

// RawSections returns the raw section slice (the root package converts it to its
// own Section type so Sections()/Section(n) behave identically for both editions).
// The slice aliases internal state; treat it as read-only.
func (m *Message) RawSections() []Section { return m.secs }

// Section returns the section with the given number, or an error if absent.
func (m *Message) Section(n int) (*Section, error) {
	s, ok := m.byNum[n]
	if !ok {
		return nil, fmt.Errorf("grib1: section %d absent", n)
	}
	return s, nil
}

// Grid parses the section 2 grid definition (delegated to the grid package, shared
// with GRIB2).
func (m *Message) Grid() (*grid.Definition, error) {
	if m.gds == nil {
		return nil, fmt.Errorf("grib1: message %d: no section 2 (GDS); the grid is identified by PDS octet 7 = %d, which needs a predefined grid catalogue this library does not have",
			m.index, m.id.GridID)
	}
	return grid.NewGRIB1(m.gds.Data)
}

// Values64 decodes all data values in scan order as float64 (GRIB1's native route:
// the reference value is IBM float32 and the value formula is evaluated in float64).
//
// When a bitmap (section 3) exists, the returned slice has one entry per grid point
// with NaN at missing points — matching the GRIB2 Values() behavior.
func (m *Message) Values64() ([]float64, error) {
	g, err := m.Grid()
	if err != nil {
		return nil, err
	}
	numPoints := g.NumPoints()
	if numPoints == 0 {
		return nil, fmt.Errorf("grib1: message %d: grid template %d yields no point count, cannot size the data",
			m.index, g.TemplateNumber())
	}

	// How many values section 4 encodes: derived from the payload when present;
	// constant fields (bitsPerValue 0) take the count from the grid or bitmap.
	n, err := m.bds.numCodedValues()
	if err != nil {
		return nil, fmt.Errorf("grib1: message %d: %w", m.index, err)
	}
	constant := n < 0

	var expect int
	if m.bms != nil {
		if m.bms.NumPoints() != numPoints {
			return nil, fmt.Errorf("grib1: message %d: bitmap covers %d points but the grid has %d",
				m.index, m.bms.NumPoints(), numPoints)
		}
		expect = m.bms.Count()
	} else {
		expect = numPoints
	}
	if constant {
		n = expect
	} else if n != expect {
		return nil, fmt.Errorf("grib1: message %d: section 4 encodes %d values but %s expects %d",
			m.index, n, map[bool]string{true: "the bitmap", false: "the grid"}[m.bms != nil], expect)
	}

	coded, err := m.bds.unpack(n, m.prod.DecimalScaleFactor)
	if err != nil {
		return nil, fmt.Errorf("grib1: message %d: %w", m.index, err)
	}
	if m.bms == nil {
		return coded, nil
	}

	// Expand the bitmap: present points take the coded values in order, missing
	// points get NaN.
	out := make([]float64, numPoints)
	k := 0
	for i := 0; i < numPoints; i++ {
		if m.bms.Present(i) {
			out[i] = coded[k]
			k++
		} else {
			out[i] = math.NaN()
		}
	}
	return out, nil
}

// Values is the float32 view of Values64, kept for API parity with the existing
// grib.Message.Values. Use Values64 when GRIB1's native precision matters.
func (m *Message) Values() ([]float32, error) {
	v, err := m.Values64()
	if err != nil {
		return nil, err
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out, nil
}
