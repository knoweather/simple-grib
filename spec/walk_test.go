package spec

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Walk mechanics
// ---------------------------------------------------------------------------

// mkSection builds a section byte slice of length n with the 4-octet length
// prefix filled in (contents zero).
func mkSection(n int) []byte {
	b := make([]byte, n)
	binary.BigEndian.PutUint32(b[0:4], uint32(n))
	return b
}

// TestWalkSimplePacking decodes template 5.0 field by field against hand-set
// bytes and checks values, kinds and 1-based octet numbers.
func TestWalkSimplePacking(t *testing.T) {
	def, ok := Template(5, 0)
	if !ok {
		t.Fatal("template 5.0 missing from spec")
	}
	// Template 5.0 field octets (§5.12): R 12-15, E 16-17, D 18-19, bits 20, origType 21.
	sec := mkSection(24)
	binary.BigEndian.PutUint32(sec[11:15], math.Float32bits(27315.0)) // R @ octet 12
	binary.BigEndian.PutUint16(sec[15:17], 0x8000)                    // E raw @ octet 16 (unsigned 32768)
	binary.BigEndian.PutUint16(sec[17:19], 2)                         // D @ octet 18
	sec[19] = 8                                                       // bits @ octet 20
	sec[20] = 0                                                       // origType @ octet 21

	w, err := NewWalk(def, sec)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	if w.Len() != len(def.Fields) || w.Def().ID != "5.0" {
		t.Fatalf("Len=%d Def.ID=%s, want 5 / 5.0", w.Len(), w.Def().ID)
	}

	want := []struct {
		octet int
		kind  Kind
		u     uint64
	}{
		{12, KindF32, uint64(math.Float32bits(27315.0))},
		{16, KindU16, 0x8000},
		{18, KindU16, 2},
		{20, KindU8, 8},
		{21, KindU8, 0},
	}
	for i, wc := range want {
		v := w.At(i)
		if v.Octet != wc.octet || v.Kind != wc.kind || v.U != wc.u {
			t.Errorf("field %d: got octet=%d kind=%d U=%#x, want octet=%d kind=%d U=%#x",
				i, v.Octet, v.Kind, v.U, wc.octet, wc.kind, wc.u)
		}
	}
	// F32 fields must carry the float in F as well (bits identical).
	if got := w.At(0).F; got != 27315.0 || math.Float32bits(got) != uint32(w.At(0).U) {
		t.Errorf("F32 field: F=%v U=%#x, want 27315.0 with matching bits", got, w.At(0).U)
	}
}

// TestWalkByOctet checks the octet-indexed lookup: hits only at field starts.
func TestWalkByOctet(t *testing.T) {
	def, _ := Template(5, 0)
	sec := mkSection(24)
	w, err := NewWalk(def, sec)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	for _, octet := range []int{12, 16, 18, 20, 21} {
		if v, ok := w.ByOctet(octet); !ok || v.Octet != octet {
			t.Errorf("ByOctet(%d): ok=%v v.Octet=%d, want hit at %d", octet, ok, v.Octet, octet)
		}
	}
	for _, octet := range []int{1, 13, 17, 19, 22, 100} { // inside fields / outside
		if _, ok := w.ByOctet(octet); ok {
			t.Errorf("ByOctet(%d): unexpected hit", octet)
		}
	}
}

// TestWalkAtBounds: out-of-range At returns the zero Value, not a panic.
func TestWalkAtBounds(t *testing.T) {
	def, _ := Template(5, 0)
	w, err := NewWalk(def, mkSection(24))
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	for _, i := range []int{-1, w.Len()} {
		if v := w.At(i); v != (Value{}) {
			t.Errorf("At(%d) = %+v, want zero Value", i, v)
		}
	}
}

// TestWalkTruncation: a fixed field whose range exceeds the section must fail
// with an error naming the template; a section ending exactly at the last
// field's end is accepted.
func TestWalkTruncation(t *testing.T) {
	def, _ := Template(5, 0) // last field (origType) ends at octet 21

	if _, err := NewWalk(def, mkSection(20)); err == nil {
		t.Fatal("NewWalk with 20 bytes: want error (origType needs octet 21)")
	} else if !strings.Contains(err.Error(), "5.0") {
		t.Errorf("truncation error %q should name the template", err)
	}
	if _, err := NewWalk(def, mkSection(21)); err != nil {
		t.Fatalf("NewWalk with exactly 21 bytes: %v", err)
	}
}

// TestWalkNilDef guards the nil-definition path.
func TestWalkNilDef(t *testing.T) {
	if _, err := NewWalk(nil, mkSection(16)); err == nil {
		t.Fatal("NewWalk(nil): want error")
	}
}

// TestWalkTailAndData: variable tails and data aliasing for template 3.0.
func TestWalkTailAndData(t *testing.T) {
	def, ok := Template(3, 0)
	if !ok {
		t.Fatal("template 3.0 missing")
	}
	sec := mkSection(100)
	for i := 72; i < 100; i++ { // quasi-regular point list payload
		sec[i] = byte(i - 72 + 1)
	}
	w, err := NewWalk(def, sec)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	tail := w.Tail(73) // octet 73 onward (0-based idx 72)
	if len(tail) != 28 || tail[0] != 1 || tail[27] != 28 {
		t.Errorf("Tail(73) = %d bytes [%x..], want 28 bytes starting at 1", len(tail), tail[:4])
	}
	if got := w.Tail(101); got == nil || len(got) != 0 {
		t.Errorf("Tail(101) (== end) = %v, want empty non-nil slice", got)
	}
	for _, bad := range []int{0, -5, 102, 1000} {
		if got := w.Tail(bad); got != nil {
			t.Errorf("Tail(%d) = %v, want nil", bad, got)
		}
	}
	if d := w.Data(); &d[0] != &sec[0] || len(d) != 100 {
		t.Error("Data() must alias the input section bytes")
	}
}

// TestWalkSymbolic: template 3.101 mixes plain and symbolic fields.
func TestWalkSymbolic(t *testing.T) {
	def, ok := Template(3, 101)
	if !ok {
		t.Fatal("template 3.101 missing")
	}
	// Fields: U8@octet15, Sym@octet16 (Start=15), U8@octet19, Sym@octet20 (Start=19).
	sec := mkSection(35)
	sec[14] = 7 // octet 15
	w, err := NewWalk(def, sec)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	if got := w.SymbolicAt(); got != 1 {
		t.Errorf("SymbolicAt() = %d, want 1", got)
	}
	if v := w.At(1); v.Kind != KindSymbolic || v.U != 0 {
		t.Errorf("symbolic field value = %+v, want zeroed KindSymbolic", v)
	}
	// A symbolic field with Start >= 0 still answers ByOctet at its position.
	if v, ok := w.ByOctet(16); !ok || v.Kind != KindSymbolic {
		t.Errorf("ByOctet(16) = %+v ok=%v, want symbolic hit", v, ok)
	}
	if v, ok := w.ByOctet(15); !ok || v.Kind != KindU8 || v.U != 7 {
		t.Errorf("ByOctet(15) = %+v ok=%v, want U8 value 7", v, ok)
	}
}

// TestWalkNoSymbolic: a plain-only template reports SymbolicAt == -1.
func TestWalkNoSymbolic(t *testing.T) {
	def, _ := Template(5, 0)
	w, err := NewWalk(def, mkSection(24))
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	if got := w.SymbolicAt(); got != -1 {
		t.Errorf("SymbolicAt() = %d, want -1", got)
	}
}

// ---------------------------------------------------------------------------
// Scale (WMO scaling convention helper)
// ---------------------------------------------------------------------------

func TestScale(t *testing.T) {
	cases := []struct {
		raw   uint32
		scale int
		want  float64
	}{
		{12345000, 4, 1234.5},
		{12345, 2, 123.45},
		{12345, -2, 1234500},
		{7, 0, 7},
		{1, 9, 1e-9},
	}
	for _, c := range cases {
		if got := Scale(c.raw, c.scale); got != c.want {
			t.Errorf("Scale(%d, %d) = %v, want %v", c.raw, c.scale, got, c.want)
		}
	}
	// The full signed 1-octet range is honoured: no silent clamp.
	if got, want := Scale(1, 127), 1/math.Pow(10, 127); got != want {
		t.Errorf("Scale(1,127) = %v, want %v (must not clamp to 1e-9)", got, want)
	}
	if got, want := Scale(1, -127), math.Pow(10, 127); got != want {
		t.Errorf("Scale(1,-127) = %v, want %v (must not clamp to 1e9)", got, want)
	}
}

// Sign-magnitude helpers (ecCodes grib_decode_signed_long convention).
func TestSignedHelpers(t *testing.T) {
	if got := Signed8(0x81); got != -1 {
		t.Errorf("Signed8(0x81) = %d, want -1", got)
	}
	if got := Signed8(0x7F); got != 127 {
		t.Errorf("Signed8(0x7F) = %d, want 127", got)
	}
	if got := Signed8(0x80); got != 0 {
		t.Errorf("Signed8(0x80) = %d, want -0 (0)", got)
	}
	if got := Signed8(0xFF); got != -127 {
		t.Errorf("Signed8(0xFF) = %d, want -127 (missing sentinel)", got)
	}
	if got := Signed16(0x8006); got != -6 {
		t.Errorf("Signed16(0x8006) = %d, want -6", got)
	}
	if got := Signed32(0x80000001); got != -1 {
		t.Errorf("Signed32(0x80000001) = %d, want -1", got)
	}
	if got := Signed32(0x7FFFFFFF); got != 2147483647 {
		t.Errorf("Signed32(0x7FFFFFFF) = %d, want 2147483647", got)
	}
}

// ---------------------------------------------------------------------------
// Table / template registry lookups
// ---------------------------------------------------------------------------

func TestTableLookup(t *testing.T) {
	if _, ok := TableByID("no.such.table"); ok {
		t.Error("TableByID(\"no.such.table\"): want miss")
	}
	// table 5.1 (original data type): 0 = float, 1 = int (§5.12).
	tab, ok := TableByID("5.1")
	if !ok {
		t.Fatal("table 5.1 missing")
	}
	if e, ok := tab.Lookup(0); !ok || e.Name == "" {
		t.Errorf("table 5.1 Lookup(0) = %+v ok=%v, want a named entry", e, ok)
	}
	if _, ok := tab.Lookup(9999); ok {
		t.Error("table 5.1 Lookup(9999): want miss")
	}
	// table 3.2 (shape of the Earth): code 6 = spherical Earth with given radius.
	tab, ok = TableByID("3.2")
	if !ok {
		t.Fatal("table 3.2 missing")
	}
	e, ok := tab.Lookup(6)
	if !ok || e.Name == "" {
		t.Errorf("table 3.2 Lookup(6) = %+v ok=%v, want named entry", e, ok)
	}
}

func TestTemplateLookup(t *testing.T) {
	for _, tc := range []struct{ sec, num int }{{3, 0}, {3, 40}, {4, 0}, {5, 0}, {5, 4}} {
		def, ok := Template(tc.sec, tc.num)
		if !ok {
			t.Errorf("Template(%d, %d): want hit", tc.sec, tc.num)
			continue
		}
		if def.ID == "" || def.Name == "" || len(def.Fields) == 0 {
			t.Errorf("Template(%d,%d) = %+v, want populated definition", tc.sec, tc.num, def)
		}
	}
	if _, ok := Template(3, 9999); ok {
		t.Error("Template(3, 9999): want miss")
	}
	if _, ok := Template(9, 0); ok {
		t.Error("Template(9, 0): want miss (no such section)")
	}
}

// ---------------------------------------------------------------------------
// Binary differential: Walk over eccodes-encoded section 5 bytes
// (testdata/ecc-xcheck/*.json, captured from eccodes 2.49 — same vectors the
// pack xcheck uses; here we verify the WALK layer's field decoding).
// ---------------------------------------------------------------------------

type spVec struct {
	Kind        string  `json:"kind"`
	Bits        int     `json:"bits"`
	E           int     `json:"E"`
	D           int     `json:"D"`
	R           float64 `json:"R"`
	NumValues   int     `json:"numValues"`
	Section5Hex string  `json:"section5_hex"`
}

func TestWalkXcheckSection5(t *testing.T) {
	dir := filepath.Join("..", "testdata", "ecc-xcheck")
	for _, name := range []string{"sp_a.json", "sp_b.json", "sp_c.json", "sp_d.json", "sp_e.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var v spVec
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sec5, err := hex.DecodeString(v.Section5Hex)
		if err != nil {
			t.Fatalf("%s: bad section5_hex: %v", name, err)
		}
		def, ok := Template(5, 0)
		if !ok {
			t.Fatal("template 5.0 missing")
		}
		w, err := NewWalk(def, sec5)
		if err != nil {
			t.Fatalf("%s: NewWalk over eccodes section 5: %v", name, err)
		}
		// Walk field order for 5.0: R(F32), E(U16), D(U16), bits(U8), origType(U8).
		if got := w.At(0).F; math.Float32bits(got) != math.Float32bits(float32(v.R)) {
			t.Errorf("%s: Walk R = %v, want eccodes R = %v", name, got, v.R)
		}
		if got := int(w.At(1).U); got != v.E {
			t.Errorf("%s: Walk E = %d, want %d", name, got, v.E)
		}
		if got := int(w.At(2).U); got != v.D {
			t.Errorf("%s: Walk D = %d, want %d", name, got, v.D)
		}
		if got := int(w.At(3).U); got != v.Bits {
			t.Errorf("%s: Walk bits = %d, want %d", name, got, v.Bits)
		}
		// numberOfValues lives in the section header (octets 6-9), not in the
		// template — verify it straight from the bytes.
		if nv := int(binary.BigEndian.Uint32(sec5[5:9])); nv != v.NumValues {
			t.Errorf("%s: section5 numberOfValues = %d, want %d", name, nv, v.NumValues)
		}
	}

	// IEEE vector: template 5.4 has a single precision field at octet 12.
	raw, err := os.ReadFile(filepath.Join(dir, "ieee_a.json"))
	if err != nil {
		t.Fatalf("ieee_a.json: %v", err)
	}
	var iv struct {
		Kind        string `json:"kind"`
		NumValues   int    `json:"numValues"`
		Section5Hex string `json:"section5_hex"`
	}
	if err := json.Unmarshal(raw, &iv); err != nil {
		t.Fatalf("ieee_a.json: %v", err)
	}
	sec5, err := hex.DecodeString(iv.Section5Hex)
	if err != nil {
		t.Fatalf("ieee_a.json: bad section5_hex: %v", err)
	}
	def, ok := Template(5, 4)
	if !ok {
		t.Fatal("template 5.4 missing")
	}
	w, err := NewWalk(def, sec5)
	if err != nil {
		t.Fatalf("ieee_a: NewWalk over eccodes section 5: %v", err)
	}
	if got := int(w.At(0).U); got != 1 { // precision = 32-bit IEEE float
		t.Errorf("ieee_a: Walk precision = %d, want 1", got)
	}
	if tmplNo := int(binary.BigEndian.Uint16(sec5[9:11])); tmplNo != 4 {
		t.Errorf("ieee_a: section5 template number = %d, want 4", tmplNo)
	}
}
