//
// Binary differential of the full chain against eccodes 2.49.0: complete
// GRIB2 messages captured byte-for-byte from eccodes itself (see
// testdata/ecc-xcheck/golden_*.json; the capture script mirrors mk_vectors.py —
// same sample, same grid setup, values set through grib_set_double_array and
// the message re-encoded by eccodes). Each vector carries:
//
// - full_message_hex: the entire encoded message (all sections plus the 4-byte
// "7777" tail this eccodes build appends after section 7);
//   - sections: per-section bytes (for layout documentation);
//   - eccodes_values: what eccodes decodes from its own encoding.
//
// The test drives the public API end to end:
//
//	OpenReader -> Message -> Identification / Grid / Product / DataRep -> Values
//
// and asserts both the metadata (decoded from the captured section bytes) and
// the decoded values (float32 vs eccodes' float64, tolerance 1e-5 relative —
// simple packing is computed in float64 here and rounded to float32, while
// eccodes returns full doubles).
//
// The test was authored for the target state and doubles as the regression
// test for two fixes: (1) parseSections must tolerate the <=4-byte tail after
// section 7 that every message from this build carries, and (2) DataRep must
// read the template number from octets 10-11, U16(9). Until both land,
// OpenReader fails on the tail and DataRep/Values report the wrong template
// number.

package grib

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

type goldenVector struct {
	Kind           string    `json:"kind"`
	NumValues      int       `json:"numValues"`
	GridPoints     int       `json:"gridPoints"`
	FullMessageHex string    `json:"full_message_hex"`
	TailHex        string    `json:"tail_hex"`
	TotalLenField  int64     `json:"totalLen_field"`
	EccodesValues  []float64 `json:"eccodes_values"`
}

func loadGoldenVector(t *testing.T, name string) goldenVector {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ecc-xcheck", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var v goldenVector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	return v
}

func TestGoldenFullChain(t *testing.T) {
	cases := []struct {
		file       string
		drTemplate int // expected data representation template number (section 5 octets 10-11)
	}{
		{"golden_sp_a.json", 0},   // simple packing, D=2, bits=20
		{"golden_sp_b.json", 0},   // simple packing, wide integer range, E=6, bits=24
		{"golden_ieee_a.json", 4}, // IEEE binary32
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			v := loadGoldenVector(t, tc.file)
			msg, err := hex.DecodeString(v.FullMessageHex)
			if err != nil {
				t.Fatalf("decode full_message_hex: %v", err)
			}

			f, err := OpenReader(bytes.NewReader(msg), int64(len(msg)))
			if err != nil {
				t.Fatalf("OpenReader (needs item 1: tail tolerance): %v", err)
			}
			defer f.Close()

			n, err := f.NumMessages()
			if err != nil {
				t.Fatalf("NumMessages: %v", err)
			}
			if n != 1 {
				t.Fatalf("NumMessages = %d, want 1", n)
			}

			m, err := f.Message(0)
			if err != nil {
				t.Fatalf("Message(0): %v", err)
			}
			if m.Size() != len(msg) {
				t.Errorf("Size = %d, want %d", m.Size(), len(msg))
			}
			if m.Edition() != 2 {
				t.Errorf("Edition = %d, want 2", m.Edition())
			}
			if m.Discipline() != 0 {
				t.Errorf("Discipline = %d, want 0", m.Discipline())
			}

			wantSecs := map[int]bool{0: true, 1: true, 3: true, 4: true, 5: true, 6: true, 7: true}
			if got := m.Sections(); len(got) != len(wantSecs) {
				t.Errorf("Sections = %v, want exactly %v", got, wantSecs)
			} else {
				for _, s := range got {
					if !wantSecs[s] {
						t.Errorf("unexpected section %d in %v", s, got)
					}
					delete(wantSecs, s)
				}
			}

			// --- identification (section 1, fixed WMO layout) ---
			id, err := m.Identification()
			if err != nil {
				t.Fatalf("Identification: %v", err)
			}
			if id.Centre != 98 || id.SubCentre != 0 {
				t.Errorf("centre/subcentre = %d/%d, want 98/0 (ECMWF)", id.Centre, id.SubCentre)
			}
			if id.MasterTableVersion != 4 || id.LocalTableVersion != 0 {
				t.Errorf("table versions = %d/%d, want 4/0", id.MasterTableVersion, id.LocalTableVersion)
			}
			wantRef := "2007-03-23T12:00:00Z"
			if got := id.ReferenceTime.UTC().Format("2006-01-02T15:04:05Z"); got != wantRef {
				t.Errorf("ReferenceTime = %s, want %s", got, wantRef)
			}
			if id.ProductionStatus != 0 || id.TypeOfProcessedData != 2 {
				t.Errorf("prodStatus/typeProc = %d/%d, want 0/2", id.ProductionStatus, id.TypeOfProcessedData)
			}

			// --- grid (section 3, template 0: 9x12 regular ll, all-positive corners) ---
			g, err := m.Grid()
			if err != nil {
				t.Fatalf("Grid: %v", err)
			}
			if got := g.NumPoints(); got != v.GridPoints {
				t.Errorf("NumPoints = %d, want %d", got, v.GridPoints)
			}
			firstLat, firstLon, err := g.Point(0)
			if err != nil {
				t.Fatalf("Point(0): %v", err)
			}
			if firstLat != 5 || firstLon != 0 {
				t.Errorf("first point = (%g, %g), want (5, 0)", firstLat, firstLon)
			}
			lastIdx := v.GridPoints - 1
			lastLat, lastLon, err := g.Point(lastIdx)
			if err != nil {
				t.Fatalf("Point(%d): %v", lastIdx, err)
			}
			if lastLat != 45 || lastLon != 55 {
				t.Errorf("last point = (%g, %g), want (45, 55)", lastLat, lastLon)
			}

			// --- product (section 4, template 0.0: temperature at a level) ---
			p, err := m.Product()
			if err != nil {
				t.Fatalf("Product: %v", err)
			}
			if p.TemplateNumber != 0 {
				t.Errorf("product template = %d, want 0", p.TemplateNumber)
			}
			if p.ParameterCategory != 0 || p.ParameterNumber != 0 {
				t.Errorf("parameter = %d.%d, want 0.0 (temperature)", p.ParameterCategory, p.ParameterNumber)
			}
			if p.TypeOfGeneratingProcess != 0 {
				t.Errorf("generating process = %d, want 0", p.TypeOfGeneratingProcess)
			}
			// WMO template 4.0: type of first fixed surface at octet 23
			// (idx 22) = 1 (ground or water surface); its scale/value bytes
			// are all-ones (WMO missing) -> HasValue must be false.
			if p.Level1 == nil || p.Level1.Type != 1 || p.Level1.HasValue {
				t.Errorf("level 1 = %+v, want {Type:1 HasValue:false} (ground)", p.Level1)
			}
			if p.Level2 == nil || p.Level2.Type != 255 {
				t.Errorf("level 2 type = %+v, want 255 (missing)", p.Level2)
			}

			// --- data representation (section 5) ---
			dr, err := m.DataRep()
			if err != nil {
				t.Fatalf("DataRep: %v", err)
			}
			if dr.TemplateNumber != tc.drTemplate {
				t.Errorf("DataRep template = %d, want %d (needs item 2: U16(9))", dr.TemplateNumber, tc.drTemplate)
			}
			if dr.NumValues != v.NumValues {
				t.Errorf("NumValues = %d, want %d", dr.NumValues, v.NumValues)
			}

			// --- values (full chain: section 7 payload -> pack registry) ---
			got, err := m.Values()
			if err != nil {
				t.Fatalf("Values: %v", err)
			}
			if len(got) != len(v.EccodesValues) {
				t.Fatalf("len(Values) = %d, want %d", len(got), len(v.EccodesValues))
			}
			maxRel := 0.0
			for i := range got {
				want := v.EccodesValues[i]
				denom := math.Max(1.0, math.Abs(want))
				rel := math.Abs(float64(got[i])-want) / denom
				if rel > maxRel {
					maxRel = rel
				}
				if rel > 1e-5 {
					t.Errorf("value[%d] = %v, want %v (rel diff %g)", i, got[i], want, rel)
				}
			}
			t.Logf("%s: %d values match eccodes (max rel diff %g)", tc.file, len(got), maxRel)
		})
	}
}
