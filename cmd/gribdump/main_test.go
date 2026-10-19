// Regression tests for the gribdump CLI: JSON-safe float encoding,
// -section flag parsing, and error accumulation.

package main

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
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
