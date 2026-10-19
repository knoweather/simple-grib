# specdata — vendored WMO GRIB2 specification data

Source of truth for `cmd/specgen`, which regenerates `spec/tables_gen.go` and
`spec/doc.go`. Do not hand-edit the CSVs: they are upstream snapshots.

## Contents

- `GRIB2_Template_*.csv` / `GRIB2_CodeFlag_*.csv` — from `wmo-im/GRIB2`
 (WMO official structure spec, amendments through FT2026-1; see PROVENANCE.txt)
- `CCT/C11.csv`, `CCT/C14.csv` — from `wmo-im/CCT` (Common Code Tables;
 C-11 = originating centres used by section 1 octets 6-7). Other CCT tables
 can be added the same way; specgen picks up every `CCT/C*.csv`.
- `PROVENANCE.txt` — exact source commits; its summary is stamped into the
 generated files (spec.Version).

## Refresh procedure (after a WMO amendment, e.g. FT2027-x)

1. Check amendment status (APPROVED + implementation date):
 `https://community.wmo.int/site/knowledge-hub/programmes-and-initiatives/wmo-information-system-wis/about-manual-codes-volume-i2`
2. Download fresh tarballs (on the Linux bench host or any machine with GitHub access):
 ```bash
 curl -sfL https://api.github.com/repos/wmo-im/GRIB2/tarball/master -o /tmp/grib2.tgz
 curl -sfL https://api.github.com/repos/wmo-im/CCT/tarball/master -o /tmp/cct.tgz
 ```
3. Replace the CSVs here, update PROVENANCE.txt (new commit hashes + amendment
 level), then regenerate and verify:
 ```bash
 go run ./cmd/specgen
 go run ./cmd/specgen -check
 CGO_ENABLED=0 go build ./spec && go vet ./spec
 ```
4. If the section/template structure changed, re-run the checks described in
 `GRIB2-spec-核验报告.md` and update COORDINATION.md §5 accordingly.
