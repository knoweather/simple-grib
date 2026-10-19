// Code tables for GRIB1: tables 1 / 3 / 4 / 5 / 6 / 7 / 8 / 11 and common table C-1.
//
// Same philosophy as the GRIB2 side's "spec as data": entries are not typed from
// memory but transcribed one-for-one from ecCodes' machine-readable definition
// files (Apache-2.0), with format conversion only and no content edits. Names keep
// the official WMO English wording on purpose — they are spec identifiers, not
// display copy.
//
// To regenerate, take definitions/grib1/*.table and definitions/common/c-1.table
// from an ecCodes source tree as the single source of truth and re-run the
// transcription step after changing it.
//
// Not included: table 2 (parameter tables). It varies by centre and table version,
// is dominated by centre-local codes, and does not fit the built-in scheme; this
// package returns "" for codes outside the standard international exchange tables
// and callers interpret them by centre/version themselves (see Product.ParameterName).
package grib1

// codeEntry is one row of a "code → name" table.
type codeEntry struct {
	code int
	name string
}

// bitFlagEntry is one row of a "bit number + bit value → meaning" table. Bit
// numbers follow the WMO convention and start at 1 (1 = MSB).
type bitFlagEntry struct {
	bit   int
	value int
	name  string
}

// lookupCode finds a code's name; unknown codes (reserved, local or unallocated)
// return "".
func lookupCode(t []codeEntry, code int) string {
	for _, e := range t {
		if e.code == code {
			return e.name
		}
	}
	return ""
}

// lookupBitFlag finds the meaning of a bit taking a value; unknown combinations
// return "".
func lookupBitFlag(t []bitFlagEntry, bit, value int) string {
	for _, e := range t {
		if e.bit == bit && e.value == value {
			return e.name
		}
	}
	return ""
}

// Table 1: section 1 flags (whether section 2 / section 3 are present).
// Source: transcribed from ecCodes definitions/grib1/1.table.
var table1Section1Flags = []bitFlagEntry{
	{1, 0, "Section 2 omited"},
	{1, 1, "Section 2 included"},
	{2, 0, "Section 3 omited"},
	{2, 1, "Section 3 included"},
}

// Table 3: fixed levels / layer pairs (level indicators).
// Source: transcribed from ecCodes definitions/grib1/3.table; names keep the WMO
// English wording.
var table3LevelTypes = []codeEntry{
	{0, "Reserved"},
	{1, "Surface (of the Earth, which includes sea surface)"},
	{2, "Cloud base level"},
	{3, "Cloud top level"},
	{4, "0 deg (C) isotherm level"},
	{5, "Adiabatic condensation level (parcel lifted from surface)"},
	{6, "Maximum wind speed level"},
	{7, "Tropopause level"},
	{8, "Nominal top of atmosphere"},
	{9, "Sea bottom"},
	{20, "Isothermal level Temperature in 1/100 K"},
	{100, "Isobaric level pressure in hectoPascals (hPa) (2 octets)"},
	{101, "Layer between two isobaric levels pressure of top (kPa) pressure of bottom (kPa)"},
	{102, "Mean sea level 0 0"},
	{103, "Fixed height level height above mean sea level (MSL) in meters"},
	{104, "Layer between two specfied altitudes above mean sea level - altitude of top, altitude of bottom (hm)"},
	{105, "Fixed height above ground height in meters (2 octets)"},
	{106, "Layer between two height levels above ground - height of top, height of bottom (hm)"},
	{107, "Sigma level sigma value in 1/10000 (2 octets)"},
	{108, "Layer between two sigma levels sigma value at top in 1/100 sigma value at bottom in 1/100"},
	{109, "Hybrid level level number (2 octets)"},
	{110, "Layer between two hybrid levels level number of top level number of bottom"},
	{111, "Depth below land surface centimeters (2 octets)"},
	{112, "Layer between two depths below land surface - depth of upper surface, depth of lower surface (cm)"},
	{113, "Isentropic (theta) level Potential Temp. degrees K (2 octets)"},
	{114, "Layer between two isentropic levels 475K minus theta of top in Deg. K 475K minus theta of bottom in Deg. K"},
	{115, "Level at specified pressure difference from ground to level hPa (2 octets)"},
	{116, "Layer between two levels at specified pressure differences from ground to levels pressure difference from ground to top level hPa pressure difference from ground to bottom level hPa"},
	{117, "Potential vorticity surface 10-9 K m2 kg-1 s-1"},
	{119, "ETA level: ETA value in 1/10000 (2 octets)"},
	{120, "Layer between two ETA levels: ETA value at top of layer in 1/100, ETA value at bottom of layer in 1/100"},
	{121, "Layer between two isobaric surfaces (high precision) 1100 hPa minus pressure of top, in hPa 1100 hPa minus pressure of bottom, in hPa"},
	{125, "Height level above ground (high precision) centimeters (2 octets)"},
	{128, "Layer between two sigma levels (high precision) 1.1 minus sigma of top, in 1/1000 of sigma 1.1 minus sigma of bottom, in 1/1000 of sigma"},
	{141, "Layer between two isobaric surfaces (mixed precision) pressure of top, in kPa 1100hPa minus pressure of bottom, in hPa"},
	{160, "Depth below sea level meters (2 octets)"},
	{200, "Entire atmosphere considered as a single layer 0 (2 octets)"},
	{201, "Entire ocean considered as a single layer 0 (2 octets)"},
	{210, "Isobaric surface (Pa) (ECMWF extension)"},
	{255, "Indicates a missing value"},
}

// Table 4: time units.
// Source: transcribed from ecCodes definitions/grib1/4.table; names keep the WMO
// English wording.
var table4TimeUnits = []codeEntry{
	{0, "Minute"},
	{1, "Hour"},
	{2, "Day"},
	{3, "Month"},
	{4, "Year"},
	{5, "Decade"},
	{6, "Normal (30 years)"},
	{7, "Century"},
	{10, "3 hours"},
	{11, "6 hours"},
	{12, "12 hours"},
	{13, "15 minutes"},
	{14, "30 minutes"},
	{254, "Second"},
}

// Table 5: time range indicators.
// Source: transcribed from ecCodes definitions/grib1/5.table; names keep the WMO
// English wording.
var table5TimeRanges = []codeEntry{
	{0, "Forecast product valid at reference time + P1 (P1>0)"},
	{1, "Initialized analysis product for reference time (P1=0)."},
	{2, "Product with a valid time ranging between reference time + P1 and reference time + P2"},
	{3, "Average (reference time + P1 to reference time + P2)"},
	{4, "Accumulation (reference time + P1 to reference time + P2) product considered valid at reference time + P2"},
	{5, "Difference (reference time + P2 minus reference time + P1) product considered valid at reference time + P2"},
	{6, "Average (reference time - P1 to reference time - P2)"},
	{7, "Average (reference time - P1 to reference time + P2)"},
	{10, "P1 occupies octets 19 and 20; product valid at reference time + P1"},
	{51, "Climatological Mean Value:"},
	{113, "Average of N forecasts (or initialized analyses); each product has forecast period of P1 (P1=0 for initialized analyses); products have reference times at intervals of P2, beginning at the given reference time."},
	{114, "Accumulation of N forecasts (or initialized analyses); each product has forecast period of P1 (P1=0 for initialized analyses); products have reference times at intervals of P2, beginning at the given reference time."},
	{115, "Average of N forecasts, all with the same reference time; the first has a forecast period of P1, the remaining forecasts follow at intervals of P2."},
	{116, "Accumulation of N forecasts, all with the same reference time; the first has a forecast period of P1, the remaining follow at intervals of P2."},
	{117, "Average of N forecasts, the first has a period of P1, the subsequent ones have forecast periods reduced from the previous one by an interval of P2; the reference time for the first is given in octets 13- 17, the subsequent ones have reference times increased from the previous one by an interval of P2. Thus all the forecasts have the same valid time, given by the initial reference time + P1."},
	{118, "Temporal variance, or covariance, of N initialized analyses; each product has forecast period P1=0; products have reference times at intervals of P2, beginning at the given reference time."},
	{119, "Standard deviation of N forecasts, all with the same reference time with respect to the time average of forecasts; the first forecast has a forecast period of P1, the remaining forecasts follow at intervals of P2"},
	{123, "Average of N uninitialized analyses, starting at the reference time, at intervals of P2."},
	{124, "Accumulation of N uninitialized analyses, starting at the reference time, at intervals of P2."},
	{125, "Standard deviation of N forecasts, all with the same reference time with respect to time average of the time tendency of forecasts; the first forecast has a forecast period of P1, the remaining forecasts follow at intervals of P2"},
}

// Table 6: data representation types (grid types).
// Source: transcribed from ecCodes definitions/grib1/6.table; names keep the WMO
// English wording.
var table6GridTypes = []codeEntry{
	{0, "Latitude/Longitude Grid"},
	{1, "Mercator Projection Grid"},
	{2, "Gnomonic Projection Grid"},
	{3, "Lambert Conformal"},
	{4, "Gaussian Latitude/Longitude Grid"},
	{5, "Polar Stereographic Projection Grid"},
	{6, "Universal Transverse Mercator"},
	{7, "Simple polyconic projection"},
	{8, "Albers equal-area, secant or tangent, conic or bi-polar"},
	{9, "Miller's cylingrical projection"},
	{10, "Rotated Latitude/Longitude grid"},
	{13, "Oblique Lambert conformal"},
	{14, "Rotated Gaussian latitude/longitude grid"},
	{20, "Stretched latitude/longitude grid"},
	{24, "Stretched Gaussian latitude/longitude"},
	{30, "Stretched and rotated latitude/longitude"},
	{34, "Stretched and rotated Gaussian latitude/longitude"},
	{50, "Spherical Harmonic Coefficients"},
	{60, "Rotated Spherical Harmonic coefficients"},
	{70, "Stretched Spherical Harmonic coefficients"},
	{80, "Stretched and rotated Spherical Harmonic"},
	{90, "Space view perspective or orthographic grid"},
	{193, "Quasi-regular latitude/longitude"},
}

// Table 7: resolution and component flags.
// Source: transcribed from ecCodes definitions/grib1/7.table.
var table7ResolutionFlags = []bitFlagEntry{
	{1, 0, "Direction increments not given"},
	{1, 1, "Direction increments given"},
	{2, 0, "Earth assumed spherical with radius = 6367.47 km"},
	{2, 1, "Earth assumed oblate spheroid with size as determined by IAU in 1965: 6378.160 km, 6356.775 km, f = 1/297.0"},
	{5, 0, "u and v components resolved relative to easterly and northerly directions"},
	{5, 1, "u and v components resolved relative to the defined grid"},
}

// Table 8: scan mode flags.
// Source: transcribed from ecCodes definitions/grib1/8.table.
var table8ScanModes = []bitFlagEntry{
	{1, 0, "Points scan in +i direction"},
	{1, 1, "Points scan in -i direction"},
	{2, 0, "Points scan in -j direction"},
	{2, 1, "Points scan in +j direction"},
	{3, 0, "Adjacent points in i direction are consecutive"},
	{3, 1, "Adjacent points in j direction are consecutive"},
}

// Table 11: data representation flags (dataFlags).
// Source: transcribed from ecCodes definitions/grib1/11.table.
var table11DataFlags = []bitFlagEntry{
	{1, 0, "Grid-point data"},
	{1, 1, "Spherical harmonic coefficients"},
	{2, 0, "Simple packing"},
	{2, 1, "Complex or second-order packing"},
	{3, 0, "Floating point values are represented"},
	{3, 1, "Integer values are represented"},
	{4, 0, "No additional flags at octet 14"},
	{4, 1, "Octet 14 contains additional flag bits"},
}

// Common table C-1: originating centre identifiers (shared by GRIB1 and BUFR3;
// 1 byte in GRIB1).
// Source: transcribed from ecCodes definitions/common/c-1.table; names keep the WMO
// English wording.
var tableC1Centres = []codeEntry{
	{0, "Absent"},
	{1, "Melbourne (WMC)"},
	{2, "Melbourne (WMC)"},
	{4, "Moscow (WMC)"},
	{5, "Moscow (WMC)"},
	{7, "US National Weather Service - NCEP (WMC)"},
	{8, "US National Weather Service - NWSTG (WMC)"},
	{9, "US National Weather Service - Other (WMC)"},
	{10, "Cairo (RSMC/RAFC)"},
	{12, "Dakar (RSMC/RAFC)"},
	{14, "Nairobi (RSMC/RAFC)"},
	{16, "Atananarivo (RSMC)"},
	{18, "Tunis-Casablanca (RSMC)"},
	{20, "Las Palmas (RAFC)"},
	{21, "Algiers (RSMC)"},
	{22, "Lagos (RSMC)"},
	{24, "Pretoria (RSMC)"},
	{26, "Khabarovsk (RSMC)"},
	{28, "New Delhi (IMD)"},
	{29, "New Delhi (NCMRWF)"},
	{30, "Novosibirsk (RSMC)"},
	{32, "Tashkent (RSMC)"},
	{33, "Jeddah (RSMC)"},
	{34, "Japanese Meteorological Agency - Tokyo (RSMC)"},
	{36, "Bankok"},
	{37, "Ulan Bator"},
	{38, "Beijing (RSMC)"},
	{40, "Seoul"},
	{41, "Buenos Aires (RSMC/RAFC)"},
	{43, "Brasilia (RSMC/RAFC)"},
	{45, "Santiago"},
	{46, "Brasilian Space Agency - INPE"},
	{51, "Miami (RSMC/RAFC)"},
	{52, "National Hurricane Center, Miami"},
	{53, "Canadian Meteorological Service - Montreal (RSMC)"},
	{54, "Canadian Meteorological Service - Montreal (RSMC)"},
	{55, "San Francisco"},
	{57, "U.S. Air Force - Global Weather Center"},
	{58, "US Navy - Fleet Numerical Oceanography Center"},
	{59, "NOAA Forecast Systems Lab, Boulder CO"},
	{60, "National Center for Atmospheric Research (NCAR), Boulder, CO"},
	{64, "Honolulu"},
	{65, "Darwin (RSMC)"},
	{67, "Melbourne (RSMC)"},
	{69, "Wellington (RSMC/RAFC)"},
	{74, "U.K. Met Office - Exeter"},
	{76, "Moscow (RSMC/RAFC)"},
	{78, "Offenbach (RSMC)"},
	{80, "Rome (RSMC)"},
	{82, "Norrkoping"},
	{84, "French Weather Service - Toulouse"},
	{85, "French Weather Service - Toulouse"},
	{86, "Helsinki"},
	{87, "Belgrade"},
	{88, "Oslo"},
	{89, "Prague"},
	{90, "Episkopi"},
	{91, "Ankara"},
	{92, "Frankfurt/Main (RAFC)"},
	{93, "London (WAFC)"},
	{94, "Copenhagen"},
	{95, "Rota"},
	{96, "Athens"},
	{97, "European Space Agency (ESA)"},
	{98, "European Centre for Medium-Range Weather Forecasts"},
	{99, "DeBilt, Netherlands"},
	{110, "Hong-Kong"},
	{160, "US NOAA/NESDIS"},
	{173, "US National Aeronautics and Space Administration (NASA)"},
	{195, "Indonesia (NMC)"},
	{204, "National Institute of Water and Atmospheric Research (NIWA - New Zealand)"},
	{210, "Frascati (ESA/ESRIN)"},
	{211, "Lannion"},
	{212, "Lisboa"},
	{213, "Reykjavik"},
	{214, "INM Madrid"},
	{215, "Zurich"},
	{216, "Service ARGOS Toulouse"},
	{218, "Budapest"},
	{224, "Austria"},
	{227, "Belgium (NMC)"},
	{233, "Dublin"},
	{235, "INGV"},
	{239, "CERFAX"},
	{244, "The former Yugoslav Republic of Macedonia (NMC)"},
	{245, "Ukraine (NMC)"},
	{246, "Republic of Moldova (NMC)"},
	{247, "Operational Programme for the Exchange of weather RAdar information (OPERA) - EUMETNET"},
	{250, "COnsortium for Small scale MOdelling (COSMO)"},
	{251, "Meteorological Cooperation on Operational NWP (MetCoOp)"},
	{252, "Max Planck Institute for Meteorology (MPI-M)"},
	{254, "EUMETSAT Operation Centre"},
	{255, "Consensus"},
}
