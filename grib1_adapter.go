package grib

import (
	"github.com/knoweather/simple-grib/grib1"
)

// This file adapts GRIB1 messages to the root package's unified types.
//
// Why adapt here instead of letting grib1 return root-package types directly:
// grib1 depends on grid to compute coordinates, and the root package also
// depends on both grid and grib1 — if grib1 depended back on the root package
// the imports would cycle. So grib1 stays self-contained and describes only
// GRIB1, and this package maps the two views onto one set of accessors.
//
// Mapping principle: common concepts get the common value; concepts that do
// not exist in GRIB1 get explicit "not applicable". The functions below use
// -1 for that (not 0 — 0 is a legal value in most code tables, so using it as
// a placeholder would fabricate data that looks real).

// identificationFromGRIB1 maps the GRIB1 section 1 onto Identification.
func identificationFromGRIB1(g *grib1.Message) *Identification {
	id := g.Identification()
	out := &Identification{
		Edition:            1,
		Centre:             id.Centre,
		CentreName:         id.CentreName,
		SubCentre:          id.SubCentre,
		MasterTableVersion: id.TableVersion,
		ReferenceTime:      id.ReferenceTime,
		// GRIB1 has no "production status" or "type of data" concepts
		// (they are GRIB2 tables 1.3/1.4); -1 marks them not applicable.
		ProductionStatus:    -1,
		TypeOfProcessedData: -1,
	}
	return out
}

// productFromGRIB1 maps the GRIB1 product description onto Product.
//
// Note the code spaces that differ and must not be mixed:
//   - ParameterNumber is a GRIB1 table 2 code (not GRIB2 table 4.2);
//   - Level1/2 Type is a GRIB1 table 3 code (not GRIB2 table 4.5);
//   - TypeOfGeneratingProcess is the GRIB1 "generating process identifier"
//     (PDS octet 6, centre-defined), NOT a GRIB2 table 4.3 code.
//
// That is why Product.Edition matters: interpret the codes against the tables
// of the corresponding edition.
func productFromGRIB1(g *grib1.Message) *Product {
	p := g.Product()
	out := &Product{
		Edition: 1,
		// GRIB1 has no product definition template number (a GRIB2 concept).
		TemplateNumber:          -1,
		ParameterNumber:         p.Parameter,
		ParameterName:           p.ParameterName,
		TypeOfGeneratingProcess: g.Identification().GeneratingProcess,
	}
	out.Level1 = &Level{Type: p.LevelType, Scale: float64(p.LevelTop), HasValue: !p.LevelMissing}
	if p.LayerRange {
		out.Level2 = &Level{Type: p.LevelType, Scale: float64(p.LevelBottom), HasValue: !p.LevelMissing}
	}
	if d, ok := p.ForecastTime(); ok {
		out.ForecastTime = &ForecastTime{Unit: p.TimeRangeUnit, Raw: uint32(p.P1), Value: d}
	} else {
		// Units without a fixed length (month/year): keep the raw count,
		// leave Value 0, and let the caller interpret TimeRangeUnit — the
		// same convention as the GRIB2-side ForecastTime.
		out.ForecastTime = &ForecastTime{Unit: p.TimeRangeUnit, Raw: uint32(p.P1)}
	}
	return out
}

// dataRepFromGRIB1 maps the GRIB1 data section onto DataRep.
func dataRepFromGRIB1(g *grib1.Message) *DataRep {
	out := &DataRep{
		// GRIB1 has exactly one data representation type ("simple packing";
		// the section 4/BDS flags already reject second-order packing,
		// spherical harmonics, and IEEE in parseBDS), so there is no
		// template number to report.
		TemplateNumber: -1,
	}
	if n, err := g.CodedValues(); err == nil && n >= 0 {
		out.NumValues = n
		return out
	}
	// A GRIB1 constant field (bit width 0) has no countable coded values:
	// count the bitmap first, else fall back to the grid point count. If
	// neither is known (e.g. no GDS, no BMS), keep 0 rather than inventing
	// a value count.
	if bitmap := g.Bitmap(); bitmap != nil {
		out.NumValues = bitmap.Count()
		return out
	}
	if grid, err := g.Grid(); err == nil {
		out.NumValues = grid.NumPoints()
	}
	return out
}
