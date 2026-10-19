// Package grib1 is a pure-Go reader for WMO GRIB edition 1 (FM 92-IX) messages.
//
// Positioning: the GRIB2 side (root package plus spec/grid/pack) is the heavyweight
// implementation with data-driven templates and code tables. GRIB1's spec is a set
// of document tables with no machine-readable CSV counterpart, so this package
// takes the other road: the fixed byte layouts live directly in the code, every
// offset is annotated with its WMO octet number, and every read is validated.
//
// Layout (section numbering matches the WMO documents):
//
//	Section 0 IS indicator section, 8 bytes: 'GRIB' + 3-byte total length + edition (=1)
//	Section 1 PDS product definition section (mandatory)
//	Section 2 GDS grid description section (present per the PDS flags)
//	Section 3 BMS bitmap section (present per the PDS flags)
//	Section 4 BDS binary data section (mandatory)
//	Section 5 ES end marker '7777' (4 bytes)
//
// Three hard rules shared with the rest of the library:
//
//  1. Fail loudly: contradictory lengths, out-of-bounds reads and inconsistent
//     counts return contextual errors — never panic, never silently truncate or
//     zero-fill.
//  2. Name what is unsupported: second-order packing, spherical harmonics, IEEE
//     packing, projection grids over a non-spherical earth — all return explicit
//     errors instead of a guessed result.
//  3. Do not guess: unverified encodings (e.g. the rotation angles of rotated
//     grids) are rejected rather than answered with possibly-wrong coordinates.
//
// This package parses one message at a time and does no file I/O — file indexing
// and edition dispatch are handled centrally in the root grib package (see grib.Open).
package grib1

// Edition is the GRIB edition this package implements.
const Edition = 1
