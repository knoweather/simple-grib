# usability corpus & results

Real-world GRIB2 usability cross-check of this library vs eccodes 2.41.0 over
47 files from an operational-data archive; this directory holds the
machine-readable evidence (per-file metadata matrix and decode benchmarks).

## Corpus source (NOT committed — 405MB)

- 47 files / 10 categories / 405,102,570 bytes total (cap: 500MB), taken from
  an operational-data archive during development.
- The corpus itself is not shipped (405 MB of real weather data); this
  directory only carries the machine-readable evidence plus per-file sha256 in
  `manifest.json`, so any subset can be re-verified against its own copy.
- Per-file `sha256` + `size_bytes` + `category`: `manifest.json`.

## Files here

| file | content |
|---|---|
| `manifest.json` | 47 entries: name / category / size / sha256 |
| `matrix_go.jsonl` | Go-side usability matrix (one JSON line per file): open result, per-message value counts or error reasons |
| `matrix_eccodes.jsonl` | eccodes-side matrix (python-eccodes 2.41, metadata level: numberOfValues + grid/datarep template per message) |
| `bench_go_p8.jsonl` | Go decode benchmark, protocol (warmup 1 + timed 10; mean primary + median/min/max), full corpus |
| `bench_py_p8.jsonl` | eccodes decode benchmark, same protocol, same machine (go1.24.4 / eccodes 2.41.0) |

## Regenerate

```sh
# Go matrix and benchmark (any host with the repo + a local corpus copy):
CGO_ENABLED=0 go run ./bench/p7_decode_bench <corpus-dir>/* > bench_go_p8.jsonl
python3 -u bench/p7_decode_bench/eccodes_bench.py <corpus-dir>/* > bench_py_p8.jsonl
```

## Caliber notes

- eccodes python binding per-message overhead is one C call per message
  (`codes_get_values`); no CLI engine in the timed path.
- Multi-field envelopes (JMA GPV): the binding's FileReader exposes only the
  first field; slices per oracle method, labeled `multifield-slice`.
  The metadata matrix shows the same first-field limitation — JMA message
  counts there are first-field-only (Go side sees all fields).
- **Bitmap semantics difference**: for files with section 4 present, this
  library's `Values()` returns only the present values (NV), while the
  binding's `codes_get_values` returns ALL grid points with missing markers.
  UKMO synoptic files: 311,022 present vs 467,775 grid points — both engines
  decode correctly; the bench `values_decoded` column differs by that
  semantic for those files (footnoted in the result tables).
- eccodes decodes JPEG 2000 (template 40) and GRIB (edition 1); this
  library decodes both as well (5.40 pure-Go JPC decoder, `grib1/`). Where a
  corpus file hits an unsupported template, both engines report it — the
  matrix column records which engine failed and why.
