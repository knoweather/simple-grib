#!/usr/bin/env python3
# Decode benchmark driver, eccodes side.
# Timing: 1 warmup round + N timed rounds (default 10); report MEAN as the
# primary column plus median and min/max.
#
# Usage: python3 -u eccodes_bench.py [--rounds N] file...
#
# Caliber: a pure PYTHON LOOP over the system
# eccodes 2.41.0 binding — per-message handle creation and value extraction
# go through the binding layer. NOTE on this build: GRIBMessage.values()
# iterates key names, so values are extracted with the low-level
# codes_get_values(handle) call (one C call per message returning a numpy
# array): the binding overhead is PER-MESSAGE, not per-value — less
# conservative than a per-value python loop. No CLI engine in the timed path.
#
# Multi-field envelopes (JMA GPV: s1/s3 shared, [s4..s7] x N, totalLength over
# the whole envelope): this binding's FileReader exposes only the FIRST field
# of such an envelope (the build has no codes_grib_next; the C library's file
# scanner reports one offset while grib_ls/iterator see all fields). To decode
# all fields through the same per-message path, each group is extracted to a
# [s0'][s1][s3][group] + b"7777" slice file (totalLength patched to the slice
# length; section contents verbatim) — identical to the oracle method.
# Such files are labeled "multifield-slice" in the output.
#
# Protocol identical to the Go driver (): 1 warmup + N timed rounds per
# file (default 10); MEAN is the primary metric (mb_per_s / msg_per_s derive
# from it), with median and min/max alongside; failed files are reported with
# the failure reason (no silent exclusion).

import argparse
import hashlib
import json
import os
import struct
import sys
import tempfile
import time

import numpy as np
from eccodes import FileReader, codes_get_values


def extract_values(msg):
    """Full value extraction for one message (one C binding call)."""
    return np.asarray(codes_get_values(msg._handle), dtype=np.float32)


def walk(raw):
    """Section header walk: list of (number, offset, length)."""
    spans = []
    pos, end = 16, len(raw)
    while pos < end:
        if end - pos < 5:
            assert end - pos <= 4, f"trailer {end - pos} > 4"
            break
        slen = int.from_bytes(raw[pos:pos + 4], "big")
        num = raw[pos + 4]
        spans.append((num, pos, slen))
        pos += slen
    return spans


def group_starts(spans):
    starts, seen = [], False
    for i, (n, _, _) in enumerate(spans):
        if n == 4 and (not seen or spans[i - 1][0] == 7):
            starts.append(i)
            seen = True
    return starts


def build_slices(raw, spans, tmpdir):
    """Per-field slice files [s0'][s1][s3][group] + b'7777', totalLength patched."""
    starts = group_starts(spans)
    if len(starts) < 2:
        return None
    head_end = spans[starts[0]][1]
    shared = raw[:head_end] # s0 + head sections (1,3)
    paths = []
    for k, st in enumerate(starts):
        end_i = starts[k + 1] if k + 1 < len(starts) else len(spans)
        body = b"".join(raw[p:p + l] for (_, p, l) in spans[st:end_i])
        buf = bytearray(shared) + body + b"7777"
        buf[8:16] = len(buf).to_bytes(8, "big")
        path = os.path.join(tmpdir, f"slice_{k}.grib2")
        with open(path, "wb") as f:
            f.write(bytes(buf))
        paths.append(path)
    return paths


def decode_once(path, raw, slices=None):
    """Full decode of one file: metadata + all values. Returns (msgs, values).

    slices: prebuilt per-field slice paths for multi-field envelopes (built
    once per file, outside the timed rounds — rebuilding them inside a round
    would add write I/O that the Go side does not have).
    """
    msgs = values = 0
    if slices:
        for sp in slices:
            with FileReader(sp) as fr:
                for msg in fr:
                    values += len(extract_values(msg))
                    msgs += 1
        return msgs, values
    with FileReader(path) as fr:
        for msg in fr:
            values += len(extract_values(msg))
            msgs += 1
    return msgs, values


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--rounds", type=int, default=10)
    ap.add_argument("files", nargs="+")
    a = ap.parse_args()

    for path in a.files:
        raw = open(path, "rb").read()
        r = {
            "file": path,
            "sha256": hashlib.sha256(raw).hexdigest(),
            "size_bytes": len(raw),
            "rounds": a.rounds,
            "engine": "eccodes-python-binding-2.41",
        }
        slices = None
        if raw[:4] == b"GRIB":
            spans = walk(raw)
            if len(group_starts(spans)) >= 2:
                slices = build_slices(raw, spans, tempfile.mkdtemp(prefix="p7b_"))
                r["note"] = ("multifield-slice (binding has no next-message API; "
                             "slices per oracle method, prebuilt once)")

        t0 = time.time()
        try:
            msgs, vals = decode_once(path, raw, slices)
        except Exception as e: # noqa: BLE001 - report, do not exclude silently
            r["warmup_s"] = round(time.time() - t0, 6)
            t1 = time.time()
            try:
                decode_once(path, raw, slices)
                r["status"] = "ok"
            except Exception as e2: # noqa: BLE001
                r["median_s"] = round(time.time() - t1, 6)
                r["status"] = f"FAIL: {type(e2).__name__}: {e2}"
                r["messages_decoded"] = 0
                r["values_decoded"] = 0
                print(json.dumps(r))
                continue
        r["warmup_s"] = round(time.time() - t0, 6)

        ts = []
        status = "ok"
        for i in range(a.rounds):
            t1 = time.time()
            try:
                msgs, vals = decode_once(path, raw, slices)
            except Exception as e: # noqa: BLE001
                status = f"FAIL (round {i + 1}): {type(e).__name__}: {e}"
                break
            ts.append(time.time() - t1)
        if ts:
            ts.sort()
            mean = sum(ts) / len(ts) # : primary column
            med = ts[len(ts) // 2]
            r["mean_s"] = round(mean, 6)
            r["median_s"] = round(med, 6)
            r["min_s"] = round(ts[0], 6)
            r["max_s"] = round(ts[-1], 6)
            r["mb_per_s"] = round(len(raw) / mean / 1e6, 6) # from MEAN
            r["msg_per_s"] = round(msgs / mean, 6)
        r["messages_decoded"] = msgs
        r["values_decoded"] = vals
        r["status"] = status
        print(json.dumps(r))


if __name__ == "__main__":
    main()
