package grib1

import "fmt"

// GRIB1 section numbers (matching the WMO documents; note this numbering differs
// entirely from GRIB2's 0-7).
const (
	SectionIndicator = 0 // IS, indicator section (8 bytes)
	SectionPDS       = 1 // product definition section (mandatory)
	SectionGDS       = 2 // grid description section (optional)
	SectionBMS       = 3 // bitmap section (optional)
	SectionBDS       = 4 // binary data section (mandatory)
	SectionEnd       = 5 // end marker '7777' (4 bytes)
)

// Minimum section lengths per the WMO spec; used to reject clearly damaged
// sections before any parsing happens.
const (
	indicatorSectionLen = 8
	endSectionLen       = 4
	minPDSLen           = 28 // section 1: octets 1-28 are the fixed part
	minGDSLen           = 6  // section 2: octets 1-6 are the common header (length + NV + PV + type)
	minBMSLen           = 6  // section 3: octets 1-6 (length + unused bits + table reference)
	minBDSLen           = 11 // section 4: octets 1-11 (length + flags + E + R + bits per value)
	minMessageLen       = indicatorSectionLen + minPDSLen + minBDSLen + endSectionLen
)

// PDS octet 8 is the flag byte (table 1): bit 1 = section 2 (GDS) present,
// bit 2 = section 3 (BMS) present.
const (
	pdsFlagGDS = 0x80
	pdsFlagBMS = 0x40
)

// Section is one section of a message.
type Section struct {
	Number int    // section number (SectionIndicator … SectionEnd)
	Length int    // section length in bytes; IS is always 8, the end marker always 4
	Data   []byte // the full section bytes starting at octet 1 (length prefix included)
}

// splitSections slices exactly one complete message into sections, validating the
// structural invariants (fail loudly on any violation):
//   - 'GRIB' magic and edition number = 1;
//   - the total length declared by section 0 equals len(msg) (the caller must
//     pass exactly one message);
//   - sections 1/2/3/4 carry self-consistent length prefixes and stay inside the message;
//   - the GDS/BMS presence declared by the section 1 flags matches the actual bytes;
//   - the message ends with '7777' and has no trailing bytes.
func splitSections(msg []byte) ([]Section, error) {
	if len(msg) < minMessageLen {
		return nil, fmt.Errorf("grib1: message too short: %d bytes, want >= %d", len(msg), minMessageLen)
	}
	if string(msg[0:4]) != "GRIB" {
		return nil, fmt.Errorf("grib1: bad magic % X, want 'GRIB'", msg[0:4])
	}
	if msg[7] != Edition {
		return nil, fmt.Errorf("grib1: edition octet = %d, want %d", msg[7], Edition)
	}
	total := int(msg[4])<<16 | int(msg[5])<<8 | int(msg[6])
	if total != len(msg) {
		return nil, fmt.Errorf("grib1: indicator section declares %d bytes, got %d", total, len(msg))
	}

	secs := []Section{{Number: SectionIndicator, Length: indicatorSectionLen, Data: msg[0:indicatorSectionLen]}}

	off := indicatorSectionLen
	pds, err := takeSection(msg, off, SectionPDS, minPDSLen)
	if err != nil {
		return nil, err
	}
	secs = append(secs, pds)
	flags := pds.Data[7] // PDS octet 8
	off += pds.Length

	if flags&pdsFlagGDS != 0 {
		gds, err := takeSection(msg, off, SectionGDS, minGDSLen)
		if err != nil {
			return nil, err
		}
		secs = append(secs, gds)
		off += gds.Length
	}
	if flags&pdsFlagBMS != 0 {
		bms, err := takeSection(msg, off, SectionBMS, minBMSLen)
		if err != nil {
			return nil, err
		}
		secs = append(secs, bms)
		off += bms.Length
	}
	bds, err := takeSection(msg, off, SectionBDS, minBDSLen)
	if err != nil {
		return nil, err
	}
	secs = append(secs, bds)
	off += bds.Length

	if len(msg)-off != endSectionLen {
		return nil, fmt.Errorf("grib1: %d bytes between the data section and the end of the message, want %d ('7777')",
			len(msg)-off, endSectionLen)
	}
	end := Section{Number: SectionEnd, Length: endSectionLen, Data: msg[off:]}
	if string(end.Data) != "7777" {
		return nil, fmt.Errorf("grib1: end section = % X, want '7777'", end.Data)
	}
	return append(secs, end), nil
}

// takeSection reads the section's 3-octet length prefix at off and validates the
// lower and upper bounds before returning the section.
func takeSection(msg []byte, off, number, minLen int) (Section, error) {
	if off+3 > len(msg) {
		return Section{}, fmt.Errorf("grib1: section %d: no room for the 3-octet length at offset %d", number, off)
	}
	n := int(msg[off])<<16 | int(msg[off+1])<<8 | int(msg[off+2])
	if n < minLen {
		return Section{}, fmt.Errorf("grib1: section %d length %d, want >= %d", number, n, minLen)
	}
	if off+n > len(msg) {
		return Section{}, fmt.Errorf("grib1: section %d needs %d bytes at offset %d, message has %d", number, n, off, len(msg))
	}
	return Section{Number: number, Length: n, Data: msg[off : off+n]}, nil
}
