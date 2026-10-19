// tests for the local-use template registration API
// (spec.RegisterLocalTable) and its reader wiring (Message.Values() must
// surface *grib.ErrUnknownTemplate with the registration hint for unregistered
// local templates, and must decode normally once a definition is registered).
// External test package: the wiring half imports the root grib package.

package spec_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/pack"
	"github.com/knoweather/simple-grib/spec"
)

// findFreeLocalNumber returns a local-band template number in the given
// section with no WMO definition — scanned dynamically so the tests stay
// correct if the WMO CSV tables ever grow into the band.
func findFreeLocalNumber(t *testing.T, section int) int {
	t.Helper()
	for n := spec.LocalBandLowMin; n <= spec.LocalBandLowMax; n++ {
		if _, ok := spec.Template(section, n); !ok {
			return n
		}
	}
	for n := spec.LocalBandHighMin; n <= spec.LocalBandHighMax; n++ {
		if _, ok := spec.Template(section, n); !ok {
			return n
		}
	}
	t.Fatalf("no free local-band number found in section %d", section)
	return 0
}

func TestRegisterLocalTableValidation(t *testing.T) {
	free := findFreeLocalNumber(t, 5)
	def := &spec.TemplateDef{Name: "local test template"}

	// Section validation: only template-bearing sections (1,3,4,5,6,7).
	for _, badSec := range []int{0, 2, 8, -1} {
		if err := spec.RegisterLocalTable(badSec, free, def); err == nil {
			t.Errorf("section %d: expected error, got nil", badSec)
		}
	}
	// Band validation: outside both local bands.
	for _, badNum := range []int{0, 191, 255, 32767, 65535} {
		if err := spec.RegisterLocalTable(5, badNum, def); err == nil {
			t.Errorf("number %d: expected error, got nil", badNum)
		}
	}
	// Nil definition.
	if err := spec.RegisterLocalTable(5, free, nil); err == nil {
		t.Error("nil definition: expected error, got nil")
	}
}

func TestRegisterLocalTableWMOCollision(t *testing.T) {
	// A real WMO definition inside the local band (per specdata: e.g. 5.200
	// is WMO-assigned inside section 5's 192-254 band). Scan dynamically so
	// the test tracks table drift.
	var wmoNum int
	found := false
	for n := spec.LocalBandLowMin; n <= spec.LocalBandLowMax; n++ {
		if _, ok := spec.Template(5, n); ok {
			wmoNum, found = n, true
			break
		}
	}
	if !found {
		t.Fatal("no WMO definition found inside section 5's local band — collision path untestable")
	}
	def := &spec.TemplateDef{Name: "attempted override"}
	err := spec.RegisterLocalTable(5, wmoNum, def)
	if err == nil {
		t.Fatalf("registering over WMO template 5.%d: expected error, got nil", wmoNum)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("WMO definition")) {
		t.Errorf("error %q does not identify the WMO collision", err.Error())
	}
	// The WMO definition must be untouched.
	wmo, ok := spec.Template(5, wmoNum)
	if !ok || wmo.Name == "attempted override" {
		t.Errorf("WMO template 5.%d was modified by the failed registration", wmoNum)
	}
}

func TestRegisterLocalTableLookupAndReplace(t *testing.T) {
	free := findFreeLocalNumber(t, 4) // section 4 (product definition) low band

	def1 := &spec.TemplateDef{Name: "first local"}
	if err := spec.RegisterLocalTable(4, free, def1); err != nil {
		t.Fatalf("register: %v", err)
	}
	// The registry stores a validated deep copy: the caller's def is never
	// mutated, and Template() never hands the caller's pointer back out
	// (sharing the pointer in place would race two goroutines registering
	// at the same time).
	if def1.ID != "" {
		t.Errorf("caller's def was mutated: ID = %q, want empty", def1.ID)
	}
	got, ok := spec.Template(4, free)
	if !ok {
		t.Fatalf("Template(4,%d) after register: not found", free)
	}
	if got == def1 {
		t.Fatalf("Template(4,%d) returned the caller's pointer, want the registry's copy", free)
	}
	if got.ID != "4."+strconv.Itoa(free) {
		t.Errorf("registered copy ID = %q, want 4.%d", got.ID, free)
	}
	// Later edits to the caller's struct must not corrupt the registry.
	def1.Name = "mutated after registration"
	if again, _ := spec.Template(4, free); again.Name != "first local" {
		t.Errorf("registry reflects caller mutation: Name = %q, want %q", again.Name, "first local")
	}

	// Re-registration of the same local pair replaces the definition.
	def2 := &spec.TemplateDef{Name: "second local"}
	if err := spec.RegisterLocalTable(4, free, def2); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	got, ok = spec.Template(4, free)
	if !ok || got.Name != "second local" {
		t.Errorf("Template(4,%d) after re-register: got %q, want the replacement", free, got.Name)
	}
}

// Field validation at registration time: an incoherent fixed field range
// must be rejected up front instead of surfacing later from NewWalk (or
// panicking inside it).
func TestRegisterLocalTableFieldValidation(t *testing.T) {
	free := findFreeLocalNumber(t, 5)
	bad := []*spec.TemplateDef{
		{Fields: []spec.Field{{Start: -1, Count: 1, Kind: spec.KindU8}}},
		{Fields: []spec.Field{{Start: 10, Count: 4, Kind: spec.KindU8}}},  // width mismatch
		{Fields: []spec.Field{{Start: 10, Count: 1, Kind: spec.KindU16}}}, // width mismatch
		{Fields: []spec.Field{{Start: 10, Count: 1, Kind: spec.Kind(99)}}},
	}
	for i, def := range bad {
		if err := spec.RegisterLocalTable(5, free, def); err == nil {
			t.Errorf("case %d: expected a validation error, got nil", i)
		}
	}
	ok := &spec.TemplateDef{Fields: []spec.Field{{Start: 10, Count: 1, Kind: spec.KindU8}}}
	if err := spec.RegisterLocalTable(5, free, ok); err != nil {
		t.Errorf("valid definition rejected: %v", err)
	}
}

func TestErrUnknownTemplateHint(t *testing.T) {
	e := &grib.ErrUnknownTemplate{Section: 5, Number: 200}
	msg := e.Error()
	if !bytes.Contains([]byte(msg), []byte("spec.RegisterLocalTable")) {
		t.Errorf("error %q must name the registration API (ruling)", msg)
	}
	if !bytes.Contains([]byte(msg), []byte("5")) || !bytes.Contains([]byte(msg), []byte("200")) {
		t.Errorf("error %q must carry section and number", msg)
	}
}

// patchDataRepTemplate rewrites the data-representation (section 5) template
// number in a copy of the golden file to a local-band value. Sections are
// located by walking [U32 len][U8 section] records after the 16-byte header —
// no hardcoded offsets, so the test survives canonical-message edits.
func patchDataRepTemplate(t *testing.T, n int) []byte {
	t.Helper()
	path := filepath.Join("..", "testdata", "gribw-xcheck", "p31_frame_simple.grib2")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	out := append([]byte(nil), data...)
	off := 16 // header: "GRIB" FF FF discipline edition U64 totalLen
	for off < len(out) {
		ln := int(binary.BigEndian.Uint32(out[off : off+4]))
		if ln <= 0 || off+ln > len(out) {
			t.Fatalf("bad section length %d at offset %d", ln, off)
		}
		if out[off+4] == 5 {
			binary.BigEndian.PutUint16(out[off+9:off+11], uint16(n))
			return out
		}
		off += ln
	}
	t.Fatal("no section 5 found in golden file")
	return nil
}

func TestValuesUnregisteredLocalTemplate(t *testing.T) {
	n := findFreeLocalNumber(t, 5)
	patched := patchDataRepTemplate(t, n)

	f, err := grib.OpenReader(bytes.NewReader(patched), int64(len(patched)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()
	m, _ := f.Message(0)
	_, err = m.Values()
	var ukt *grib.ErrUnknownTemplate
	if !errors.As(err, &ukt) {
		t.Fatalf("Values() error = %v (%T), want *grib.ErrUnknownTemplate", err, err)
	}
	if ukt.Section != 5 || ukt.Number != n {
		t.Errorf("ErrUnknownTemplate = {%d,%d}, want {5,%d}", ukt.Section, ukt.Number, n)
	}
	if !bytes.Contains([]byte(ukt.Error()), []byte("spec.RegisterLocalTable")) {
		t.Errorf("wired error %q must carry the registration hint", ukt.Error())
	}
}

func TestValuesRegisteredLocalTemplate(t *testing.T) {
	n := findFreeLocalNumber(t, 5)
	patched := patchDataRepTemplate(t, n)

	// Unregistered: structured unknown-template error (wiring half).
	f, err := grib.OpenReader(bytes.NewReader(patched), int64(len(patched)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	m, _ := f.Message(0)
	if _, err := m.Values(); err == nil {
		t.Fatal("Values() before registration: expected error, got nil")
	}
	f.Close()

	// Register a local definition that reuses the simple-packing field layout.
	// Registration makes the template KNOWN — Walk-based parsing and name
	// display work — but with no pack-level decoder installed, Values() must
	// keep the generic pack.ErrUnsupported instead of the unknown-template
	// error.
	base, ok := spec.Template(5, 0)
	if !ok {
		t.Fatal("WMO template 5.0 missing")
	}
	local := &spec.TemplateDef{
		Name:   "local simple packing clone",
		Status: "local-use",
		Fields: base.Fields,
	}
	if err := spec.RegisterLocalTable(5, n, local); err != nil {
		t.Fatalf("RegisterLocalTable: %v", err)
	}

	f2, err := grib.OpenReader(bytes.NewReader(patched), int64(len(patched)))
	if err != nil {
		t.Fatalf("OpenReader after register: %v", err)
	}
	defer f2.Close()
	m2, _ := f2.Message(0)
	_, err = m2.Values()
	if err == nil {
		t.Fatal("Values() after registration without a decoder: expected error, got nil")
	}
	var ukt *grib.ErrUnknownTemplate
	if errors.As(err, &ukt) {
		t.Errorf("registered template must not surface ErrUnknownTemplate (got %v)", err)
	}
	if !errors.Is(err, pack.ErrUnsupported) {
		t.Errorf("registered-but-undecoded error = %v (%T), want pack.ErrUnsupported", err, err)
	}
}
