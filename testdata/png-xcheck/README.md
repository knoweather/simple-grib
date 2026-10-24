# PNG 5.41 cross-check fixtures

Generated 2026-10-25 for `pack/png_external_test.go`. Each `<depth>.png` is a
7x5 grayscale PNG and `<depth>.txt` lists its raw samples in scan order (one
per line).

| depth | encoder |
|---|---|
| 1 / 2 / 4 | pypng 0.20220715.0 (pure-Python writer, `bitdepth=1/2/4`) |
| 8 / 16 | Pillow 12.2.0 (`L` / `I;16`) |
| `interlace4` / `interlace8` | pypng, Adam7 interlaced (`interlace=True`) |

The point is encoder independence: `gribw.SetPNG` is not involved, so these
vectors catch row padding, bit order and 16-bit endianness mistakes that a
self-round-trip would hide. Samples are deterministic patterns, not
compressed noise, so a failure points at a specific bit position.
