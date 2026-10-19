// Package gribw encodes GRIB2 messages.
//
// Scope: message framing plus section assembly. Section field offsets come
// from the same vendored spec definitions the reader walks (spec.Template —
// there are no hard-coded layout constants outside parts with no template in
// the WMO tables), and the assembly order is explicit:
//
//	sections 1/3/4/5/7 assembled → payload injected → section 0 backfilled last
//
// Section 0's total length is an 8-byte U64 covering the whole message,
// including the bare "7777" end-section tail every real-world message ends
// with (the reader accepts it as that section's ≤4-byte trailer). Section 2
// (local use) is never emitted. Section 6 is always emitted with bitmap
// indicator 255 (no bitmap): every real file carries one, and eccodes'
// grib_get_data needs the derived missingValuesPresent key, which requires the
// section to exist. Bitmaps are not supported, so 255 is the only honest value.
package gribw

import (
	"encoding/binary"
	"fmt"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/grid"
)

// endSection is the bare "7777" tail appended after section 7; it is counted in
// section 0's total length.
var endSection = []byte{0x37, 0x37, 0x37, 0x37}

// noBitmapSection is the section 6 always emitted: indicator 255 (no bitmap).
// See the package comment for why it must exist.
var noBitmapSection = []byte{0, 0, 0, 6, 6, 255}

// Message is a builder for a single GRIB2 message. The setters are
// order-independent; Build validates everything and fails loud on any missing
// or contradictory part.
type Message struct {
	discipline int
	id         *grib.Identification
	gridDef    *grid.Definition
	sec3       []byte // full section 3 bytes (octet 1 based, already validated)
	prod       *grib.Product
	sec5       []byte // full section 5 bytes (validated header)
	payload    []byte // section 7 data (from octet 6); may be empty (bits=0 constant field)
	hasPayload bool
}

// New returns an empty message builder (discipline 0).
func New() *Message { return &Message{} }

// SetDiscipline sets the WMO discipline code (section 0 octet 7, table 0.0).
func (m *Message) SetDiscipline(d int) error {
	if d < 0 || d > 255 {
		return fmt.Errorf("gribw: discipline %d out of range [0,255]", d)
	}
	m.discipline = d
	return nil
}

// SetIdentification sets section 1 from an already-parsed (or hand-built)
// reader Identification. The fixed part is encoded by encodeSec1; an optional
// calendar definition is emitted as calendar template 1.0/1.2 (typeOfCalendar
// at octet 24, table 1.6).
func (m *Message) SetIdentification(id *grib.Identification) error {
	if id == nil {
		return fmt.Errorf("gribw: SetIdentification with nil")
	}
	if _, err := encodeSec1(id); err != nil {
		return err
	}
	m.id = id
	return nil
}

// SetGrid re-emits section 3 from an already-parsed grid definition, reusing
// its original section bytes verbatim (lossless clone). The bytes are
// re-validated with grid.New. Grid templates without a vendored spec
// definition (local use) must be injected via SetGridBytes instead.
func (m *Message) SetGrid(g *grid.Definition) error {
	if g == nil {
		return fmt.Errorf("gribw: SetGrid with nil")
	}
	w := g.Walk()
	if w == nil {
		return fmt.Errorf("gribw: grid template %d has no spec definition; use SetGridBytes", g.TemplateNumber())
	}
	return m.SetGridBytes(w.Data())
}

// SetGridBytes injects pre-built section 3 bytes (a complete section, octet 1
// based, with the 4-byte length prefix). They are validated by grid.New and
// retained for the point-count cross-check in Build.
func (m *Message) SetGridBytes(data []byte) error {
	g, err := grid.New(data)
	if err != nil {
		return fmt.Errorf("gribw: section 3: %w", err)
	}
	m.gridDef = g
	m.sec3 = append([]byte(nil), data...)
	return nil
}

// SetProduct sets section 4. Only product template 4.0 is encoded; any other
// template number fails loud. Fields beyond the reader's common core (type of
// time boundary, step range, time interval — octets 13-16) are written as 0.
func (m *Message) SetProduct(p *grib.Product) error {
	if p == nil {
		return fmt.Errorf("gribw: SetProduct with nil")
	}
	if _, err := encodeSec4(p); err != nil {
		return err
	}
	m.prod = p
	return nil
}

// SetDataSection injects a pre-built section 5 header plus the section 7 data
// (octet 6 onward). The pair is produced as separate values by the packing
// encoders (simple packing, IEEE) so tests can golden-compare both halves
// independently. The payload may be empty (a bits=0 constant field).
func (m *Message) SetDataSection(sec5, payload []byte) error {
	if len(sec5) < 11 {
		return fmt.Errorf("gribw: section 5 too short: %d bytes, want >= 11 (header + numberOfValues + template number)", len(sec5))
	}
	if slen := int(binary.BigEndian.Uint32(sec5[0:4])); slen != len(sec5) {
		return fmt.Errorf("gribw: section 5 length field is %d, section has %d bytes", slen, len(sec5))
	}
	if sec5[4] != 5 {
		return fmt.Errorf("gribw: section 5 number octet = %d, want 5", sec5[4])
	}
	m.sec5 = append([]byte(nil), sec5...)
	m.payload = append([]byte(nil), payload...)
	m.hasPayload = true
	return nil
}

// Build assembles the complete message bytes: sections in order
// (1, 3, 4, 5, 6, 7), the bare "7777" tail, and section 0 backfilled last (its
// totalLen is the U64 over everything). Section 6 is always emitted with
// indicator 255 (no bitmap).
func (m *Message) Build() ([]byte, error) {
	if m.id == nil {
		return nil, fmt.Errorf("gribw: SetIdentification required")
	}
	if len(m.sec3) == 0 || m.gridDef == nil {
		return nil, fmt.Errorf("gribw: section 3 required (SetGrid or SetGridBytes)")
	}
	if m.prod == nil {
		return nil, fmt.Errorf("gribw: SetProduct required")
	}
	if !m.hasPayload || len(m.sec5) == 0 {
		return nil, fmt.Errorf("gribw: section 5 + payload required (SetDataSection)")
	}

	numValues := int(binary.BigEndian.Uint32(m.sec5[5:9])) // octets 6-9
	if err := m.checkValueCount(numValues); err != nil {
		return nil, err
	}
	if err := m.checkPayloadSize(numValues); err != nil {
		return nil, err
	}

	sec1, err := encodeSec1(m.id)
	if err != nil {
		return nil, err
	}
	sec4, err := encodeSec4(m.prod)
	if err != nil {
		return nil, err
	}
	sec7 := make([]byte, 5+len(m.payload))
	binary.BigEndian.PutUint32(sec7[0:4], uint32(len(sec7)))
	sec7[4] = 7
	copy(sec7[5:], m.payload)

	total := 16 + len(sec1) + len(m.sec3) + len(sec4) + len(m.sec5) + len(noBitmapSection) + len(sec7) + len(endSection)
	out := make([]byte, 0, total)
	out = append(out, make([]byte, 16)...) // section 0 placeholder, backfilled below
	out = append(out, sec1...)
	out = append(out, m.sec3...)
	out = append(out, sec4...)
	out = append(out, m.sec5...)
	out = append(out, noBitmapSection...)
	out = append(out, sec7...)
	out = append(out, endSection...)

	// Section 0 backfilled last (assembly order: sections → payload → totalLen):
	// "GRIB" + reserved FF FF + discipline + edition 2 + U64 total length.
	copy(out[0:4], "GRIB")
	out[4], out[5] = 0xFF, 0xFF
	out[6] = byte(m.discipline)
	out[7] = 2
	binary.BigEndian.PutUint64(out[8:16], uint64(len(out)))
	return out, nil
}

// checkValueCount cross-checks section 5's numberOfValues against the grid.
// No bitmap is emitted, so the two must be equal. The grid-computed count is
// authoritative: a grid the reader cannot turn into coordinates is rejected
// rather than written out, so the writer never emits a file the reader would
// refuse.
func (m *Message) checkValueCount(numValues int) error {
	gp := m.gridDef.NumPoints()
	if gp == 0 {
		return fmt.Errorf("gribw: cannot verify numberOfValues %d: grid template %d has no computable point count (unimplemented or metadata-only template)",
			numValues, m.gridDef.TemplateNumber())
	}
	if gp != numValues {
		return fmt.Errorf("gribw: section 5 numberOfValues %d != grid point count %d ( emits no bitmap)", numValues, gp)
	}
	return nil
}

// checkPayloadSize validates the section 7 data length against the data
// representation template. Only template 5.0 simple packing and 5.4 IEEE
// precision=1 are supported. Section 5 is bounds-checked against each
// template's fixed header, so a truncated section is rejected here instead of
// indexing out of range.
func (m *Message) checkPayloadSize(numValues int) error {
	tmplNo := int(binary.BigEndian.Uint16(m.sec5[9:11])) // octets 10-11
	switch tmplNo {
	case 0:
		if len(m.sec5) < 20 { // bitsPerValue 在 octet 20（规范模板 5.0）
			return fmt.Errorf("gribw: section 5 template 5.0 needs at least 20 bytes (bitsPerValue at octet 20), section has %d", len(m.sec5))
		}
		bits := int(m.sec5[19]) // octet 20
		if bits > 32 {          // WMO 表 5.5 定义 0..32
			return fmt.Errorf("gribw: section 5 template 5.0 bitsPerValue %d exceeds the WMO maximum 32", bits)
		}
		want := (int64(numValues)*int64(bits) + 7) / 8
		if int64(len(m.payload)) != want {
			return fmt.Errorf("gribw: section 7 has %d bytes, simple packing with %d values × %d bits needs %d",
				len(m.payload), numValues, bits, want)
		}
	case 4:
		if len(m.sec5) < 12 { // precision 在 octet 12（规范模板 5.4）
			return fmt.Errorf("gribw: section 5 template 5.4 needs at least 12 bytes (precision at octet 12), section has %d", len(m.sec5))
		}
		if prec := m.sec5[11]; prec != 1 { // octet 12，表 5.7
			return fmt.Errorf("gribw: IEEE precision %d not supported by the writer ", prec)
		}
		want := int64(numValues) * 4
		if int64(len(m.payload)) != want {
			return fmt.Errorf("gribw: section 7 has %d bytes, IEEE precision 1 with %d values needs %d",
				len(m.payload), numValues, want)
		}
	default:
		return fmt.Errorf("gribw: payload size verification for data representation template %d not implemented in (supported: 0 simple packing, 4 IEEE precision=1)", tmplNo)
	}
	return nil
}

// File builds each message and concatenates them into a GRIB2 file body.
// Messages are self-delimiting through section 0's total length; no separator
// is needed (the reader splits on it). At least one message is required: an
// empty file body is rejected by the reader.
func File(msgs ...*Message) ([]byte, error) {
	if len(msgs) == 0 {
		return nil, fmt.Errorf("gribw: File requires at least one message")
	}
	var out []byte
	for i, m := range msgs {
		if m == nil {
			return nil, fmt.Errorf("gribw: message %d is nil", i+1)
		}
		b, err := m.Build()
		if err != nil {
			return nil, fmt.Errorf("gribw: message %d: %w", i+1, err)
		}
		out = append(out, b...)
	}
	return out, nil
}
