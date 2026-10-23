package grib_test

import (
	"fmt"
	"log"

	"github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/paramdb"
)

// Example is the README quick-start, kept compilable so the docs cannot drift:
// open a file, walk its messages, and read the ecCodes-compatible parameter
// names (Product.ParameterName is the table 4.2 title; paramdb answers the
// shortName / units keys). The corpus is the 187-byte ECMWF test file in this
// repository, so the output is deterministic.
func Example() {
	f, err := grib.Open("testdata/p6-a-xcheck/p6a_ll_1xn.grib2")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	for m, err := range f.Messages() {
		if err != nil {
			log.Fatal(err)
		}
		p, err := m.Product()
		if err != nil {
			log.Fatal(err)
		}
		q, err := paramdb.QueryFrom(m)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(p.ParameterName, paramdb.ShortNameOrUnknown(q), paramdb.UnitsOrUnknown(q))
	}

	// Output: Temperature t K
}
