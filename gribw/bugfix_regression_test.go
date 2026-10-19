// Regression tests for the gribw writer: the writer must never emit a file the
// reader would refuse, and must never index out of range on a hand-built
// section.

package gribw

import (
	"encoding/binary"
	"testing"
)

// A section 5 too short for its template header must be rejected, not indexed
// out of range (an 11-byte section 5 has no room for bitsPerValue at octet 20
// or precision at octet 12).
func TestBuildShortSection5FailsLoud(t *testing.T) {
	g := fixtureGrid(t)
	n := g.NumPoints()
	for _, tmpl := range []uint16{0, 4} {
		sec5 := make([]byte, 11)
		binary.BigEndian.PutUint32(sec5[0:4], 11)
		sec5[4] = 5
		binary.BigEndian.PutUint32(sec5[5:9], uint32(n))
		binary.BigEndian.PutUint16(sec5[9:11], tmpl)

		m := New()
		if err := m.SetIdentification(canonicalID()); err != nil {
			t.Fatalf("id: %v", err)
		}
		if err := m.SetGrid(g); err != nil {
			t.Fatalf("grid: %v", err)
		}
		if err := m.SetProduct(canonicalProduct()); err != nil {
			t.Fatalf("product: %v", err)
		}
		if err := m.SetDataSection(sec5, nil); err != nil {
			t.Fatalf("SetDataSection: %v", err)
		}
		if _, err := m.Build(); err == nil {
			t.Errorf("template %d: Build accepted an 11-byte section 5; want a bounds error", tmpl)
		} else {
			t.Logf("template %d: Build -> %v", tmpl, err)
		}
	}
}

// File() with no messages must fail rather than return an empty (unreadable)
// body, which the reader rejects.
func TestFileRequiresAtLeastOneMessage(t *testing.T) {
	if _, err := File(); err == nil {
		t.Errorf("File() with no messages: got nil error, want a fail-loud error")
	}
}

// A grid whose template the reader cannot turn into coordinates must be
// refused rather than written out from the declared count.
func TestBuildUnimplementedGridRejected(t *testing.T) {
	sec3 := make([]byte, 200)
	binary.BigEndian.PutUint32(sec3[0:4], 200)
	sec3[4] = 3
	binary.BigEndian.PutUint16(sec3[12:14], 10) // grid template 3.10 (metadata-only)
	binary.BigEndian.PutUint32(sec3[30:34], 5)
	binary.BigEndian.PutUint32(sec3[34:38], 2)

	m := New()
	if err := m.SetIdentification(canonicalID()); err != nil {
		t.Fatalf("id: %v", err)
	}
	if err := m.SetGridBytes(sec3); err != nil {
		t.Fatalf("SetGridBytes: %v", err)
	}
	if err := m.SetProduct(canonicalProduct()); err != nil {
		t.Fatalf("product: %v", err)
	}
	sec5, payload, err := EncodeSimple(make([]float32, 10), SimpleParams{Bits: 1})
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	if err := m.SetDataSection(sec5, payload); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	if _, err := m.Build(); err == nil {
		t.Errorf("Build accepted an unimplemented grid template; want a fail-loud error")
	} else {
		t.Logf("Build with an unimplemented grid template -> %v", err)
	}
}

// The writer rejects bitsPerValue > 32 (WMO table 5.5) and never emits a file
// the reader would refuse.
func TestBuildRejectsBitsAbove32(t *testing.T) {
	m := New()
	if err := m.SetIdentification(canonicalID()); err != nil {
		t.Fatalf("id: %v", err)
	}
	if err := m.SetGrid(fixtureGrid(t)); err != nil {
		t.Fatalf("grid: %v", err)
	}
	if err := m.SetProduct(canonicalProduct()); err != nil {
		t.Fatalf("product: %v", err)
	}
	sec5 := simpleSec5(t, 0, 0, 0, 33)
	if err := m.SetDataSection(sec5, make([]byte, 40)); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	if _, err := m.Build(); err == nil {
		t.Errorf("Build accepted bitsPerValue = 33; want a fail-loud error")
	}
}
