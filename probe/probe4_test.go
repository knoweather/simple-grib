package probe

import (
	"math/rand"
	"testing"

	"github.com/knoweather/simple-grib/internal/octetio"
	"github.com/knoweather/simple-grib/pack"
	"github.com/knoweather/simple-grib/spec"
)

// Fuzz the full decode path (structure + pack) for panics.
func TestProbeFuzzDecodePath(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	templates := []int{0, 2, 3, 4, 40, 42, 49152, 253}
	panics := 0
	for iter := 0; iter < 30000; iter++ {
		tmpl := templates[rng.Intn(len(templates))]
		def, ok := spec.Template(5, tmpl)
		if !ok {
			continue
		}
		// Build a section 5 of the template's minimum size, then a random payload.
		sec5 := make([]byte, 11)
		binaryPutU32(sec5, 0, 11)
		sec5[4] = 5
		binaryPutU32(sec5, 5, uint32(rng.Intn(64)))
		binaryPutU16(sec5, 9, uint16(tmpl))
		for _, f := range def.Fields {
			if f.Start+1 > len(sec5) {
				sec5 = append(sec5, make([]byte, f.Start+1-len(sec5))...)
			}
			if f.Kind == spec.KindU8 && f.Start < len(sec5) {
				sec5[f.Start] = byte(rng.Intn(256))
			} else if f.Kind == spec.KindU16 && f.Start+2 <= len(sec5) {
				sec5[f.Start] = byte(rng.Intn(256))
				sec5[f.Start+1] = byte(rng.Intn(256))
			} else if f.Kind == spec.KindU32 && f.Start+4 <= len(sec5) {
				for b := 0; b < 4; b++ {
					sec5[f.Start+b] = byte(rng.Intn(256))
				}
			} else if f.Kind == spec.KindF32 && f.Start+4 <= len(sec5) {
				for b := 0; b < 4; b++ {
					sec5[f.Start+b] = byte(rng.Intn(256))
				}
			}
		}
		nv := int(binaryGetU32(sec5, 5))
		if nv > 2000 {
			continue
		}
		payload := make([]byte, rng.Intn(64))
		for i := range payload {
			payload[i] = byte(rng.Intn(256))
		}
		w, err := spec.NewWalk(def, sec5)
		if err != nil {
			continue
		}
		bitmap := make([]byte, rng.Intn(16))
		for i := range bitmap {
			bitmap[i] = byte(rng.Intn(256))
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					panics++
					t.Errorf("PANIC (iter %d, template %d, numValues %d): %v", iter, tmpl, nv, r)
				}
			}()
			_, err := pack.Unpack(&pack.Context{
				TemplateNumber: tmpl,
				Fields:         w,
				Data:           payload,
				NumValues:      nv,
				Bitmap:         octetio.NewBits(bitmap),
				GridPoints:     rng.Intn(2000),
			})
			_ = err
		}()
		if panics > 10 {
			break
		}
	}
	if panics == 0 {
		t.Log("decode-path fuzz: no panics in 30000 iterations")
	}
}

func binaryPutU32(b []byte, off int, v uint32) {
	b[off] = byte(v >> 24)
	b[off+1] = byte(v >> 16)
	b[off+2] = byte(v >> 8)
	b[off+3] = byte(v)
}

func binaryGetU32(b []byte, off int) uint32 {
	return uint32(b[off])<<24 | uint32(b[off+1])<<16 | uint32(b[off+2])<<8 | uint32(b[off+3])
}

func binaryPutU16(b []byte, off int, v uint16) {
	b[off] = byte(v >> 8)
	b[off+1] = byte(v)
}
