# decode benchmark drivers

Same-machine decode benchmark of this Go GRIB reader vs eccodes 2.41.0 on the
Linux bench host (go1.24.4 linux + system eccodes 2.41.0 + python3).
Protocol: multiple timed rounds, averages reported.

## Files

- `main.go` — Go driver. `go run ./bench/p7_decode_bench --rounds N file...`
 One round = `grib.Open` + full `Messages()` iteration + `Values()` on every
 message (metadata + all values). MEAN of the timed rounds is the primary
 metric, with median and min/max alongside.
- `eccodes_bench.py` — eccodes-side driver. `python3 -u eccodes_bench.py
 --rounds N file...` Pure python loop over the system binding (per-message
 handle creation + value extraction through the binding layer; no CLI engine
 in the timed path). Caliber note for this build: `GRIBMessage.values()`
 iterates key names, so values come from one low-level `codes_get_values`
 C call per message (numpy array) — the binding overhead is PER-MESSAGE, not
 per-value (less conservative than a per-value python loop). Multi-field
 envelopes (JMA GPV) are decoded through per-field slice files because this
 binding build has no next-message API (FileReader exposes only the first
 field of such an envelope); slices are prebuilt once per file and labeled
 `multifield-slice` in the output.

## Protocol (both engines, )

- 1 warmup round + N timed rounds (default 10), MEAN reported as the primary
 column plus median and min/max.
- Per file: wall seconds (mean/median/min/max), MB/s (file bytes / mean),
 msg/s (from mean), messages and values decoded, per-file sha256,
 GOMAXPROCS (Go side).
- Failed files are NOT silently excluded: they are reported with the failure
 reason plus the wall time of a representative failed attempt.

## Re-run on the Linux bench host

```sh
cd /root/simple-grib
CGO_ENABLED=0 go run ./bench/p7_decode_bench --rounds 10 /root/p7b/* > /root/p7b/go_results.jsonl
python3 -u bench/p7_decode_bench/eccodes_bench.py --rounds 10 /root/p7b/* > /root/p7b/py_results.jsonl
```

Corpus: `/root/p7b/` = the 11-file `.m-realworld` real-data corpus (sha256 in
the run output; the same 11 files are mirrored on the local workbench under a
git-ignored directory). The expanded corpus (47 files, 10 families) lives at
`/root/p8-corpus/` (sha256 in `SHA256SUMS`); the measured summary is in the
top-level [`README.md`](../../README.md#performance-comparison).
