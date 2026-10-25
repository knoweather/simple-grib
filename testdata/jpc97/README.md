# JPEG 2000 5.40 irreversible (9/7) fixtures

Generated with Pillow 12.2.0 (OpenJPEG 2.x), saving a single-component grayscale
image as a raw codestream:

```python
Image.save(path, no_jp2=True, irreversible=True, num_resolutions=N)
```

The encoder emits one layer, LRCP progression, no precincts, `cblksty=0`, one
tile and QCD style 2, which is the subset `pack/jpc97_external_test.go`
exercises. `<name>.txt` is the pixel sequence returned by Pillow's own decoder
(OpenJPEG), one sample per line; the test requires an exact match.

| fixture | bits | levels | note |
|---|---|---|---|
| `grad_16x12_l1` | 8 | 0 | no DWT: isolates dequantization |
| `grad_16x12_l3` | 8 | 3 | even dimensions |
| `rand_17x13_l3` | 8 | 3 | odd dimensions, boundary lifting |
| `gray16_16x12_l3` | 16 | 3 | 16-bit precision |
