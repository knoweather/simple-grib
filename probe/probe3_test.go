package probe

import (
	"encoding/binary"
	"testing"
	"time"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/gribw"
	"github.com/knoweather/simple-grib/spec"
)

// Deferred structural errors: Open() accepts these, Message() rejects them.
func TestProbeDeferredSectionErrors(t *testing.T) {
	cases := []struct {
		name string
		secs [][]byte
	}{
		{"missing section 7", [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0)}},
		{"missing section 3", [][]byte{sec1(), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7(nil)}},
		{"missing section 4", [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec5Simple(1, 0, 0, 0, 1, 0), sec7(nil)}},
		{"local section before 7", [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), secLocal(8), sec7(nil)}},
		{"section 0 inside", [][]byte{sec1(), secLocal(0), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7(nil)}},
		{"duplicate section 4", [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), secLocal(4), sec5Simple(1, 0, 0, 0, 1, 0), sec7(nil)}},
		{"last section is local 8", [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7(nil), secLocal(8)}},
		{"section 2 present", [][]byte{sec1(), secLocal(2), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7(nil)}},
	}
	for _, c := range cases {
		f, err := openBytes(assemble(0, c.secs))
		if err != nil {
			t.Logf("%-22s open=%v", c.name, err)
			continue
		}
		_, err = f.Message(0)
		t.Logf("%-22s open=nil, Message(0)=%v", c.name, err)
		f.Close()
	}
}

// gribw validation gaps
func TestProbeGribwValidation(t *testing.T) {
	base := func() *gribw.Message {
		m := gribw.New()
		_ = m.SetIdentification(&grib.Identification{Centre: 98, ReferenceTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
		_ = m.SetGridBytes(sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil))
		_ = m.SetProduct(&grib.Product{TemplateNumber: 0, ForecastTime: &grib.ForecastTime{Unit: 1, Value: 0}})
		return m
	}

	// nil message in File()
	if _, err := gribw.File(nil); err != nil {
		t.Logf("File(nil) -> %v", err)
	} else {
		t.Errorf("File(nil) accepted")
	}

	// zero messages
	out, err := gribw.File()
	t.Logf("File() with no messages -> len=%d err=%v", len(out), err)

	// Product with NV != 0
	p := &grib.Product{TemplateNumber: 0, NV: 5, ForecastTime: &grib.ForecastTime{Unit: 1, Value: 0}}
	m := base()
	if err := m.SetProduct(p); err != nil {
		t.Logf("SetProduct(NV=5) -> %v", err)
	} else {
		t.Errorf("SetProduct accepted NV=5")
	}

	// Product with an unknown template number inside the local band
	p2 := &grib.Product{TemplateNumber: 250, NV: 0, ForecastTime: &grib.ForecastTime{Unit: 1, Value: 0}}
	m2 := base()
	if err := m2.SetProduct(p2); err != nil {
		t.Logf("SetProduct(4.250) -> %v", err)
	} else {
		_, err := m2.Build()
		t.Logf("SetProduct(4.250) accepted, Build -> %v", err)
	}

	// time.Time always holds a NORMALISED calendar date: time.Date(2026,2,30)
	// is already 2026-03-02 by the time SetIdentification sees it, so an
	// impossible date cannot be expressed through this API. The writer's
	// calendar-consistency check is therefore defensive only (bug.md §4.6).
	// What IS enforceable is the representable range.
	id := &grib.Identification{Centre: 98, ReferenceTime: time.Date(2026, 2, 30, 0, 0, 0, 0, time.UTC)}
	m3 := base()
	if err := m3.SetIdentification(id); err != nil {
		t.Errorf("SetIdentification(normalised date) -> %v, want nil", err)
	} else {
		t.Logf("SetIdentification(Feb 30 -> %s) accepted; time.Time normalises (bug.md §4.6)",
			id.ReferenceTime.Format("2006-01-02"))
	}
	bad := &grib.Identification{Centre: 98, ReferenceTime: time.Date(12026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := base().SetIdentification(bad); err == nil {
		t.Errorf("SetIdentification accepted year 12026 (out of the 4-octet range)")
	} else {
		t.Logf("SetIdentification(year 12026) -> %v", err)
	}

	// Data section length prefix mismatch (checked) vs grid section (not checked)
	bad5 := make([]byte, 21)
	binary.BigEndian.PutUint32(bad5[0:4], 99)
	bad5[4] = 5
	m4 := base()
	err = m4.SetDataSection(bad5, nil)
	t.Logf("SetDataSection length mismatch -> %v", err)

	// payload size check for template 5.0 with a wrong bit count
	m5 := base()
	sec5 := sec5Simple(1, 0, 0, 0, 3, 0) // 1 value * 3 bits -> 1 byte expected
	if err := m5.SetDataSection(sec5, []byte{0}); err != nil {
		t.Fatalf("set: %v", err)
	}
	_, err = m5.Build()
	t.Logf("Build with 1-byte payload for 1 value x 3 bits -> %v", err)

	// Build with no data section at all
	m6 := base()
	_, err = m6.Build()
	t.Logf("Build without section 5 -> %v", err)
}

// ValuesAll error parity with the sequential path
func TestProbeValuesAllErrorParity(t *testing.T) {
	// message 0 decodes, message 1 fails (section 7 absent)
	good := assemble(0, [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7([]byte{1})})
	bad := assemble(0, [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0)})
	joined := append(append([]byte{}, good...), bad...)
	f, err := openBytes(joined)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	n, _ := f.NumMessages()
	t.Logf("messages=%d", n)
	for _, w := range []int{1, 4} {
		_, err := f.ValuesAll(w)
		t.Logf("ValuesAll(%d) -> %v", w, err)
	}
}

// A message whose section 0 total length disagrees with the actual bytes
func TestProbeTotalLengthMismatch(t *testing.T) {
	msg := assemble(0, [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7([]byte{1})})
	binary.BigEndian.PutUint64(msg[8:16], uint64(len(msg)+1))
	_, err := openBytes(msg)
	t.Logf("totalLength +1 -> %v", err)

	// 8-octet total length with the value in the low half only (legacy producers)
	m2 := make([]byte, len(msg))
	copy(m2, msg)
	binary.BigEndian.PutUint32(m2[8:12], uint32(len(msg)))
	binary.BigEndian.PutUint32(m2[12:16], 0)
	_, err = openBytes(m2)
	t.Logf("totalLength written in octets 9-12 only -> %v", err)
}

// Edition / discipline sanity
func TestProbeEditionAndDiscipline(t *testing.T) {
	msg := assemble(3, [][]byte{sec1(), sec3(1, 1, 0, 0, 0, 0, 0, 0, 64, nil), sec4(), sec5Simple(1, 0, 0, 0, 1, 0), sec7([]byte{1})})
	msg[7] = 1 // edition 1
	_, err := openBytes(msg)
	t.Logf("edition 1 -> %v", err)

	msg[7] = 255
	_, err = openBytes(msg)
	t.Logf("edition 255 -> %v", err)

	msg[7] = 2
	f, err := openBytes(msg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	t.Logf("discipline = %d", m.Discipline())
	if _, err := f.Message(0); err != nil {
		t.Fatalf("message: %v", err)
	}
	// Section(0) returns the fixed header with Length = total message length
	s, err := m.Section(0)
	if err == nil {
		t.Logf("Section(0): Number=%d Length=%d (Data len %d) — Length is the message total, not a section length", s.Number, s.Length, len(s.Data))
	}
}

// Walk.Tail / At contract
func TestProbeWalkContract(t *testing.T) {
	def, _ := spec.Template(5, 0)
	w, err := spec.NewWalk(def, sec5Simple(1, 0, 0, 0, 1, 0))
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for i := 0; i <= def.Fields[0].Count; i++ { // deliberately out of range
		v := w.At(i)
		if i >= w.Len() {
			t.Logf("At(%d) out of range -> %+v (no error, no panic)", i, v)
		}
	}
	// ByOctet inside a multi-octet field
	if v, ok := w.ByOctet(13); ok {
		t.Logf("ByOctet(13) (inside the 12-15 F32 field) -> %+v", v)
	} else {
		t.Log("ByOctet(13) -> not found (correct)")
	}
	// Tail with fromOctet == len(data)+1
	t.Logf("Tail(%d) -> %v", len(w.Data())+1, w.Tail(len(w.Data())+1))
}
