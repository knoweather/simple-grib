package pack

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/knoweather/simple-grib/spec"
)

// loadPNGFixture reads testdata/png-xcheck/depthN.{png,txt}: an independently
// encoded grayscale PNG (pypng for 1/2/4-bit, Pillow for 8/16-bit) plus its raw
// samples in scan order.
func loadPNGFixture(t *testing.T, depth int) (pngData []byte, samples []uint32) {
	t.Helper()
	return loadPNGNamed(t, "depth"+strconv.Itoa(depth))
}

func loadPNGNamed(t *testing.T, name string) (pngData []byte, samples []uint32) {
	t.Helper()
	pngData, err := os.ReadFile("../testdata/png-xcheck/" + name + ".png")
	if err != nil {
		t.Fatalf("read %s.png: %v", name, err)
	}
	txt, err := os.ReadFile("../testdata/png-xcheck/" + name + ".txt")
	if err != nil {
		t.Fatalf("read %s.txt: %v", name, err)
	}
	for _, line := range strings.Fields(string(txt)) {
		v, err := strconv.ParseUint(line, 10, 32)
		if err != nil {
			t.Fatalf("%s.txt: %q: %v", name, line, err)
		}
		samples = append(samples, uint32(v))
	}
	return pngData, samples
}

// TestPNGExternalFixtures decodes PNGs produced by other encoders and compares
// every sample with the recorded pattern. This is the encoder-independence
// check: a self round-trip through gribw.SetPNG would not catch a mistake that
// both sides share.
func TestPNGExternalFixtures(t *testing.T) {
	def, ok := spec.Template(5, TemplatePNG)
	if !ok {
		t.Fatal("no spec definition for template 5.41")
	}
	for _, depth := range []int{1, 2, 4, 8, 16} {
		depth := depth
		t.Run("depth"+strconv.Itoa(depth), func(t *testing.T) {
			pngData, samples := loadPNGFixture(t, depth)
			sec5 := pngSec5(t, len(samples), 0, 0, 0, uint8(depth), 0)
			w, err := spec.NewWalk(def, sec5)
			if err != nil {
				t.Fatalf("NewWalk: %v", err)
			}
			got, err := Unpack(&Context{
				TemplateNumber: TemplatePNG,
				Fields:         w,
				Data:           pngData,
				NumValues:      len(samples),
			})
			if err != nil {
				t.Fatalf("Unpack: %v", err)
			}
			if len(got) != len(samples) {
				t.Fatalf("got %d values, want %d", len(got), len(samples))
			}
			for i, want := range samples {
				if got[i] != float32(want) {
					t.Errorf("value[%d] = %v, want %v", i, got[i], want)
				}
			}
			// Header sanity: the fixture really is the depth under test.
			if int(pngData[24]) != depth {
				t.Errorf("fixture IHDR bit depth = %d, want %d", pngData[24], depth)
			}
		})
	}
}

// TestPNGInterlacedFixtures covers Adam7-interlaced images at 8 and 4 bits:
// the standard library de-interlaces, so sample extraction must still be
// row-major and packed MSB-first.
func TestPNGInterlacedFixtures(t *testing.T) {
	def, ok := spec.Template(5, TemplatePNG)
	if !ok {
		t.Fatal("no spec definition for template 5.41")
	}
	for _, tc := range []struct {
		name  string
		depth int
	}{
		{"interlace8", 8},
		{"interlace4", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pngData, samples := loadPNGNamed(t, tc.name)
			if int(pngData[28]) != 1 {
				t.Fatalf("%s.png is not interlaced (IHDR byte 28 = %d)", tc.name, pngData[28])
			}
			sec5 := pngSec5(t, len(samples), 0, 0, 0, uint8(tc.depth), 0)
			w, err := spec.NewWalk(def, sec5)
			if err != nil {
				t.Fatalf("NewWalk: %v", err)
			}
			got, err := Unpack(&Context{
				TemplateNumber: TemplatePNG,
				Fields:         w,
				Data:           pngData,
				NumValues:      len(samples),
			})
			if err != nil {
				t.Fatalf("Unpack: %v", err)
			}
			for i, want := range samples {
				if got[i] != float32(want) {
					t.Errorf("value[%d] = %v, want %v", i, got[i], want)
				}
			}
		})
	}
}

// TestPNGSimplePngFixtures decodes PNGs written by the sister library
// simple-png (v1.4.0): 8-bit, Adam7-interlaced 8-bit and 16-bit. It keeps the
// fixture-based cross-check in CI without adding simple-png as a module
// dependency of simple-grib.
func TestPNGSimplePngFixtures(t *testing.T) {
	def, ok := spec.Template(5, TemplatePNG)
	if !ok {
		t.Fatal("no spec definition for template 5.41")
	}
	for _, tc := range []struct {
		name  string
		depth int
	}{
		{"simplepng8", 8},
		{"simplepng8i", 8},
		{"simplepng16", 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pngData, samples := loadPNGNamed(t, tc.name)
			sec5 := pngSec5(t, len(samples), 0, 0, 0, uint8(tc.depth), 0)
			w, err := spec.NewWalk(def, sec5)
			if err != nil {
				t.Fatalf("NewWalk: %v", err)
			}
			got, err := Unpack(&Context{
				TemplateNumber: TemplatePNG,
				Fields:         w,
				Data:           pngData,
				NumValues:      len(samples),
			})
			if err != nil {
				t.Fatalf("Unpack: %v", err)
			}
			for i, want := range samples {
				if got[i] != float32(want) {
					t.Errorf("value[%d] = %v, want %v", i, got[i], want)
				}
			}
		})
	}
}
