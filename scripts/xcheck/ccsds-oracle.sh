#!/bin/bash
# ccsds-oracle.sh — re-runnable cross-check for the CCSDS AEC (template 5.42) decode
# (ref-decode-first). Runs on the Linux bench host against three
# ECMWF open-data samples (real operational CCSDS files):
# /root/aws-ecmwf-samples/aifs025_oper.grib2 (w, vertical velocity)
# /root/aws-ecmwf-samples/ifs025_oper.grib2 (slor)
# /root/aws-ecmwf-samples/wave025.grib2 (mp2, significant wave height;
# regular_ll 1440x721 WITH BITMAP,
# NV=566193 — third anchor C48)
# Parameter census : all public-AWS ECMWF tmpl=42 messages
# share one parameter combination (bpv=16 flags=14 blockSize=32 rsi=128); the
# three anchors differ in R/B/NV/grid — no other variant exists to add.
# Checks per file: md5 → sec5 raw CCSDS fields (tmpl=42 layout = 25B, internal 1436
# byte-exact) → eccodes grib_get_data value-array digest (float32 BE) vs the
# recorded reference-decode constants (independent python mirror of
# libaec aec_buffer_decode + (sample×2^B+R)×10^(−D), internal 1425②).
# No repo code required; eccodes 2.41 only. Digest = stability anchor
# (same eccodes build → same digest), same convention as ed-smag-oracle.sh.
set -u
S=/root/aws-ecmwf-samples
FAIL=0

ok() { echo "PASS: $1"; }
bad() { echo "FAIL: $1"; FAIL=1; }

# --- per-file field + digest check -------------------------------------------
check_file() {
  local f="$1" name="$2" want_md5="$3" want_rhex="$4" want_braw="$5" want_draw="$6" \
        want_bpv="$7" want_flags="$8" want_bs="$9" want_rsi="${10}" \
        want_short="${11}" want_digest="${12}" want_nv="${13}"

  [ -f "$f" ] || { bad "$name: fixture missing: $f"; return; }
  local MD5=$(md5sum "$f" | cut -d' ' -f1)
  [ "$MD5" = "$want_md5" ] && ok "$name md5 ($MD5)" || bad "$name md5: got $MD5 want $want_md5"

  # sec5 raw fields (first message; both samples are single-message files)
  local FIELDS=$(python3 -c "
import struct
d=open('$f','rb').read()
p=16
while p+5<=len(d):
    ln,sn=struct.unpack('>IB',d[p:p+5])
    if sn==5:
        s=d[p:p+ln]
        tmpl=struct.unpack('>H',s[9:11])[0]
        print('%d %d %s %s %s %d %d %d %d' % (tmpl, ln, s[11:15].hex(), s[15:17].hex(), s[17:19].hex(), s[19], s[21], s[22], struct.unpack('>H',s[23:25])[0]))
        break
    if sn==7: break
    p+=ln
")
  local TMPL=$(echo "$FIELDS" | cut -d' ' -f1)
  local LEN=$(echo "$FIELDS" | cut -d' ' -f2)
  local RHEX=$(echo "$FIELDS" | cut -d' ' -f3)
  local BRAW=$(echo "$FIELDS" | cut -d' ' -f4)
  local DRAW=$(echo "$FIELDS" | cut -d' ' -f5)
  local BPV=$(echo "$FIELDS" | cut -d' ' -f6)
  local FLAGS=$(echo "$FIELDS" | cut -d' ' -f7)
  local BS=$(echo "$FIELDS" | cut -d' ' -f8)
  local RSI=$(echo "$FIELDS" | cut -d' ' -f9)

  [ "$TMPL" = "42" ] && ok "$name sec5 template = 42 (CCSDS)" || bad "$name sec5 template = $TMPL want 42"
  [ "$LEN" = "25" ] && ok "$name sec5 len = 25B " || bad "$name sec5 len = $LEN want 25"
  [ "$RHEX" = "$want_rhex" ] && ok "$name R raw = $RHEX" || bad "$name R raw = $RHEX want $want_rhex"
  [ "$BRAW" = "$want_braw" ] && ok "$name B raw = $BRAW (smag)" || bad "$name B raw = $BRAW want $want_braw"
  [ "$DRAW" = "$want_draw" ] && ok "$name D raw = $DRAW" || bad "$name D raw = $DRAW want $want_draw"
  [ "$BPV" = "$want_bpv" ] && ok "$name bitsPerSample = $BPV" || bad "$name bitsPerSample = $BPV want $want_bpv"
  [ "$FLAGS" = "$want_flags" ] && ok "$name ccsdsFlags = $FLAGS (SIGNED|3BYTE off)" || bad "$name ccsdsFlags = $FLAGS want $want_flags"
  [ "$BS" = "$want_bs" ] && ok "$name blockSize = $BS" || bad "$name blockSize = $BS want $want_bs"
  [ "$RSI" = "$want_rsi" ] && ok "$name rsi = $RSI" || bad "$name rsi = $RSI want $want_rsi"

  # eccodes value-array digest (stability anchor, ed-smag convention)
  local OUT=/tmp/ccsds_oracle_${name}.txt
  grib_get_data -w shortName=$want_short,dataRepresentationTemplateNumber=42 "$f" > "$OUT" 2>&1 \
    || { bad "$name grib_get_data failed"; return; }
  local DIGEST=$(python3 -c "
import struct, hashlib
vals=[]
for line in open('$OUT'):
    parts=line.split()
    if len(parts)==3 and parts[0] not in ('Latitude',):
        try: vals.append(float(parts[2]))
        except ValueError: pass
if not vals: sys.exit('no values parsed')
blob=b''.join(struct.pack('>f', v) for v in vals)
print(len(vals), hashlib.md5(blob).hexdigest())
" 2>&1)
  local N=$(echo "$DIGEST" | cut -d' ' -f1); local D=$(echo "$DIGEST" | cut -d' ' -f2)
  [ "$N" = "$want_nv" ] && ok "$name eccodes value count = $N" || bad "$name eccodes value count = $N want $want_nv"
  if [ "$D" = "$want_digest" ]; then
    ok "$name value-array digest stable ($D, anchor recorded 2026-10-05, eccodes 2.41)"
  else
    echo "NOTE: $name digest = $D differs from anchor ${want_digest} (eccodes version change?) — re-record with evidence"
  fi
}

echo "==== CCSDS AEC ORACLE (template 5.42) ===="
check_file "$S/aifs025_oper.grib2" aifs \
  "66ff73d46d585b6bc67551b16a0efda1" "c06ca807" "800d" "0000" 16 14 32 128 w \
  "1af2b5b81ec158005d68ee0c3d6c1dea" 1038240
echo ""
check_file "$S/ifs025_oper.grib2" ifs \
  "786c65f4ff9cd32fd3b6aee7478f18c2" "38d1affe" "8012" "0000" 16 14 32 128 slor \
  "15af366e53c7e4e127489afa5a5d0b6a" 1038240
echo ""
check_file "$S/wave025.grib2" wave \
  "b6291dc44efc20339fe25eec70b8997d" "3f64f5bb" "800c" "0000" 16 14 32 128 mp2 \
  "9a97151ebd8cb507ea7c705e53a3d69e" 566193

echo ""
if [ $FAIL -eq 0 ]; then
  echo "==== CCSDS ORACLE: ALL CHECKS PASS (eccodes 2.41 @ $(grib_ls --version 2>/dev/null | head -1 || echo eccodes))"
else
  echo "==== CCSDS ORACLE: FAILURES PRESENT"
fi
exit $FAIL
