#!/bin/bash
# Owner: — oracle harness 
#
# Cross-validates simple-grib WRITER output against independent GRIB2
# implementations on the Linux bench host:
# ① eccodes 2.41 (Debian libeccodes-tools): grib_ls / grib_get -p / grib_get_data
# ② wgrib2 3.8.0 (NCEP, C-API build at /root/wgrib2-src)
# ③ xcheck_bin (custom C harness over the wgrib2 C API; msg0 only, 9 points)
# plus the in-repo bit-level + determinism tests (go test ./... — run separately).
#
# Boundary notes (ruling ): cfgrib is NOT installed on the Linux bench host — it shares
# the eccodes base library, so its independence would be ≈0; grib_api (ECMWF)
# is unavailable here. The three oracles above are the independent set.
#
# Value tiers:
# - eccodes grib_get_data prints 10 significant digits — enough to recover
# every float32 bit pattern uniquely (the bit-exact oracle);
# - xcheck_bin prints %.6g — a ~1e-6-level cross-check, order-normalized
# (wgrib2 traverses the grid lat-ascending regardless of file order).
#
# Usage: bash scripts/xcheck/p35-oracle.sh (on the Linux bench host; paths overridable via env)

set -u
REPO=$(cd "$(dirname "$0")/../.." && pwd)
FX=$REPO/testdata/gribw-xcheck
WG=${WG:-/root/wgrib2-src/build/src/wgrib2}
CAPI=${CAPI:-/root/wgrib2-src/build/src/c_api}
XCHECK=${XCHECK:-/root/xcheck_bin}

FAIL=0
ok() { echo "PASS: $1"; }
bad() { echo "FAIL: $1"; FAIL=1; }

# --- fixture integrity (md5 pre-check, merge-protocol habit) ---------------
[ "$(md5sum "$FX/p31_frame_simple.grib2" | cut -d' ' -f1)" = "74004ed6cd25346d1327250a1e14d736" ] \
  && ok "fixture p31_frame_simple.grib2 md5" || bad "fixture p31_frame_simple.grib2 md5 drift"
[ "$(md5sum "$FX/p35_dual.grib2" | cut -d' ' -f1)" = "489b05345acccd5138ebcf2c2529e506" ] \
  && ok "fixture p35_dual.grib2 md5" || bad "fixture p35_dual.grib2 md5 drift"

# --- ① eccodes metadata -----------------------------------------------------
P31=$(grib_get -p edition,centre,discipline,numberOfValues,typeOfLevel,level $FX/p31_frame_simple.grib2 2>&1)
[ "$(echo "$P31" | tr '\n' ' ')" = "2 ecmf 10 9 atmosphere 850 " ] \
  && ok "eccodes metadata p31 (edition/centre/discipline/NV/level)" \
  || bad "eccodes metadata p31: got [$P31]"

DUAL_LS=$(grib_ls "$FX/p35_dual.grib2" 2>&1)
echo "$DUAL_LS" | grep -q "grid_simple" && echo "$DUAL_LS" | grep -q "grid_ieee" \
  && ok "eccodes grib_ls dual: both packing types recognized" \
  || bad "eccodes grib_ls dual packing types: [$DUAL_LS]"
echo "$DUAL_LS" | grep -q "2 of 2 messages" \
  && ok "eccodes grib_ls dual: message count = 2" \
  || bad "eccodes grib_ls dual message count: [$DUAL_LS]"

# --- ② eccodes values (bit-exact via 10-sig-digit decimal) ------------------
python3 - "$FX/p31_frame_simple.grib2" "$FX/p35_dual.grib2" <<'PYEOF'
import struct, subprocess, sys

def f32bits(x): return struct.unpack(">I", struct.pack(">f", x))[0]

def parse_blocks(out):
    """grib_get_data prints a 'Latitude Longitude Value' header per message."""
    blocks, cur = [], None
    for line in out.splitlines():
        if line.startswith("Latitude"):
            cur = []
            blocks.append(cur)
        elif cur is not None and line.strip():
            parts = line.split()
            assert len(parts) == 3, f"bad value line: {line!r}"
            cur.append((float(parts[0]), float(parts[1]), parts[2]))
    return blocks

def check(name, file, want_msgs):
    out = subprocess.run(["grib_get_data", file], capture_output=True, text=True).stdout
    blocks = parse_blocks(out)
    if len(blocks) != len(want_msgs):
        print(f"FAIL: {name}: {len(blocks)} value blocks, want {len(want_msgs)}")
        return 1
    fail = 0
    for mi, (block, want) in enumerate(zip(blocks, want_msgs)):
        if len(block) != len(want):
            print(f"FAIL: {name} msg{mi}: {len(block)} values, want {len(want)}")
            fail += 1
            continue
        for i, ((lat, lon, s), wbits) in enumerate(zip(block, want)):
            if s == "inf": got = 0x7F800000
            elif s == "-inf": got = 0xFF800000
            elif s == "nan": got = 0x7FC00000
            else: got = f32bits(float(s))
            if got != wbits:
                print(f"FAIL: {name} msg{mi}[{i}] ({lat},{lon}) eccodes={s!r} bits={got:08x} want={wbits:08x}")
                fail += 1
    return fail

# canonical identity for both fixtures: discipline 10, centre 98 (ecmf),
# refTime 2026-10-05T00Z, product 2.2, level type 10 value 850, 3x3 grid
# first point (45,10) last point (25,30).
SIMPLE_S1 = [f32bits(float(i)) for i in range(9)] # 0..8
SIMPLE_S2 = [f32bits(float(k) / 10.0) for k in range(1, 10)] # 0.1..0.9
IEEE = [0x00000000, 0x80000000, f32bits(1.5), f32bits(-0.5),
             0x7F800000, 0xFF800000, 0x7FC00000, 0x00000001, 0x7F7FFFFF]

fail = 0
fail += check("p31 eccodes values", sys.argv[1], [SIMPLE_S1])
fail += check("dual eccodes values", sys.argv[2], [SIMPLE_S2, IEEE])
if fail:
    sys.exit(1)
print("PASS: eccodes values bit-exact (p31 msg0; dual msg0 simple + msg1 IEEE incl ±Inf/NaN/-0.0/denormal)")
PYEOF
[ $? -eq 0 ] || FAIL=1

# --- ③ wgrib2 summary -------------------------------------------------------
# Parameter shortName is discipline-dependent in wgrib2's tables: our fixtures
# use discipline 10 (oceanographic), where parameter 2.2 maps to DICED.
W_OUT=$(LD_LIBRARY_PATH=$CAPI "$WG" "$FX/p35_dual.grib2" </dev/null 2>&1)
[ "$(echo "$W_OUT" | grep -c ':d=2026100500:DICED:entire atmosphere:anl:')" = "2" ] \
  && ok "wgrib2 dual: 2 messages, date/param/level match" \
  || bad "wgrib2 dual summary: [$W_OUT]"
W1=$(LD_LIBRARY_PATH=$CAPI "$WG" "$FX/p31_frame_simple.grib2" </dev/null 2>&1)
[ "$(echo "$W1" | grep -c ':d=2026100500:DICED:entire atmosphere:anl:')" = "1" ] \
  && ok "wgrib2 p31: 1 message, date/param/level match" \
  || bad "wgrib2 p31 summary: [$W1]"

# --- ④ xcheck_bin C-API cross-check (msg0, order-normalized) -----------------
python3 - "$XCHECK" "$CAPI" "$FX/p31_frame_simple.grib2" "$FX/p35_dual.grib2" <<'PYEOF'
import os, re, subprocess, sys

xcheck, capi, p31, dual = sys.argv[1:5]
env = dict(os.environ, LD_LIBRARY_PATH=capi)

def run(f):
    # xcheck_bin emits raw control bytes on its wgrib2 arg-list echo line, and
    # the wgrib2 C-API library writes two small diagnostic files (message
    # summaries) into the CWD — pin cwd to /tmp so repo trees stay clean
    # (recurrence 2026-10-06 01:21, internal post).
    out = subprocess.run([xcheck, f], capture_output=True, env=env, cwd="/tmp")
    out = out.stdout.decode("utf-8", errors="replace")
    vals = re.search(r"^values:(.*)$", out, re.M)
    pts = re.findall(r"\(([-\d.]+), ([-\d.]+)\)", out)
    assert vals and len(pts) == 9, f"xcheck output malformed for {f}"
    # xcheck prints "(lon, lat)" pairs — normalize keys to (lat, lon).
    return dict(zip([(float(b), float(a)) for a, b in pts],
                    [float(x) for x in vals.group(1).split()]))

def grid_map(vals_rowmajor):
    """Our file order: row-major, lat 45→25 step -10, lon 10→30 step +10."""
    m = {}
    for i, v in enumerate(vals_rowmajor):
        m[(45.0 - 10.0 * (i // 3), 10.0 + 10.0 * (i % 3))] = v
    return m

def check(name, f, want_vals):
    got = run(f)
    want = grid_map(want_vals)
    if set(got) != set(want):
        print(f"FAIL: {name}: point sets differ: got={sorted(got)}")
        return 1
    fail = 0
    for p in want:
        if abs(got[p] - want[p]) > 5e-7: # %.6g tier
            print(f"FAIL: {name} at {p}: xcheck={got[p]} want={want[p]}")
            fail += 1
    return fail

fail = 0
fail += check("xcheck p31 msg0", p31, [float(i) for i in range(9)])
fail += check("xcheck dual msg0", dual, [k / 10.0 for k in range(1, 10)])
if fail:
    sys.exit(1)
print("PASS: xcheck_bin (wgrib2 C API) values + lonlat grid cross-check (msg0 of both fixtures)")
PYEOF
[ $? -eq 0 ] || FAIL=1

echo ""
if [ $FAIL -eq 0 ]; then
  echo "==== ORACLE: ALL CHECKS PASS (eccodes 2.41 + wgrib2 3.8 + xcheck C-API)"
else
  echo "==== ORACLE: FAILURES PRESENT"
fi
exit $FAIL
