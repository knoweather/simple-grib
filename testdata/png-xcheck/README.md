# PNG 5.41 cross-check fixtures

Generated 2026-10-25 for `pack/png_external_test.go`. Each `<depth>.png` is a
7x5 grayscale PNG and `<depth>.txt` lists its raw samples in scan order (one
per line).

| depth | encoder |
|---|---|
| 1 / 2 / 4 | pypng 0.20220715.0 (pure-Python writer, `bitdepth=1/2/4`) |
| 8 / 16 | Pillow 12.2.0 (`L` / `I;16`) |
| `interlace4` / `interlace8` | pypng, Adam7 interlaced (`interlace=True`) |
| `simplepng8` / `simplepng8i` / `simplepng16` | simple-png v1.4.0 encoder (8-bit, Adam7 8-bit, 16-bit) |

`simplepng*` also records a cross-check against the sister library: the same
files were decoded by simple-png and matched these samples during fixture
generation, so simple-grib and simple-png agree on all three layouts.

The point is encoder independence: `gribw.SetPNG` is not involved, so these
vectors catch row padding, bit order and 16-bit endianness mistakes that a
self-round-trip would hide. Samples are deterministic patterns, not
compressed noise, so a failure points at a specific bit position.
