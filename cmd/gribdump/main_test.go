// Regression tests for the gribdump CLI: JSON-safe float encoding,
// -section flag parsing, error accumulation, and -webp extraction.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"testing"

	simplewebp "github.com/knoweather/simple-webp"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/gribw"
)

// Non-finite float values must encode as strings so a single NaN/Inf cannot
// abort the whole JSON document.
func TestJSONSafeFloats(t *testing.T) {
	cases := []struct {
		v    float32Val
		want string
	}{
		{float32Val(273.15), "273.15"},
		{float32Val(float32(math.NaN())), `"NaN"`},
		{float32Val(float32(math.Inf(1))), `"+Inf"`},
		{float32Val(float32(math.Inf(-1))), `"-Inf"`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil {
			t.Errorf("Marshal(%v): %v", float32(c.v), err)
			continue
		}
		if string(b) != c.want {
			t.Errorf("Marshal(%v) = %s, want %s", float32(c.v), b, c.want)
		}
	}

	// The whole document must survive a slice containing NaN/Inf.
	if _, err := json.Marshal([]float32Val{
		float32Val(1), float32Val(float32(math.NaN())), float32Val(float32(math.Inf(1))),
	}); err != nil {
		t.Errorf("marshal of a mixed slice failed: %v", err)
	}

	if b, err := json.Marshal(float64Val(math.Inf(1))); err != nil || string(b) != `"+Inf"` {
		t.Errorf("float64Val(+Inf) = %s, err=%v", b, err)
	}
	if b, err := json.Marshal(float64Val(1.5)); err != nil || string(b) != "1.5" {
		t.Errorf("float64Val(1.5) = %s, err=%v", b, err)
	}
}

// -section must reject trailing junk and out-of-range numbers.
func TestSectionFlagParsing(t *testing.T) {
	var s sectionFlag
	for _, ok := range []string{"0", "7", "3"} {
		if err := s.Set(ok); err != nil {
			t.Errorf("Set(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"3abc", "8", "-1", "", "x"} {
		var f sectionFlag
		if err := f.Set(bad); err == nil {
			t.Errorf("Set(%q) = nil, want an error", bad)
		}
	}
}

// Per-component errors accumulate rather than overwrite each other.
func TestAddErrAccumulates(t *testing.T) {
	var mo msgOutput
	addErr(&mo, "grid", errors.New("bad grid"))
	addErr(&mo, "product", errors.New("bad product"))
	want := "grid: bad grid; product: bad product"
	if mo.Error != want {
		t.Errorf("Error = %q, want %q", mo.Error, want)
	}
}

// --- -webp extraction -------------------------------------------------------

// encodeTestWebP builds a small opaque WebP image with distinct pixels.
func encodeTestWebP(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(20 * x), G: uint8(30 * y), B: 40, A: 0xFF})
		}
	}
	out, err := simplewebp.EncodeBytes(img, nil)
	if err != nil {
		t.Fatalf("EncodeBytes: %v", err)
	}
	return out
}

// buildWebPGribFile builds a multi-message GRIB2 file whose payloads are the
// given WebP images, reusing the small fixture for identification/grid/product.
func buildWebPGribFile(t *testing.T, payloads ...[]byte) []byte {
	t.Helper()
	fl, err := grib.Open("../../testdata/gribw-xcheck/t20_grid_3x3.grib2")
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

	msgs := make([]*gribw.Message, 0, len(payloads))
	for _, payload := range payloads {
		w := gribw.New()
		if err := w.SetDiscipline(m.Discipline()); err != nil {
			t.Fatalf("SetDiscipline: %v", err)
		}
		if err := w.SetIdentification(id); err != nil {
			t.Fatalf("SetIdentification: %v", err)
		}
		if err := w.SetGrid(g); err != nil {
			t.Fatalf("SetGrid: %v", err)
		}
		if err := w.SetProduct(p); err != nil {
			t.Fatalf("SetProduct: %v", err)
		}
		if err := w.SetWebP(payload); err != nil {
			t.Fatalf("SetWebP: %v", err)
		}
		msgs = append(msgs, w)
	}
	data, err := gribw.File(msgs...)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	return data
}

func openReader(t *testing.T, data []byte) *grib.File {
	t.Helper()
	f, err := grib.OpenReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	return f
}

func TestExtractWebPSingleMessage(t *testing.T) {
	webp := encodeTestWebP(t, 4, 3)
	f := openReader(t, buildWebPGribFile(t, webp))
	defer f.Close()

	dest := filepath.Join(t.TempDir(), "out.webp")
	if err := extractWebP(f, 1, dest); err != nil {
		t.Fatalf("extractWebP: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, webp) {
		t.Fatalf("extracted %d bytes, want the %d written bytes verbatim", len(got), len(webp))
	}
}

func TestExtractWebPMultiMessage(t *testing.T) {
	first := encodeTestWebP(t, 4, 3)
	second := encodeTestWebP(t, 5, 2)
	f := openReader(t, buildWebPGribFile(t, first, second))
	defer f.Close()

	dir := filepath.Join(t.TempDir(), "images")
	if err := extractWebP(f, 2, dir); err != nil {
		t.Fatalf("extractWebP: %v", err)
	}
	for i, want := range [][]byte{first, second} {
		name := filepath.Join(dir, fmt.Sprintf("msg%03d.webp", i))
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: %d bytes, want %d", name, len(got), len(want))
		}
	}
}

func TestExtractWebPRejectsNonWebPInput(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/gribw-xcheck/t20_grid_3x3.grib2")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	f := openReader(t, raw)
	defer f.Close()

	err = extractWebP(f, 1, filepath.Join(t.TempDir(), "out.webp"))
	if err == nil {
		t.Fatal("extractWebP accepted a non-WebP message; want an error")
	}
}
