// support: structure, metadata, bit-exact values and coordinates vs the
// the Linux bench host eccodes 2.41.0 oracle, plus fail-loud negatives.
//
// FIXTURE: testdata/p7-a-multifield/jma_gpv_0p5deg_p1000.grib2 — real JMA GSM
// GPV product (rjtd, 2025-09-24 00Z, 1000 hPa), one GRIB2 envelope holding
// six data fields (gh/u/v/t/r/w) that share sections 1 and 3:
//
//	s0 | s1 s3 | [s4 s5 s6 s7] x 6 | "7777" (4-byte trailer, in totalLength)
//
// section-0 totalLength = 569501 covers the WHOLE envelope. Before the
// reader rejected it with "message 0: sections out of order (got 4 after 7)".
// eccodes 2.41.0 reads all six fields (grib_ls: "6 of 6 messages"); its file
// scanner reports a single offset for the envelope, so the oracle extracts
// each field from a [s0'][s1][s3][group] slice with totalLength patched to
// the slice length — section contents are verbatim envelope bytes.
// Provenance: extract_oracle.py (same directory), run on the Linux bench host.
//
// MULTI-FIELD VIEW SEMANTICS (A's design decision, ):
//   - NumMessages() counts data fields across all envelopes (6 here);
//   - Message(i) for a multi-field envelope returns the virtual view
//     [s0][s1][s3][group_i] assembled contiguously; Index() is the flat
//     position, Offset() the envelope start, Size() the envelope's declared
//     totalLength (the section-0 field, as always);
//   - classic single-field envelopes keep the legacy whole-body path with
//     byte-for-byte unchanged validation.

package grib

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

const (
	p7aFixture    = "testdata/p7-a-multifield/jma_gpv_0p5deg_p1000.grib2"
	p7aOraclePath = "testdata/p7-a-multifield/oracle.json"
)

// p7aWantSHA is the fixture's sha256 (see SHA256SUMS in the same directory).
var p7aWantSHA = [32]byte{
	0xb2, 0x72, 0x2b, 0xa9, 0x0f, 0x92, 0x5a, 0x16, 0x17, 0xf7, 0x43, 0x15,
	0xd7, 0xec, 0x34, 0x83, 0xf7, 0x01, 0xab, 0x41, 0xfb, 0x6c, 0xba, 0xfd,
	0xe2, 0xea, 0x4b, 0x1b, 0xab, 0x40, 0xbb, 0x05,
}

type p7aOracleField struct {
	Index      int                    `json:"index"`
	SliceBytes int                    `json:"slice_bytes"`
	Meta       map[string]interface{} `json:"meta"`
	NValues    int                    `json:"n_values"`
	ValuesB64  string                 `json:"values_b64"`
	LatsB64    string                 `json:"lats_b64,omitempty"`
	LonsB64    string                 `json:"lons_b64,omitempty"`
}

type p7aOracle struct {
	File      string           `json:"file"`
	SHA256    string           `json:"sha256"`
	SizeBytes int              `json:"size_bytes"`
	NFields   int              `json:"n_fields"`
	Fields    []p7aOracleField `json:"fields"`
}

func loadP7a(t *testing.T) (fixture []byte, oracle p7aOracle) {
	t.Helper()
	raw, err := os.ReadFile(p7aFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sum := sha256.Sum256(raw)
	if sum != p7aWantSHA {
		t.Fatalf("fixture sha256 = %x, want %x (corrupted testdata?)", sum, p7aWantSHA)
	}
	ob, err := os.ReadFile(p7aOraclePath)
	if err != nil {
		t.Fatalf("read oracle: %v", err)
	}
	if err := json.Unmarshal(ob, &oracle); err != nil {
		t.Fatalf("parse oracle: %v", err)
	}
	if oracle.SHA256 != hex.EncodeToString(sum[:]) || oracle.SizeBytes != len(raw) {
		t.Fatalf("oracle does not describe the fixture (sha=%s size=%d, want %x / %d)",
			oracle.SHA256, oracle.SizeBytes, p7aWantSHA, len(raw))
	}
	return raw, oracle
}

// walkP7a mirrors the index-time section walk: returns per-section
// (number, offset, length) triples for the fixture envelope.
func walkP7a(t *testing.T, raw []byte) [][3]int {
	t.Helper()
	var out [][3]int
	pos, end := 16, len(raw)
	for pos < end {
		if end-pos < 5 {
			if end-pos > 4 {
				t.Fatalf("walk: %d trailing bytes > 4", end-pos)
			}
			break
		}
		slen := int(binary.BigEndian.Uint32(raw[pos : pos+4]))
		num := int(raw[pos+4])
		out = append(out, [3]int{num, pos, slen})
		pos += slen
	}
	return out
}

// p7aGroupStarts returns the span indices where data-field groups begin:
// the first section 4 (after the s1/s3 head) and every section 4 that
// directly follows a section 7.
func p7aGroupStarts(spans [][3]int) []int {
	var starts []int
	seen := false
	for i := 0; i < len(spans); i++ {
		if spans[i][0] == 4 && (!seen || spans[i-1][0] == 7) {
			starts = append(starts, i)
			seen = true
		}
	}
	return starts
}

func p7aMetaInt(t *testing.T, meta map[string]interface{}, key string) int {
	t.Helper()
	f, ok := meta[key].(float64)
	if !ok {
		t.Fatalf("oracle meta %q missing or not numeric: %v", key, meta[key])
	}
	return int(f)
}

func TestA_P7A_MultiFieldStructure(t *testing.T) {
	fixture, oracle := loadP7a(t)
	if len(oracle.Fields) != 6 {
		t.Fatalf("oracle has %d fields, want 6", len(oracle.Fields))
	}
	rdr, err := OpenReader(bytes.NewReader(fixture), int64(len(fixture)))
	if err != nil {
		t.Fatalf("OpenReader: %v (multi-field envelope must open)", err)
	}
	defer rdr.Close()

	n, err := rdr.NumMessages()
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("NumMessages = %d, want 6 (one per data field)", n)
	}
	wantEnvelopeLen := int(binary.BigEndian.Uint64(fixture[8:16]))
	for i := 0; i < n; i++ {
		m, err := rdr.Message(i)
		if err != nil {
			t.Fatalf("Message(%d): %v", i, err)
		}
		if got := m.Sections(); len(got) != 7 || got[0] != 0 || got[1] != 1 || got[2] != 3 || got[3] != 4 || got[6] != 7 {
			t.Errorf("field %d: Sections() = %v, want [0 1 3 4 5 6 7]", i, got)
		}
		if m.Size() != wantEnvelopeLen {
			t.Errorf("field %d: Size() = %d, want envelope totalLength %d", i, m.Size(), wantEnvelopeLen)
		}
		if m.Offset() != 0 {
			t.Errorf("field %d: Offset() = %d, want 0 (single envelope)", i, m.Offset())
		}
		if m.Index() != i {
			t.Errorf("field %d: Index() = %d", i, m.Index())
		}
		if m.Edition() != 2 || m.Discipline() != 0 {
			t.Errorf("field %d: edition=%d discipline=%d, want 2/0", i, m.Edition(), m.Discipline())
		}
		if _, err := m.Section(4); err != nil {
			t.Fatalf("field %d: section 4 absent: %v", i, err)
		}
		of := oracle.Fields[i]
		wNi := p7aMetaInt(t, of.Meta, "Ni")
		wNj := p7aMetaInt(t, of.Meta, "Nj")
		wNV := p7aMetaInt(t, of.Meta, "numberOfValues")
		if wNi != 331 || wNj != 191 || wNV != 63221 {
			t.Errorf("field %d: oracle grid metadata unexpected: Ni=%d Nj=%d NV=%d", i, wNi, wNj, wNV)
		}
		g, err := m.Grid()
		if err != nil {
			t.Fatalf("field %d: Grid(): %v", i, err)
		}
		if g.Ni() != wNi || g.Nj() != wNj {
			t.Errorf("field %d: grid Ni/Nj = %d/%d, want %d/%d (oracle)", i, g.Ni(), g.Nj(), wNi, wNj)
		}
		dr, err := m.DataRep()
		if err != nil {
			t.Fatalf("field %d: DataRep(): %v", i, err)
		}
		if dr.NumValues != wNV {
			t.Errorf("field %d: DataRep.NumValues = %d, want %d (oracle)", i, dr.NumValues, wNV)
		}
		if dr.TemplateNumber != p7aMetaInt(t, of.Meta, "dataRepresentationTemplateNumber") {
			t.Errorf("field %d: DataRep.TemplateNumber = %d, oracle disagrees", i, dr.TemplateNumber)
		}
		p, err := m.Product()
		if err != nil {
			t.Fatalf("field %d: Product(): %v", i, err)
		}
		if p.ParameterCategory != p7aMetaInt(t, of.Meta, "parameterCategory") ||
			p.ParameterNumber != p7aMetaInt(t, of.Meta, "parameterNumber") {
			t.Errorf("field %d: product param = %d.%d, oracle disagrees", i, p.ParameterCategory, p.ParameterNumber)
		}
	}
	// The six fields must be distinct parameters in the JMA order gh/u/v/t/r/w.
	wantParams := [][2]int{{3, 5}, {2, 2}, {2, 3}, {0, 0}, {1, 1}, {2, 8}}
	for i, wp := range wantParams {
		m, err := rdr.Message(i)
		if err != nil {
			t.Fatal(err)
		}
		p, err := m.Product()
		if err != nil {
			t.Fatal(err)
		}
		if p.ParameterCategory != wp[0] || p.ParameterNumber != wp[1] {
			t.Errorf("field %d: parameter = %d.%d, want %d.%d (JMA order gh/u/v/t/r/w)",
				i, p.ParameterCategory, p.ParameterNumber, wp[0], wp[1])
		}
	}
}

func TestA_P7A_ValuesBitExact(t *testing.T) {
	fixture, oracle := loadP7a(t)
	rdr, err := OpenReader(bytes.NewReader(fixture), int64(len(fixture)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer rdr.Close()
	for i, of := range oracle.Fields {
		m, err := rdr.Message(i)
		if err != nil {
			t.Fatalf("Message(%d): %v", i, err)
		}
		got, err := m.Values()
		if err != nil {
			t.Fatalf("field %d: Values(): %v", i, err)
		}
		vb, err := base64.StdEncoding.DecodeString(of.ValuesB64)
		if err != nil {
			t.Fatalf("field %d: oracle values b64: %v", i, err)
		}
		if len(got) != of.NValues || len(vb)/4 != of.NValues {
			t.Fatalf("field %d: lengths got=%d oracle=%d bytes=%d", i, len(got), of.NValues, len(vb))
		}
		mismatches := 0
		maxDiff := 0.0
		for j := range got {
			want := math.Float32frombits(binary.LittleEndian.Uint32(vb[4*j:]))
			if math.Float32bits(got[j]) != math.Float32bits(want) {
				mismatches++
				if d := math.Abs(float64(got[j]) - float64(want)); d > maxDiff {
					maxDiff = d
				}
			}
		}
		if mismatches != 0 {
			t.Errorf("field %d: %d/%d values differ from eccodes oracle (max abs diff %g) — want bit-exact",
				i, mismatches, len(got), maxDiff)
		}
	}
}

// TestA_P7A_Coordinates compares per-point lat/lon of field 0 against the
// eccodes get_data arrays. Tolerance 1e-9 deg ( caliber). Longitudes are
// compared after mod-360 normalization: eccodes reports the unnormalized
// 195.0 where this reader normalizes to -165.0 (same physical point; our
// normalizeLon convention, established P1).
func TestA_P7A_Coordinates(t *testing.T) {
	fixture, oracle := loadP7a(t)
	rdr, err := OpenReader(bytes.NewReader(fixture), int64(len(fixture)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer rdr.Close()
	m, err := rdr.Message(0)
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.Grid()
	if err != nil {
		t.Fatal(err)
	}
	of := oracle.Fields[0]
	latsB, _ := base64.StdEncoding.DecodeString(of.LatsB64)
	lonsB, _ := base64.StdEncoding.DecodeString(of.LonsB64)
	n := len(latsB) / 8
	if g.NumPoints() != n {
		t.Fatalf("grid points = %d, oracle %d", g.NumPoints(), n)
	}
	const tol = 1e-9
	badLat, badLon := 0, 0
	var maxLatDiff, maxLonDiff float64
	for i := 0; i < n; i++ {
		lat, lon, err := g.Point(i)
		if err != nil {
			t.Fatalf("Point(%d): %v", i, err)
		}
		wLat := math.Float64frombits(binary.LittleEndian.Uint64(latsB[8*i:]))
		wLon := math.Float64frombits(binary.LittleEndian.Uint64(lonsB[8*i:]))
		if d := math.Abs(lat - wLat); d > tol {
			badLat++
			if d > maxLatDiff {
				maxLatDiff = d
			}
		}
		if d := math.Abs(normLon360(lon) - normLon360(wLon)); d > tol {
			badLon++
			if d > maxLonDiff {
				maxLonDiff = d
			}
		}
	}
	if badLat != 0 || badLon != 0 {
		t.Errorf("coordinates differ from eccodes: %d/%d lats (max %g), %d/%d lons (max %g), want 0 within %.0e deg",
			badLat, n, maxLatDiff, badLon, n, maxLonDiff, tol)
	}
	// All six fields share the grid: field 5 must produce identical points.
	m5, err := rdr.Message(5)
	if err != nil {
		t.Fatal(err)
	}
	g5, err := m5.Grid()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i += 7919 { // sparse probe across the grid
		la, lo, _ := g.Point(i)
		lb, lb2, _ := g5.Point(i)
		if math.Abs(la-lb) > tol || math.Abs(normLon360(lo)-normLon360(lb2)) > tol {
			t.Errorf("field 5 Point(%d) = (%v,%v), field 0 = (%v,%v): shared grid must match", i, lb, lb2, la, lo)
		}
	}
}

func normLon360(lon float64) float64 {
	l := math.Mod(lon+180, 360)
	if l < 0 {
		l += 360
	}
	return l - 180
}

// TestA_P7A_FailLoudNegatives: byte-surgery on the fixture. Every variant
// must still fail loudly ( contract: new negatives error, existing
// error types/messages not rewritten).
func TestA_P7A_FailLoudNegatives(t *testing.T) {
	fixture, _ := loadP7a(t)
	spans := walkP7a(t, fixture)
	groups := p7aGroupStarts(spans)
	if len(groups) != 6 {
		t.Fatalf("fixture walk: %d groups, want 6", len(groups))
	}

	patchTotal := func(b []byte, total int64) {
		binary.BigEndian.PutUint64(b[8:16], uint64(total))
	}
	expectOpenError := func(name string, b []byte, substr ...string) {
		t.Helper()
		rdr, err := OpenReader(bytes.NewReader(b), int64(len(b)))
		if err == nil {
			rdr.Close()
			t.Fatalf("%s: OpenReader succeeded, want error", name)
		}
		for _, s := range substr {
			if !strings.Contains(err.Error(), s) {
				t.Fatalf("%s: error %q does not contain %q", name, err.Error(), s)
			}
		}
	}
	expectReadError := func(name string, b []byte, substr ...string) {
		t.Helper()
		rdr, err := OpenReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatalf("%s: OpenReader: %v (want open OK, read fail)", name, err)
		}
		defer rdr.Close()
		nm, _ := rdr.NumMessages()
		for i := 0; i < nm; i++ {
			if _, err := rdr.Message(i); err != nil {
				for _, s := range substr {
					if strings.Contains(err.Error(), s) {
						return
					}
				}
				t.Fatalf("%s: Message(%d) error %q does not contain %v", name, i, err.Error(), substr)
			}
		}
		t.Fatalf("%s: all Messages() succeeded, want error", name)
	}

	// (a1) Truncated multi-field envelope: cut mid group-6 s7 so the walk
	// ends with an unclosed field group; totalLength patched to the size.
	g6 := groups[5]
	s7off := spans[g6+3][1] // fixture group layout is [4,5,6,7]
	b := append([]byte(nil), fixture[:s7off+3]...)
	patchTotal(b, int64(len(b)))
	expectOpenError("a1 truncated final group", b, "incomplete")

	// (a2) File-level truncation: totalLength still claims the full envelope.
	b = append([]byte(nil), fixture[:len(fixture)-100]...)
	expectOpenError("a2 file truncated", b, "truncated")

	// (b) Non-4 after s7: group 2's leading section number 4 -> 5. The walk
	// defers to read time (no multi-field boundary armed yet), so Open stays
	// OK and Message(0) fails with the classic message — unchanged behavior.
	b = append([]byte(nil), fixture...)
	g2s4 := groups[1]
	b[spans[g2s4][1]+4] = 5
	expectReadError("b non-4 after s7", b, "sections out of order (got 5 after 7)")

	// (c) Out-of-order nesting inside a group: swap section numbers of
	// group 3's s5/s6. The first group boundary has already armed the
	// multi-field mode, so this fails at open time with the classic message.
	b = append([]byte(nil), fixture...)
	g3 := groups[2]
	s5num, s6num := spans[g3+1][1]+4, spans[g3+2][1]+4
	b[s5num], b[s6num] = 6, 5
	expectOpenError("c out-of-order nesting", b, "sections out of order (got 5 after 6)")

	// (d) Non-uniform layout: delete group 4's s6 entirely (and shrink
	// totalLength by its length). Field layouts must be identical.
	b = append([]byte(nil), fixture...)
	g4 := groups[3]
	s6 := spans[g4+2]
	copy(b[s6[1]:], b[s6[1]+s6[2]:])
	b = b[:len(b)-s6[2]]
	patchTotal(b, int64(len(b)))
	expectOpenError("d non-uniform layout", b, "field 3 has 3 sections, field 0 has 4")

	// (f) Trailer > 4 bytes after the last section in a multi-field envelope.
	b = append(append([]byte(nil), fixture...), '7', '7', '7', '7', 0)
	patchTotal(b, int64(len(b)))
	expectOpenError("f long trailer", b, "bad section length")

	// (g) Section length overshoot inside a multi-field group: inflate
	// group 5's s7 length so it claims bytes beyond the envelope end.
	b = append([]byte(nil), fixture...)
	g5 := groups[4]
	s7 := spans[g5+3]
	binary.BigEndian.PutUint32(b[s7[1]:s7[1]+4], uint32(s7[2]+10))
	expectOpenError("g section overshoot", b, "only")
}

// TestA_P7A_MixedFile: a multi-field envelope followed by a classic
// single-field message in the same file. The index must expand only the
// multi-field envelope; the classic tail keeps its legacy behavior.
func TestA_P7A_MixedFile(t *testing.T) {
	fixture, _ := loadP7a(t)
	classic, err := os.ReadFile("testdata/ecc-xcheck/negcorners.grib2")
	if err != nil {
		t.Fatalf("read classic fixture: %v", err)
	}
	if int64(binary.BigEndian.Uint64(classic[8:16])) != int64(len(classic)) {
		t.Fatalf("classic fixture totalLength mismatch (test precondition)")
	}
	b := append(append([]byte(nil), fixture...), classic...)
	rdr, err := OpenReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer rdr.Close()
	n, _ := rdr.NumMessages()
	if n != 7 {
		t.Fatalf("NumMessages = %d, want 7 (6 JMA fields + 1 classic)", n)
	}
	last, err := rdr.Message(6)
	if err != nil {
		t.Fatalf("Message(6): %v (classic tail must keep working)", err)
	}
	vals, err := last.Values()
	if err != nil || len(vals) != 9 {
		t.Fatalf("classic tail Values() = %d values, err=%v; want 9", len(vals), err)
	}
	if !almostEq(float64(vals[0]), 273.15, 1e-4) {
		t.Errorf("classic tail first value = %v, want 273.15", vals[0])
	}
	// Iteration must yield all seven in order without error.
	count := 0
	for m, err := range rdr.Messages() {
		if err != nil {
			t.Fatalf("Messages() at %d: %v", count, err)
		}
		if m.Index() != count {
			t.Errorf("iteration index mismatch: got %d want %d", m.Index(), count)
		}
		count++
	}
	if count != 7 {
		t.Fatalf("iterated %d messages, want 7", count)
	}
}

// TestA_P7A_EnvelopeNotCollapsed documents the acceptance fact: one envelope
// with six field groups must NOT collapse to one message (that would lose
// five fields), even though section-0 totalLength covers the whole file.
func TestA_P7A_EnvelopeNotCollapsed(t *testing.T) {
	fixture, _ := loadP7a(t)
	if int64(binary.BigEndian.Uint64(fixture[8:16])) != int64(len(fixture)) {
		t.Fatalf("fixture totalLength does not cover the whole file")
	}
	rdr, err := OpenReader(bytes.NewReader(fixture), int64(len(fixture)))
	if err != nil {
		t.Fatal(err)
	}
	defer rdr.Close()
	n, _ := rdr.NumMessages()
	if n != 6 {
		t.Fatalf("NumMessages = %d, want 6 (one envelope must not collapse to one message)", n)
	}
}
