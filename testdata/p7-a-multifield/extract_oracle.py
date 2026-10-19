#!/usr/bin/env python3
# oracle extraction (the Linux bench host, system eccodes 2.41.0).
#
# The JMA GPV envelope is single-envelope multi-field: s0 | s1 s3 | [s4 s5 s6
# s7] x N | 4-byte "7777" trailer, totalLength covering the whole envelope.
# eccodes 2.41's file scanner reports ONE message offset for the envelope but
# its grib_ls iterator sees all N fields (verified: "6 of 6 messages"). This
# binding exposes no codes_grib_next, so per-field values are extracted from
# slice files [s0'][s1][s3][group_k] + b"7777" where s0' carries totalLength =
# slice length (the only byte differences; section contents are verbatim
# envelope bytes). The "7777" trailer is REQUIRED by eccodes' file reader
# ("No final 7777 in message!" without it) — independent confirmation of the
# end-marker convention, and exactly the shape JMA files already have.
#
# Emits /root/p7a/oracle.json. Run: python3 -u extract_oracle.py
import base64
import hashlib
import json
import subprocess

import numpy as np
import eccodes as ec

PATH = "/root/p7a/jma_gpv_0p5deg_p1000.grib2"
OUT = "/root/p7a/oracle.json"

META_KEYS = [
    "discipline", "edition", "centre", "subCentre",
    "parameterCategory", "parameterNumber", "levelist",
    "Ni", "Nj", "numberOfValues", "dataRepresentationTemplateNumber",
    "gridType", "La1", "Lo1", "La2", "Lo2", "Di", "Dj",
    "iScansPositively", "jScansPositively", "iScansConsecutively", "jScansConsecutively",
    "firstLatitude", "firstLongitude", "lastLatitude", "lastLongitude",
]


def walk(raw):
    """Section header walk (same rules as the Go reader's index walker)."""
    pos, end = 16, len(raw)
    spans = []
    while pos < end:
        if end - pos < 5:
            assert end - pos <= 4, f"trailer {end - pos} > 4"
            break
        slen = int.from_bytes(raw[pos:pos + 4], "big")
        num = raw[pos + 4]
        spans.append((num, pos, slen))
        pos += slen
    h = next(i for i, (n, _, _) in enumerate(spans) if n == 4)
    shared = spans[:h]
    groups, cur = [], []
    for s in spans[h:]:
        cur.append(s)
        if s[0] == 7:
            groups.append(cur)
            cur = []
    assert not cur and len(groups) >= 2, "unexpected group structure"
    return shared, groups


def main():
    raw = open(PATH, "rb").read()
    sha = hashlib.sha256(raw).hexdigest()
    shared, groups = walk(raw)
    print(f"walk: {len(groups)} field groups, shared={[s[0] for s in shared]}")

    # grib_ls cross-check on the ORIGINAL file (M's caliber).
    gl = subprocess.run(
        ["grib_ls", "-p", "discipline,centre,date,parameterCategory,"
         "parameterNumber,typeOfLevel,level,Ni,Nj,numberOfValues,"
         "dataRepresentationTemplateNumber,gridType,La1,Lo1,Di,Dj,"
         "iScansPositively,jScansPositively", PATH],
        capture_output=True, text=True)
    print("grib_ls rc:", gl.returncode)
    grib_ls_rows = [ln.split() for ln in gl.stdout.splitlines()
                    if ln.strip().startswith("2 ") or ln.strip().startswith("2\t")]

    out = {
        "file": "jma_gpv_0p5deg_p1000.grib2",
        "sha256": sha,
        "size_bytes": len(raw),
        "n_fields": len(groups),
        "grib_ls_rows": grib_ls_rows,
        "fields": [],
    }

    for k, g in enumerate(groups):
        s0 = bytearray(raw[0:16])
        buf = bytearray(s0)
        for _, p, l in shared:
            buf += raw[p:p + l]
        for _, p, l in g:
            buf += raw[p:p + l]
        buf += b"7777" # eccodes' file reader requires the end marker (JMA shape)
        buf[8:16] = len(buf).to_bytes(8, "big") # totalLength = slice length
        tmp = f"/root/p7a/field_{k}.grib2"
        open(tmp, "wb").write(bytes(buf))

        fh = open(tmp, "rb")
        hdl = ec.codes_grib_new_from_file(fh)
        meta = {}
        for key in META_KEYS:
            try:
                meta[key] = ec.codes_get(hdl, key)
            except Exception as e: # noqa: BLE001 - record the miss
                meta[key] = f"<{type(e).__name__}>"
        vals = np.asarray(ec.codes_get_values(hdl), dtype=np.float32)
        entry = {
            "index": k,
            "slice_bytes": len(buf),
            "meta": meta,
            "n_values": int(vals.size),
            "values_b64": base64.b64encode(np.ascontiguousarray(vals).tobytes()).decode("ascii"),
        }
        if k == 0:
            # "latitude"/"longitude" array keys do not exist in this build;
            # codes_grib_get_data returns a tuple of Bunch(lat, lon, value).
            data = ec.codes_grib_get_data(hdl)
            lats = np.array([p.lat for p in data], dtype=np.float64)
            lons = np.array([p.lon for p in data], dtype=np.float64)
            dv = np.array([p.value for p in data], dtype=np.float32)
            if not np.array_equal(dv, vals):
                print(f"WARNING field 0: get_data values differ from codes_get_values "
                      f"(max diff {np.abs(dv - vals).max()})")
            entry["lats_b64"] = base64.b64encode(np.ascontiguousarray(lats).tobytes()).decode("ascii")
            entry["lons_b64"] = base64.b64encode(np.ascontiguousarray(lons).tobytes()).decode("ascii")
        ec.codes_release(hdl)
        fh.close()
        out["fields"].append(entry)
        print(f"field {k}: param={meta.get('parameterCategory')}.{meta.get('parameterNumber')} "
              f"level={meta.get('levelist')} n={vals.size} ok")

    with open(OUT, "w") as f:
        json.dump(out, f)
    print(f"oracle written: {OUT} fields={len(out['fields'])} sha256={sha}")


if __name__ == "__main__":
    main()
