package grib

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// TestErrorsUseASinglePrefix pins the error-prefix contract: the exported
// sentinels already begin with "grib: ", so wrapping them must not repeat it.
func TestErrorsUseASinglePrefix(t *testing.T) {
	header := func(edition byte, total uint64) []byte {
		b := make([]byte, 16)
		copy(b, "GRIB")
		b[7] = edition
		binary.BigEndian.PutUint64(b[8:], total)
		return b
	}

	// A GRIB2 message whose first section claims a length below the 5-octet
	// minimum, so section parsing fails with ErrBadSectionLength.
	badSection := append(header(2, 21), 3, 0, 0, 0, 1)

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"shorter than a section 0 header", []byte("GRIB"), ErrTruncated},
		{"wrong magic", append([]byte("BUFR"), make([]byte, 12)...), ErrNotGRIB2},
		{"unsupported edition", header(3, 16), ErrUnsupportedEdition},
		{"truncated message body", header(2, 128), ErrTruncated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := OpenReader(bytes.NewReader(tc.data), int64(len(tc.data)))
			checkErrorPrefix(t, err, tc.want)
		})
	}

	t.Run("bad section length", func(t *testing.T) {
		f, err := OpenReader(bytes.NewReader(badSection), int64(len(badSection)))
		if err != nil {
			t.Fatalf("OpenReader: %v (indexing should succeed)", err)
		}
		_, err = f.Message(0)
		checkErrorPrefix(t, err, ErrBadSectionLength)
	})
}

func checkErrorPrefix(t *testing.T, err, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error wrapping %v", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want it to wrap %v", err, want)
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "grib: ") {
		t.Errorf("error %q does not start with %q", msg, "grib: ")
	}
	if strings.Contains(msg, "grib: grib:") {
		t.Errorf("error %q repeats the package prefix", msg)
	}
}
