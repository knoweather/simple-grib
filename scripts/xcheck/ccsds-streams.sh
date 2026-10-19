#!/bin/bash
# ccsds-streams.sh — extract the CCSDS AEC bitstreams (section 7 data, octet
# 6 onward) from the two ECMWF open-data samples into a stable fixture dir.
# Companion to ccsds-oracle.sh (B 1425/1436): same source files, same
# convention. Runs on the Linux bench host (or anywhere with the sample files).
#
# CCSDS_SAMPLES=/root/aws-ecmwf-samples CCSDS_XCHECK_DIR=/root/ccsds-xcheck \
# bash scripts/xcheck/ccsds-streams.sh
#
# Output: <CCSDS_XCHECK_DIR>/ccsds_aifs_stream.bin (1,201,341 B,
# md5 cd29c999dbe477aca7e117e2f86ef4a4) and ccsds_ifs_stream.bin
# (589,229 B, md5 476f20cbb5b93216398ee7ad8cf51b8d). The Go golden test
# (pack/ccsds_golden_test.go) verifies these md5s before use.
set -u
S=${CCSDS_SAMPLES:-/root/aws-ecmwf-samples}
OUT=${CCSDS_XCHECK_DIR:-/root/ccsds-xcheck}
mkdir -p "$OUT"

python3 - "$S" "$OUT" <<'EOF'
import struct, hashlib, sys, os
src, out = sys.argv[1], sys.argv[2]
for f, name in [(os.path.join(src, 'aifs025_oper.grib2'), 'aifs'),
                (os.path.join(src, 'ifs025_oper.grib2'), 'ifs')]:
    d = open(f, 'rb').read()
    p = 16
    while p + 5 <= len(d):
        ln, sn = struct.unpack('>IB', d[p:p+5])
        if sn == 7:
            stream = d[p+5:p+ln]
            open(os.path.join(out, f'ccsds_{name}_stream.bin'), 'wb').write(stream)
            print(f'{name}: stream={len(stream)} md5={hashlib.md5(stream).hexdigest()}')
            break
        p += ln
EOF
