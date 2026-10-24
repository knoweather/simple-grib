package grib_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"strings"
	"testing"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/gribw"
)

// pngGrayBytes encodes raw 8-bit grayscale samples as a PNG.
func pngGrayBytes(t *testing.T, width, height int, samples []uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, width, height))
	copy(img.Pix, samples)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// buildPNGMessage writes a template 5.41 message with the given PNG payload.
// The section 5 header comes from EncodeSimple (same field layout) with the
// template number switched to 41.
func buildPNGMessage(t *testing.T, numValues int, bits int, pngData []byte) []byte {
	t.Helper()
	fl, err := grib.Open("testdata/gribw-xcheck/t20_grid_3x3.grib2")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer fl.Close()
	m, err := fl.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	g, _ := m.Grid()
	id, _ := m.Identification()
	p, _ := m.Product()

	vals := make([]float32, numValues)
	for i := range vals {
		vals[i] = float32(i)
	}
	sec5, _, err := gribw.EncodeSimple(vals, gribw.SimpleParams{Bits: bits})
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	binary.BigEndian.PutUint16(sec5[9:11], 41) // template 5.41 (PNG)

	msg := gribw.New()
	if err := msg.SetDiscipline(m.Discipline()); err != nil {
		t.Fatalf("SetDiscipline: %v", err)
	}
	if err := msg.SetIdentification(id); err != nil {
		t.Fatalf("SetIdentification: %v", err)
	}
	if err := msg.SetGrid(g); err != nil {
		t.Fatalf("SetGrid: %v", err)
	}
	if err := msg.SetProduct(p); err != nil {
		t.Fatalf("SetProduct: %v", err)
	}
	if err := msg.SetDataSection(sec5, pngData); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	data, err := gribw.File(msg)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	return data
}

func TestPNGEndToEnd(t *testing.T) {
	raw := []uint8{0, 1, 2, 3, 4, 5, 6, 7, 8}
	data := buildPNGMessage(t, len(raw), 8, pngGrayBytes(t, 3, 3, raw))

	f, err := grib.OpenReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	got, err := m.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if len(got) != len(raw) {
		t.Fatalf("got %d values, want %d", len(got), len(raw))
	}
	for i, want := range raw {
		if got[i] != float32(want) {
			t.Errorf("value[%d] = %v, want %v", i, got[i], want)
		}
	}
}

func TestPNGWriterRejectsShortCanvas(t *testing.T) {
	// 4 samples in the PNG, 9 declared values.
	pngData := pngGrayBytes(t, 2, 2, []uint8{1, 2, 3, 4})
	fl, err := grib.Open("testdata/gribw-xcheck/t20_grid_3x3.grib2")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer fl.Close()
	m, _ := fl.Message(0)
	g, _ := m.Grid()
	id, _ := m.Identification()
	p, _ := m.Product()
	vals := make([]float32, 9)
	sec5, _, err := gribw.EncodeSimple(vals, gribw.SimpleParams{Bits: 8})
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	binary.BigEndian.PutUint16(sec5[9:11], 41)

	msg := gribw.New()
	_ = msg.SetDiscipline(m.Discipline())
	_ = msg.SetIdentification(id)
	_ = msg.SetGrid(g)
	_ = msg.SetProduct(p)
	if err := msg.SetDataSection(sec5, pngData); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	_, err = gribw.File(msg)
	if err == nil || !strings.Contains(err.Error(), "numberOfValues") {
		t.Fatalf("File error = %v, want a canvas/numberOfValues error", err)
	}
}
