//
// : end-to-end decode of template 5.3 through the public
// API (Open -> Message -> Values). Golden values are eccodes 2.41 ground
// truth for the dual-source calibrated fixture . This test replaces the 5.3 half of
// falloud_baseline_test.go now that the unpacker is registered.

package grib

import "testing"

func TestA_Complex53EndToEnd(t *testing.T) {
	fl, err := Open("testdata/gribw-xcheck/complex_53_spd.grib2")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer fl.Close()
	m, err := fl.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	dr, err := m.DataRep()
	if err != nil {
		t.Fatalf("DataRep: %v", err)
	}
	if dr.TemplateNumber != 3 || dr.NumValues != 9 {
		t.Fatalf("tmpl=%d NV=%d, want tmpl=3 NV=9", dr.TemplateNumber, dr.NumValues)
	}
	got, err := m.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	want := []float32{100, 102, 104, 108, 110, 112, 116, 118, 120}
	if len(got) != len(want) {
		t.Fatalf("len %d != %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("value %d = %v, want %v (eccodes ground truth)", i, got[i], want[i])
		}
	}
}

// : template 5.2 (plain complex packing) now has a
// registered unpacker — the former fail-loud red line is replaced by golden
// values, exactly as falloud_baseline_test.go's header promised. Golden =
// eccodes 2.41 ground truth for the synthetic dual-source fixture (no
// real-world 5.2 samples exist in public AWS — internal item 1389①).
func TestA_Complex52EndToEnd(t *testing.T) {
	fl, err := Open("testdata/gribw-xcheck/complex_52_plain.grib2")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer fl.Close()
	m, err := fl.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	dr, err := m.DataRep()
	if err != nil {
		t.Fatalf("DataRep: %v", err)
	}
	if dr.TemplateNumber != 2 || dr.NumValues != 9 {
		t.Fatalf("tmpl=%d NV=%d, want tmpl=2 NV=9", dr.TemplateNumber, dr.NumValues)
	}
	got, err := m.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	want := []float32{100, 102, 104, 108, 110, 112, 116, 118, 120}
	if len(got) != len(want) {
		t.Fatalf("len %d != %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("value %d = %v, want %v (eccodes ground truth)", i, got[i], want[i])
		}
	}
}
