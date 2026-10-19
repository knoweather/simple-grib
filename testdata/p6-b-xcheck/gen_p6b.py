#!/usr/bin/env python3
# Ground-truth generator (2026-10-08): writes encode-side cross-validation
# fixtures with the SYSTEM eccodes C API (python-eccodes 2.41.0) and records
# per-file oracle data: input vector, encode parameters, sha256, n_points,
# values[] as reported by eccodes readback (codes_get_values), plus sec5
# parameter keys.
# Independent of the simple-grib codebase (zero shared code paths).
# Output: /root/b-p6/p6b/<name>.grib2 + oracle.json + SHA256SUMS
import hashlib
import json
import os

import eccodes

OUT = "/root/b-p6/p6b"
os.makedirs(OUT, exist_ok=True)


def gen(name, ni, nj, dr_tpl, values, extra=None):
    # Consistent explicit grid (WMO: (Ni-1)*Di must span Lo1..Lo2 etc.).
    # Di=5 deg, Dj=10 deg; La1=-15 (negative corner), Lo1=10.
    di, dj = 5.0, 10.0
    la1, lo1 = -15.0, 10.0
    g = eccodes.codes_grib_new_from_samples("regular_ll_sfc_grib2")
    try:
        eccodes.codes_set_long(g, "Ni", ni)
        eccodes.codes_set_long(g, "Nj", nj)
        eccodes.codes_set_double(g, "La1", la1)
        eccodes.codes_set_double(g, "Lo1", lo1)
        eccodes.codes_set_double(g, "Di", di)
        eccodes.codes_set_double(g, "Dj", dj)
        eccodes.codes_set_double(g, "La2", la1 + (nj - 1) * dj)
        eccodes.codes_set_double(g, "Lo2", lo1 + (ni - 1) * di)
        eccodes.codes_set_long(g, "dataRepresentationTemplateNumber", dr_tpl)
        for k, v in sorted((extra or {}).items()):
            eccodes.codes_set_long(g, k, v)
        eccodes.codes_set_values(g, values)
        path = os.path.join(OUT, name + ".grib2")
        with open(path, "wb") as f:
            eccodes.codes_write(g, f)
        # oracle readback
        g2 = eccodes.codes_grib_new_from_file(open(path, "rb"))
        try:
            got = [float(x) for x in eccodes_get_values_list(g2)]
            keys = {}
            for k in ("dataRepresentationTemplateNumber", "bitsPerValue",
                      "decimalScaleFactor", "binaryScaleFactor", "referenceValue",
                      "orderOfSpatialDifferencing", "numberOfOctetsExtraDescriptors",
                      "numberOfGroupsOfDataValues", "bitmapPresent", "numberOfValues"):
                try:
                    keys[k] = int(eccodes.codes_get_long(g2, k))
                except Exception:
                    pass
        finally:
            eccodes.codes_release(g2)
    finally:
        eccodes.codes_release(g)
    raw = open(path, "rb").read()
    return {
        "file": name + ".grib2",
        "sha256": hashlib.sha256(raw).hexdigest(),
        "grid": {"Ni": ni, "Nj": nj},
        "input_values": values,
        "encode_params": dict(extra or {}),
        "oracle_values_eccodes_readback": got,
        "n_points": len(got),
        "sec5_keys": keys,
    }


def eccodes_get_values_list(g):
    return list(eccodes.codes_get_values(g))


# value patterns (24 pts unless noted)
MIXED = [float(v) for v in (0, -7, 3, 128, -129, 4096, -4097, 1, -1,
                            55, -55, 100, -100, 255, -255, 77, -77,
                            314, -314, 16, -16, 99, -99, 42)]
MIXED_SMALL = [float(v) for v in (0, -3, 2, 5, -6, 7, -8, 9, -10,
                                  11, -12, 13, -14, 15, -16, 17,
                                  -18, 19, -20, 21, -22, 23, -24, 25)]
CONSTANT = [float(5.5)] * 24
ZEROS = [0.0] * 24
EXTREME = [float(v) for v in (1000000, -1000000, 999999, -999999, 500000,
                              -500000, 250000, -250000, 125000, -125000,
                              62500, -62500, 31250, -31250, 15625, -15625,
                              7812, -7812, 3906, -3906, 1953, -1953, 976, -976)]

cases = []

# --- A: complex 5.3 (spatial differencing) ----------------------------------
cases.append(gen("a1_cplx53_order2", 6, 4, 3, MIXED,
                 {"orderOfSpatialDifferencing": 2, "numberOfOctetsExtraDescriptors": 2}))
cases.append(gen("a2_cplx53_order1", 6, 4, 3, MIXED,
                 {"orderOfSpatialDifferencing": 1, "numberOfOctetsExtraDescriptors": 1}))
cases.append(gen("a3_cplx53_order2_d2", 6, 4, 3, MIXED_SMALL,
                 {"orderOfSpatialDifferencing": 2, "numberOfOctetsExtraDescriptors": 2,
                  "decimalScaleFactor": 2}))
cases.append(gen("a4_cplx53_constant", 6, 4, 3, CONSTANT,
                 {"orderOfSpatialDifferencing": 2, "numberOfOctetsExtraDescriptors": 2}))
cases.append(gen("a5_cplx53_extreme", 6, 4, 3, EXTREME,
                 {"orderOfSpatialDifferencing": 2, "numberOfOctetsExtraDescriptors": 2}))

# --- B: complex 5.2 (plain) --------------------------------------------------
cases.append(gen("b1_cplx52_mixed", 6, 4, 2, MIXED))
cases.append(gen("b2_cplx52_d2", 6, 4, 2, MIXED_SMALL, {"decimalScaleFactor": 2}))
cases.append(gen("b3_cplx52_constant", 5, 3, 2, [float(5.5)] * 15))

# --- C: CCSDS 5.42 AEC --------------------------------------------------------
cases.append(gen("c1_ccsds_bits8", 7, 3, 42, MIXED_SMALL[:21], {"bitsPerValue": 8}))
cases.append(gen("c2_ccsds_bits16", 7, 3, 42, MIXED[:21], {"bitsPerValue": 16}))
cases.append(gen("c4_ccsds_zeros", 7, 3, 42, ZEROS[:21], {"bitsPerValue": 8}))
cases.append(gen("c5_ccsds_extreme", 7, 3, 42, EXTREME[:21], {"bitsPerValue": 16}))

with open(os.path.join(OUT, "oracle.json"), "w") as f:
    json.dump({"generator": "gen_p6b.py (, )", "eccodes": eccodes.codes_get_version_info(),
               "cases": cases}, f, indent=1)

with open(os.path.join(OUT, "SHA256SUMS"), "w") as f:
    for c in cases:
        f.write("%s %s\n" % (c["sha256"], c["file"]))

for c in cases:
    print("%s: n=%d input_n=%d match_input=%s keys=%s" % (
        c["file"], c["n_points"], len(c["input_values"]),
        all(abs(a - b) < 1e-9 for a, b in zip(c["oracle_values_eccodes_readback"], c["input_values"])),
        {k: v for k, v in c["sec5_keys"].items() if v not in (0,)}))
