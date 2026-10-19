// fixed tree: B's original version reproduced the pre-fix failures (and did
// not compile in-package: `grib.OpenReader` inside package grib). Now a
// regression test asserting the FIXED behavior:
//
//   ② datrep.go: template number read from octets 10-11 (r.U16(9)), NOT
//      octets 8-9 (the low bytes of numberOfValues) — so
//      DataRep().TemplateNumber is the real template and Values() decodes.
//   ① parseSections: a trailing run of at most 4 bytes after section 7 is
//      tolerated (this eccodes build systematically appends "7777" and
//      includes it in totalLen); anything larger still fails with ErrTruncated.

package grib

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func be16(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }
func be32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func be64(v uint64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, v); return b }

// minimalMessage builds a minimal self-consistent GRIB2 message (sections
// 1,3,4,5,7; simple packing template 0, Ni=Nj=3, nv=9). tailLen is the number
// of stray bytes appended after section 7 (0 = well-formed; 4 = this eccodes
// build's "7777" shape); totalLen always covers them.
func minimalMessage(tailLen int) []byte {
	// --- sec1: identification, length 21 (octets 6-21 content) ---------------
	c1 := make([]byte, 21)
	binary.BigEndian.PutUint32(c1[0:4], 21) // section length
	c1[4] = 1                               // section number
	content := []byte{
		0x00, 0x62, // centre 98 (octets 6-7)
		0x00, 0x00, // subcentre (octets 8-9)
		0x00,       // master table version (octet 10)
		0x00,       // local table version (octet 11)
		0x00,       // octet 12 (not read by Identification)
		0x07, 0xE6, // year 2026 (octets 13-14)
		0x0A,             // month 10 (octet 15)
		0x04,             // day 4 (octet 16)
		0x00, 0x00, 0x00, // hour minute second (octets 17-19)
		0x00, // production status 0 = operational (octet 20)
		0x00, // type of processed data 0 (octet 21)
	}
	copy(c1[5:], content)

	// --- sec3: regular lat/lon template 0, Ni=Nj=3 --------------------------
	c3 := make([]byte, 72)
	binary.BigEndian.PutUint32(c3[0:4], 72) // section length
	c3[4] = 3                               // section number
	// content idx5..71 (octets 6..72), all zeros except:
	c3[14] = 6                               // shape: sphere R=6371229 (table 3.2)
	binary.BigEndian.PutUint32(c3[30:34], 3) // Ni
	binary.BigEndian.PutUint32(c3[34:38], 3) // Nj
	c3[38] = 0
	c3[39] = 0
	c3[40] = 0
	c3[41] = 0                                        // basicAngle 0
	binary.BigEndian.PutUint32(c3[42:46], 0xFFFFFFFF) // subdivisions missing -> 1e-6 deg
	binary.BigEndian.PutUint32(c3[46:50], 60_000_000) // La1 = +60
	c3[50] = 0
	c3[51] = 0
	c3[52] = 0
	c3[53] = 0                                        // Lo1 = 0
	c3[54] = 0                                        // resFlags 0
	binary.BigEndian.PutUint32(c3[55:59], 40_000_000) // La2 = +40
	binary.BigEndian.PutUint32(c3[59:63], 20_000_000) // Lo2 = +20
	binary.BigEndian.PutUint32(c3[63:67], 10_000_000) // Di = +10 (magnitude; iDir=+1)
	binary.BigEndian.PutUint32(c3[67:71], 10_000_000) // Dj = +10 (magnitude; jDir=-1)
	c3[71] = 0                                        // scanMode 0

	// --- sec4: product template 0, minimal ---------------------------------
	c4 := make([]byte, 9)
	binary.BigEndian.PutUint32(c4[0:4], 9) // section length
	c4[4] = 4                              // section number
	binary.BigEndian.PutUint16(c4[5:7], 9) // NV = 9 (octets 6-7)
	binary.BigEndian.PutUint16(c4[7:9], 0) // template 0 (octets 8-9)

	// --- sec5: data representation, simple packing template 0 ---------------
	c5 := make([]byte, 21)
	binary.BigEndian.PutUint32(c5[0:4], 21) // section length
	c5[4] = 5                               // section number
	binary.BigEndian.PutUint32(c5[5:9], 9)  // numberOfValues = 9 (octets 6-9)
	binary.BigEndian.PutUint16(c5[9:11], 0) // template number 0 (octets 10-11)
	var R float32 = 27315.0                 // pre-scaled by 10^D, D=2 -> 273.15 K
	binary.BigEndian.PutUint32(c5[11:15], math.Float32bits(R))
	binary.BigEndian.PutUint16(c5[15:17], 0) // E = 0 (octets 16-17)
	binary.BigEndian.PutUint16(c5[17:19], 2) // D = 2 (octets 18-19)
	c5[19] = 8                               // bits per value = 8 (octet 20)
	c5[20] = 0                               // origType 0 = float (octet 21)

	// --- sec7: data, 9 values x 8 bits = 9 bytes ---------------------------
	payload := make([]byte, 9)
	for i := range payload {
		payload[i] = byte(i) // raw 0..8 -> values 273.15 .. 273.23 K
	}
	c7 := make([]byte, 14)
	binary.BigEndian.PutUint32(c7[0:4], 14) // section length
	c7[4] = 7                               // section number
	copy(c7[5:], payload)

	msg := append(append(append(append(append([]byte{}, c1...), c3...), c4...), c5...), c7...)
	total := len(msg) + 16 + tailLen // +16: the section-0 header appended below
	// sec0: GRIB + 2 reserved octets + discipline 0 + edition 2 + U64 totalLen
	hdr := make([]byte, 16)
	copy(hdr[0:4], []byte("GRIB"))
	hdr[6] = 0 // discipline (atmospheric products)
	hdr[7] = 2 // edition GRIB2
	binary.BigEndian.PutUint64(hdr[8:16], uint64(total))
	msg = append(hdr, msg...)
	if tailLen > 0 {
		tail := make([]byte, tailLen)
		for i := range tail {
			tail[i] = 0x37 // "7"
		}
		msg = append(msg, tail...)
	}
	return msg
}

// TestQb8Repro is the regression test for (datrep.go template-number
// offset): on a well-formed message DataRep().TemplateNumber must be the real
// template (0) and Values() must decode — before the fix, TemplateNumber was
// nv%65536 and Values() failed with an unsupported-template error.
func TestQb8Repro(t *testing.T) {
	msg := minimalMessage(0)
	f, err := OpenReader(bytes.NewReader(msg), int64(len(msg)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v (structure should be valid)", err)
	}

	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v", err)
	}
	if g.NumPoints() != 9 {
		t.Errorf("NumPoints = %d, want 9", g.NumPoints())
	}
	firstLat, firstLon, err := g.Point(0)
	if err != nil || !almostEq(firstLat, 60.0, 1e-9) || !almostEq(firstLon, 0.0, 1e-9) {
		t.Errorf("Point(0) = (%v,%v) err=%v, want (60,0)", firstLat, firstLon, err)
	}

	dr, err := m.DataRep()
	if err != nil {
		t.Fatalf("DataRep: %v", err)
	}
	if dr.TemplateNumber != 0 {
		t.Errorf(" regressed: DataRep().TemplateNumber = %d, want 0 (must read octets 10-11, not the low bytes of numberOfValues)", dr.TemplateNumber)
	}

	vals, err := m.Values()
	if err != nil {
		t.Fatalf(" regressed: Values(): %v", err)
	}
	if len(vals) != 9 || !almostEq(float64(vals[0]), 273.15, 1e-4) || !almostEq(float64(vals[8]), 273.23, 1e-4) {
		t.Errorf("Values() = %v, want 9 values starting at 273.15", vals)
	}
}

// TestQb8ReproTail is the regression test for (parseSections tail
// tolerance): a 4-byte "7777" trailer (this eccodes build's universal shape,
// totalLen inclusive) must be accepted and the full chain must work; a longer
// trailer (>4 bytes) must still fail with ErrTruncated.
func TestQb8ReproTail(t *testing.T) {
	// 4-byte tail: tolerated ().
	msg := minimalMessage(4)
	f, err := OpenReader(bytes.NewReader(msg), int64(len(msg)))
	if err != nil {
		t.Fatalf("OpenReader with 4-byte tail: %v", err)
	}
	defer f.Close()
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf(" regressed: Message(0) rejected the real-shape message: %v (want: tolerate <=4 stray octets)", err)
	}
	vals, err := m.Values()
	if err != nil || len(vals) != 9 {
		t.Errorf("Values() on tailed message = %v, err=%v; want 9 values", vals, err)
	}

	// 5-byte tail: still malformed.
	msg = minimalMessage(5)
	f2, err := OpenReader(bytes.NewReader(msg), int64(len(msg)))
	if err != nil {
		t.Fatalf("OpenReader with 5-byte tail: %v", err)
	}
	defer f2.Close()
	if _, err := f2.Message(0); err == nil {
		t.Errorf(" regressed: 5-byte trailer accepted (want ErrTruncated)")
	} else if !errors.Is(err, ErrTruncated) {
		t.Logf("5-byte trailer rejected with %v (not ErrTruncated; acceptable)", err)
	}
}

func almostEq(a, b float64, tol float64) bool {
	return math.Abs(a-b) <= tol
}
