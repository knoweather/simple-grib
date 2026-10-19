#!/usr/bin/env python3
# Fixture generator (2026-10-08): builds cross-validation fixtures with the
# SYSTEM eccodes C API (python-eccodes 2.41.0) and records per-file oracle
# data: sha256, n_points, latitudes[], longitudes[], values[] (as reported by
# eccodes), bitmap bits when present, plus the grid keys used.
# Independent of the simple-grib codebase (zero shared code paths).
# Output: /root/a-p5/p6a/<name>.grib2 + /root/a-p5/p6a/oracle.json
import hashlib
import json
import os
import struct

import eccodes

M = 10 ** 6
OUT = "/root/a-p5/p6a"
os.makedirs(OUT, exist_ok=True)


def write_msg(name, sample, grid, dr_tpl, bits, values, extra=None):
    g = eccodes.codes_grib_new_from_samples(sample)
    for k, v in grid.items():
        if v is None: # sentinel: set missing
            eccodes.codes_set_missing(g, k)
        else:
            eccodes.codes_set_long(g, k, v)
    for k, v in (extra or {}).items():
        if isinstance(v, float):
            eccodes.codes_set_double(g, k, v)
        else:
            eccodes.codes_set_long(g, k, v)
    eccodes.codes_set_long(g, "dataRepresentationTemplateNumber", dr_tpl)
    if bits is not None:
        eccodes.codes_set_long(g, "bitsPerValue", bits)
    eccodes.codes_set_values(g, values)
    path = os.path.join(OUT, name + ".grib2")
    with open(path, "wb") as f:
        eccodes.codes_write(g, f)
    eccodes.codes_release(g)
    return path


def make_oracle(path):
    g = eccodes.codes_grib_new_from_file(open(path, "rb"))
    rec = {
        "sha256": hashlib.sha256(open(path, "rb").read()).hexdigest(),
        "bytes": os.path.getsize(path),
        "dr_template": eccodes.codes_get_long(g, "dataRepresentationTemplateNumber"),
        "grid_template": eccodes.codes_get_long(g, "gridDefinitionTemplateNumber"),
    }
    for k in ("Ni", "Nj", "La1", "La2", "Lo1", "Lo2", "Di", "Dj",
              "iScansPositively", "jScansPositively"):
        try:
            rec["k_" + k] = eccodes.codes_get_long(g, k)
        except Exception:
            pass
    lats = list(eccodes.codes_get_double_array(g, "latitudes"))
    lons = list(eccodes.codes_get_double_array(g, "longitudes"))
    vals = list(eccodes.codes_get_values(g))
    rec["n"] = len(lats)
    rec["latitudes"] = lats
    rec["longitudes"] = lons
    rec["values"] = vals
    try:
        bp = eccodes.codes_get_long(g, "bitmapPresent")
        if bp == 1:
            d = open(path, "rb").read()
            off = 16
            guard = 0
            while off + 5 <= len(d) and guard < 20:
                ln = int.from_bytes(d[off:off + 4], "big")
                sec = d[off + 4]
                if ln < 5 or off + ln > len(d):
                    break
                if sec == 6:
                    # sec6: [len u32][num=6][indicator @+5][bitmap @+6:]
                    bits = []
                    for i in range(len(lats)):
                        byte_i = i // 8
                        bit_i = 7 - (i % 8)
                        bits.append(1 if (d[off + 6 + byte_i] >> bit_i) & 1 else 0)
                    rec["bitmapPresent"] = 1
                    rec["bitmap"] = bits
                    break
                off += ln
                guard += 1
    except Exception:
        pass
    eccodes.codes_release(g)
    return rec


def seq(n, fn):
    return [fn(i) for i in range(n)]


fixtures = []

# 1. regular ll baseline (j scans negatively: rows La1..La2)
p = write_msg("p6a_ll_basic", "regular_ll_sfc_grib2",
              {"Ni": 7, "Nj": 4, "La1": 30 * M, "La2": 0, "Lo1": 100 * M,
               "Lo2": 160 * M, "Di": 10 * M, "Dj": 10 * M},
              0, 12, seq(28, lambda i: float(i * 7 + 3)))
fixtures.append(("p6a_ll_basic", p))

# 2. j scans positively (La1=0 first row, rows ascend to La2=30)
p = write_msg("p6a_ll_jpos", "regular_ll_sfc_grib2",
              {"Ni": 7, "Nj": 4, "La1": 0, "La2": 30 * M, "Lo1": 100 * M,
               "Lo2": 160 * M, "Di": 10 * M, "Dj": 10 * M,
               "jScansPositively": 1},
              0, 12, seq(28, lambda i: float(i * 3 + 1)))
fixtures.append(("p6a_ll_jpos", p))

# 3. southern-hemisphere negative corner points
p = write_msg("p6a_ll_neglat", "regular_ll_sfc_grib2",
              {"Ni": 5, "Nj": 4, "La1": -10 * M, "La2": -40 * M,
               "Lo1": 120 * M, "Lo2": 160 * M, "Di": 10 * M, "Dj": 10 * M},
              0, 12, seq(20, lambda i: float(i * 5 - 40)))
fixtures.append(("p6a_ll_neglat", p))

# 4. longitude crossing 0 degrees (350 -> 50 via wrap; span check:
# |Ni-1|*Di = 60 == 360 - |50-350|)
p = write_msg("p6a_ll_lonwrap", "regular_ll_sfc_grib2",
              {"Ni": 7, "Nj": 3, "La1": 20 * M, "La2": 0, "Lo1": 350 * M,
               "Lo2": 50 * M, "Di": 10 * M, "Dj": 10 * M},
              0, 12, seq(21, lambda i: float(i * 11 + 2)))
fixtures.append(("p6a_ll_lonwrap", p))

# 5. Ni = 1 boundary (single column)
p = write_msg("p6a_ll_1xn", "regular_ll_sfc_grib2",
              {"Ni": 1, "Nj": 5, "La1": 40 * M, "La2": 0, "Lo1": 150 * M,
               "Lo2": 150 * M, "Di": 0, "Dj": 10 * M},
              0, 12, seq(5, lambda i: float(i * 9 + 4)))
fixtures.append(("p6a_ll_1xn", p))

# 6. Nj = 1 boundary (single row)
p = write_msg("p6a_ll_nx1", "regular_ll_sfc_grib2",
              {"Ni": 5, "Nj": 1, "La1": 20 * M, "La2": 20 * M,
               "Lo1": 0, "Lo2": 40 * M, "Di": 10 * M, "Dj": 0},
              0, 12, seq(5, lambda i: float(i * 13 + 7)))
fixtures.append(("p6a_ll_nx1", p))

# 7. quasi-regular ll (template 0, Ni missing): not generated. eccodes 2.41
# and 2.49 both cannot generate or read back per-point coordinates for this
# grid ("Key Ni cannot be 'missing' for a regular grid!"; PLPresent/pl keys
# not exposed for t0), so no external coordinate oracle exists for it.

# 8. full Gaussian window from the gg sample, unmodified (T32: N=32,
# Ni=128, Nj=64 -> 8192 points; overriding only some of N/Ni/Nj leaves
# inconsistent corner fields and segfaults eccodes' readback)
p = write_msg("p6a_gauss", "regular_gg_sfc_grib2", {},
              0, 14, seq(8192, lambda i: float((i % 97) - 48)))
fixtures.append(("p6a_gauss", p))

# 9. sub-area Gaussian window (template 41): first 32 circles of the T32
# grid, corner latitudes taken from eccodes' own distinctLatitudes of the
# full-grid file just written (keeps the window self-consistent)
def build_gauss_sub():
    g = eccodes.codes_grib_new_from_samples("regular_gg_sfc_grib2")
    ref = eccodes.codes_grib_new_from_file(
        open(os.path.join(OUT, "p6a_gauss.grib2"), "rb"))
    dlats = list(eccodes.codes_get_double_array(ref, "distinctLatitudes"))
    eccodes.codes_release(ref)
    if len(dlats) != 64:
        raise RuntimeError("expected 64 circles for T32, got %d" % len(dlats))
    eccodes.codes_set_long(g, "gridDefinitionTemplateNumber", 41)
    eccodes.codes_set_long(g, "Nj", 32)
    eccodes.codes_set_long(g, "La1", int(round(dlats[0] * M)))
    eccodes.codes_set_long(g, "La2", int(round(dlats[31] * M)))
    eccodes.codes_set_long(g, "dataRepresentationTemplateNumber", 0)
    eccodes.codes_set_long(g, "bitsPerValue", 14)
    eccodes.codes_set_values(
        g, [float(i * 3 + 1) for i in range(128 * 32)])
    path = os.path.join(OUT, "p6a_gauss_sub.grib2")
    with open(path, "wb") as f:
        eccodes.codes_write(g, f)
    eccodes.codes_release(g)
    return path


p = build_gauss_sub()
fixtures.append(("p6a_gauss_sub", p))

# 10. rotated lat/lon (template 1): pole 45N/30W, angle 30 deg
p = write_msg("p6a_rotll", "regular_ll_sfc_grib2",
              {"gridDefinitionTemplateNumber": 1, "Ni": 9, "Nj": 5,
               "La1": 60 * M, "La2": -60 * M, "Lo1": -120 * M, "Lo2": 120 * M,
               "Di": 30 * M, "Dj": 30 * M,
               "latitudeOfSouthernPole": 45 * M,
               "longitudeOfSouthernPole": 330 * M},
              0, 12, seq(45, lambda i: float(i * 6 + 5)),
              extra={"angleOfRotation": 30.0})
fixtures.append(("p6a_rotll", p))

# 11. stretched lat/lon (template 2): pole 0/0, factor 2.0
p = write_msg("p6a_stretch", "regular_ll_sfc_grib2",
              {"gridDefinitionTemplateNumber": 2, "Ni": 9, "Nj": 5,
               "La1": 60 * M, "La2": -60 * M, "Lo1": -120 * M, "Lo2": 120 * M,
               "Di": 30 * M, "Dj": 30 * M,
               "latitudeOfThePoleOfStretching": 0,
               "longitudeOfThePoleOfStretching": 0,
               "stretchingFactorScaled": 2000000},
              0, 12, seq(45, lambda i: float(i * 8 + 3)))
fixtures.append(("p6a_stretch", p))

# 12. stretched AND rotated lat/lon (template 3): angle 45 deg
p = write_msg("p6a_stretchrot", "regular_ll_sfc_grib2",
              {"gridDefinitionTemplateNumber": 3, "Ni": 9, "Nj": 5,
               "La1": 60 * M, "La2": -60 * M, "Lo1": -120 * M, "Lo2": 120 * M,
               "Di": 30 * M, "Dj": 30 * M,
               "latitudeOfSouthernPole": 0,
               "longitudeOfSouthernPole": 0,
               "stretchingFactorScaled": 2000000},
              0, 12, seq(45, lambda i: float(i * 5 + 9)),
              extra={"angleOfRotation": 45.0})
fixtures.append(("p6a_stretchrot", p))

# 13. polar stereographic (template 20), northern regional window
p = write_msg("p6a_ps", "polar_stereographic_sfc_grib2",
              {"Ni": 5, "Nj": 4, "La1": 70 * M, "Lo1": 260 * M,
               "LaD": 70 * M, "LoV": 260 * M, "Dx": 10000000, "Dy": 10000000},
              0, 12, seq(20, lambda i: float(i * 3 + 2)))
fixtures.append(("p6a_ps", p))

# 14. Lambert conformal (template 30), conus-like window
p = write_msg("p6a_lcc", "regular_ll_sfc_grib2",
              {"gridDefinitionTemplateNumber": 30, "Ni": 6, "Nj": 5, "La1": 45 * M, "Lo1": 255 * M,
               "LaD": 45 * M, "LoV": 255 * M, "Dx": 10000000, "Dy": 10000000, "Latin1": 25 * M, "Latin2": 49 * M},
              0, 12, seq(30, lambda i: float(i * 7 + 1)))
fixtures.append(("p6a_lcc", p))

# 15. bitmap / missing values: eccodes cannot mark missing points through
# this SWIG binding (NaN -> 2^63 garbage), so two stages: (a) eccodes writes
# a plain all-values file; (b) byte surgery repacks sec7 with the valid
# values only and inserts section 6 (indicator 0 + bitmap).
def build_bitmap_fixture():
    g = eccodes.codes_grib_new_from_samples("regular_ll_sfc_grib2")
    for k, v in {"Ni": 5, "Nj": 4, "La1": 30 * M, "La2": 0,
                 "Lo1": 100 * M, "Lo2": 140 * M, "Di": 10 * M,
                 "Dj": 10 * M}.items():
        eccodes.codes_set_long(g, k, v)
    eccodes.codes_set_long(g, "dataRepresentationTemplateNumber", 0)
    eccodes.codes_set_long(g, "bitsPerValue", 12)
    base_vals = [float(i * 7 + 3) for i in range(20)]
    eccodes.codes_set_values(g, base_vals)
    base_path = os.path.join(OUT, "_bitmap_base.grib2")
    with open(base_path, "wb") as f:
        eccodes.codes_write(g, f)
    eccodes.codes_release(g)

    d = bytearray(open(base_path, "rb").read())
    # GRIB2 layout (per eccodes section defs): sec0 = 16 bytes
    # ["GRIB"][reserved 2][discipline 1][edition 1][totalLength u64];
    # sections 1-7 follow contiguously, each [len u32 BE][sectionNumber u8][fields].
    secs = {}
    off = 16
    guard = 0
    while off + 5 <= len(d) and guard < 20:
        ln = int.from_bytes(d[off:off + 4], "big")
        sec = d[off + 4]
        if ln < 5 or off + ln > len(d):
            raise RuntimeError("bad section header at %d (len=%d)" % (off, ln))
        secs[sec] = (off, ln)
        if sec == 7:
            break
        off += ln
        guard += 1
    s5_off, s5_ln = secs[5]
    s7_off, s7_ln = secs[7]
    # sec5 template 1 fields (0-based from section start):
    # [0:4] len, [4] num=5, [5:9] numberOfValues u32, [9:11] tmpl u16,
    # [11:15] referenceValue b_0 float32 BE, [15:17] E int16 BE,
    # [17:19] D int16 BE, [19] bitsPerValue u8
    import struct as _st

    def smag16(raw):
        # eccodes signed[2] is sign-magnitude, not two's complement
        v = int.from_bytes(raw, "big")
        mag = v & 0x7FFF
        return -mag if v & 0x8000 else mag

    nv = int.from_bytes(d[s5_off + 5:s5_off + 9], "big")
    b0 = _st.unpack(">f", d[s5_off + 11:s5_off + 15])[0]
    E = smag16(d[s5_off + 15:s5_off + 17])
    D = smag16(d[s5_off + 17:s5_off + 19])
    bits = d[s5_off + 19]
    if nv != 20:
        raise RuntimeError("expected 20 values in base, got %d" % nv)

    missing_idx = {2, 7, 13}
    valid = [v for i, v in enumerate(base_vals) if i not in missing_idx]

    def pack(vals):
        out = []
        bitbuf = 0
        nbits = 0
        for v in vals:
            c = int(round((v - b0) * (2.0 ** (D - E))))
            c = max(0, min((1 << bits) - 1, c))
            for k in range(bits - 1, -1, -1):
                bitbuf = (bitbuf << 1) | ((c >> k) & 1)
                nbits += 1
                if nbits == 8:
                    out.append(bitbuf)
                    bitbuf = 0
                    nbits = 0
        if nbits:
            out.append(bitbuf << (8 - nbits))
        return bytes(out)

    payload = pack(valid)
    s5_new = bytearray(d[s5_off:s5_off + s5_ln])
    s5_new[5:9] = (17).to_bytes(4, "big") # numberOfValues

    bm = bytearray(3)
    for i in range(20):
        if i not in missing_idx:
            bm[i // 8] |= 0x80 >> (i % 8)
    # sec6: [len u32][num=6][indicator u8][bitmap bytes] -> 6 + 3 = 9
    s6 = bytearray()
    s6 += (9).to_bytes(4, "big")
    s6 += bytes([6, 0])
    s6 += bm

    # sec7: [len u32][num=7][packed data]
    s7_new = bytearray()
    s7_new += (5 + len(payload)).to_bytes(4, "big")
    s7_new += bytes([7])
    s7_new += payload

    tail = d[s7_off + s7_ln:] # end-of-message marker "7777" (required)
    body = d[:s5_off] + bytes(s5_new) + bytes(s6) + bytes(s7_new) + tail
    total = len(body)
    body[8:16] = total.to_bytes(8, "big") # sec0 octets 9-16: totalLength u64

    path = os.path.join(OUT, "p6a_bitmap.grib2")
    with open(path, "wb") as f:
        f.write(bytes(body))
    return path


p = build_bitmap_fixture()
fixtures.append(("p6a_bitmap", p))

# 16. IEEE binary32 (template 4): fractional + negative values
vals = [0.5, -1.25, 3.75, -0.125, 100.0,
         1e-4, -999.5, 12345.625, -0.0625, 7.0,
         0.25, -2.5, 4.125, -88.75, 555.0,
         0.03125, -1.875, 9.375, -0.5, 66.625]
p = write_msg("p6a_ieee", "regular_ll_sfc_grib2",
              {"Ni": 5, "Nj": 4, "La1": 20 * M, "La2": -10 * M,
               "Lo1": 30 * M, "Lo2": 70 * M, "Di": 10 * M, "Dj": 10 * M},
              4, None, vals)
fixtures.append(("p6a_ieee", p))

# 17. unimplemented template (template 10 = Mercator) for fail-loud check:
# our reader must keep it metadata-only and return ErrNotImplemented{10}
p = write_msg("p6a_unimpl_t10", "regular_ll_sfc_grib2",
              {"gridDefinitionTemplateNumber": 10, "Ni": 9, "Nj": 5,
               "La1": 60 * M, "La2": -60 * M, "Lo1": 240 * M, "Lo2": 120 * M,
               "Di": 30 * M, "Dj": 30 * M},
              0, 12, seq(45, lambda i: float(i)))
fixtures.append(("p6a_unimpl_t10", p))

oracle = {}
for name, path in fixtures:
    try:
        oracle[name] = make_oracle(path)
        print("OK ", name, "n=", oracle[name]["n"])
    except Exception as e:
        print("FAIL", name, str(e)[:120])

with open(os.path.join(OUT, "oracle.json"), "w") as f:
    json.dump(oracle, f)
print("oracle.json written:", len(oracle), "fixtures")
