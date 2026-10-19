#!/usr/bin/env python3
# complex53-oracle.py — 独立解码 oracle：GRIB2 template 5.2/5.3 (complex packing [+ spatial differencing])
#
# 用途：/ 的 Go 实现（pack/complex.go 等）的对拍基准。本脚本从 WMO FM 94 布局 +
# eccodes master 源码（src/eccodes/accessor/DataG22OrderPacking.cc unpack/post_process，
# grib_bits.cc grib_decode_signed_longb）独立手写，不依赖任何现成 GRIB 解码库
# （仅用 eccodes python 绑定取参考值做对拍）。
#
# 运行环境：the Linux bench host（/root/simple-grib 同机；需 python3 + python-eccodes 2.41+）
# python3 complex53-oracle.py <file.grib2> [ncheck]
# ncheck 仅控制打印前几个值；全字段逐位对拍恒执行。
#
# 校准记录（B，2026-10-05，板面 P4-B1 完成报告）：
# C6_gefs_ap5_f000_HGT10mb.grib2 (noaa-gefs-pds, md5 见台账) : 0/259920 失配, maxrelerr=0
# S1_hrrr_f00_PRES_L25.grib2 (noaa-hrrr-bdp-pds, md5 见台账): 0/1193781 失配, maxrelerr=0
#
# 真值锚（B，2026-10-06，AWS tmpl=2 普查后）：公开 AWS GRIB2 无生产方真 tmpl=2
# （全文件级扫描：NCEP 2026 HRRR/GEFS={3}、GFS waves={40}、GFS 全球已转 BUFR、
# ECMWF aifs/ifs/wave={42}+{0}、0p4-beta oper 82 消息全 {0}、enkfgdas=NEMSIO）。
# 改走「真值 + 权威编码」路线：eccodes 2.41 grib_set -s dataRepresentationTemplateNumber=2
# 将 ECMWF IFS 0p4-beta oper 分析（ifs04beta_20230118_00z_0h.grib2, s3://ecmwf-forecasts）
# 单条消息重打包为 tmpl=2，值 digest 前后一致（保真证明）。两锚均带组拆分（gs=1, NG>1）
# ——与 NCEP 真 5.3（C6: NG=15900）同构，覆盖 必须实现的组拆分路径：
# C46 complex_52_t850_real.grib2 (t/850, 900x451 高斯, NV=405900, NG=19293, bitsW=4/bitsL=7)
# md5=cc89829e0f9f486c9a1d595b3752d9ee; 值 digest(grib_get_data)=b44828fd636ad729b2af6c48a76316f0
# sec5: R=229.1488037109375 Eraw=8005(-5) D=0 bits=12; 源消息 tmpl=0 md5 见台账
# C47 complex_52_gh500_real.grib2 (gh/500, 同网格, NV=405900, NG=20956, refL=2)
# md5=d243039b322d43fe77c6c4ca31daf330; 值 digest(grib_get_data)=9013f4c0755417647dbbc8525bb7e433
# sec5: R=4811.890625 Eraw=8001(-1) D=0 bits=12
#
# 布局要点（实证）：
# sec5 = NV[4] tmpl[2] R(F32)[4] B(smag16)[2] D(smag16)[2] bpv[1] typeOrig[1] gsm[1] mvm[1]
# pms[4] sms[4] NG[4] rgw[1] nbwg[1] rgl[4] ligl[1] tllg[4] nbsl[1] osd[1] noed[1] (=49B)
# sec7 数据模板 = [extras×osd (各 noed*8 bit, 无符号)][bias (noed*8 bit, 符号-幅度!)]（仅 tmpl=3/osd>0）
# + 顺序位流 [refs: NG×bpv][widths: NG×nbwg][lengths: NG×nbsl]（各路按字节取整）
# [values: Σwidth_i bit]
# 每组 value_j = ref_i + unsigned(width_i)；len_i = scaled*ligl+rgl（末组 = tllg）
# osd=2 逆推：vals[0]=extras[0], vals[1]=extras[1]（覆写）; v_j = coded_j+bias+2v_{j-1}-v_{j-2}
# 最终 val = (raw × 2^B + R) × 10^(−D)
# ⚠ bias 是符号-幅度（grib_decode_signed_longb），不是二进制补码。

import struct
import sys


def smag(b):
    v = int.from_bytes(b, "big")
    return -(v & 0x7FFF) if v & 0x8000 else v


class BitReader:
    def __init__(self, data, bitpos=0):
        self.d = data
        self.p = bitpos

    def u(self, nbits):
        v = 0
        for _ in range(nbits):
            byte_i, bit_i = divmod(self.p, 8)
            v = (v << 1) | ((self.d[byte_i] >> (7 - bit_i)) & 1)
            self.p += 1
        return v

    def s(self, nbits):
        # eccodes grib_decode_signed_longb: sign-magnitude (first bit sign, rest magnitude)
        sign = self.u(1)
        mag = self.u(nbits - 1)
        return -mag if sign else mag


def decode_msg(path, ncheck=8):
    import eccodes

    d = open(path, "rb").read()
    mlen = struct.unpack(">Q", d[8:16])[0]
    secs = {}
    p = 16
    while p + 5 <= 16 + mlen:
        ln, snum = struct.unpack(">IB", d[p : p + 5])
        secs[snum] = (p, ln)
        if snum == 7:
            break
        p += ln
    s5o, s5l = secs[5]
    s5 = d[s5o : s5o + s5l]
    NV = struct.unpack(">I", s5[5:9])[0]
    tmpl = struct.unpack(">H", s5[9:11])[0]
    R = struct.unpack(">f", s5[11:15])[0]
    B = smag(s5[15:17])
    D = smag(s5[17:19])
    bpv = s5[19]
    NG = struct.unpack(">I", s5[31:35])[0]
    rgw = s5[35]
    nbwg = s5[36]
    rgl = struct.unpack(">I", s5[37:41])[0]
    ligl = s5[41]
    tllg = struct.unpack(">I", s5[42:46])[0]
    nbsl = s5[46]
    osd = s5[47] if tmpl == 3 else 0
    noed = s5[48] if tmpl == 3 else 0
    if tmpl not in (2, 3) or NG == 0:
        print(f"SKIP {path}: tmpl={tmpl} NG={NG} (非 complex packing 组式编码)")
        return True
    s7o, s7l = secs[7]
    data = d[s7o + 5 : s7o + s7l] # 数据模板自 section 头 5 字节后起

    # 位流布局：[extras+refs][widths][lengths][values]，各路按字节向上取整
    ref_bits = NG * bpv
    if osd:
        ref_bits += (1 + osd) * noed * 8
    ref_bytes = (ref_bits + 7) // 8
    w_bytes = (NG * nbwg + 7) // 8
    l_bytes = (NG * nbsl + 7) // 8
    off_ref, off_w, off_l, off_v = 0, ref_bytes, ref_bytes + w_bytes, ref_bytes + w_bytes + l_bytes

    br_ref = BitReader(data, off_ref * 8)
    br_w = BitReader(data, off_w * 8)
    br_l = BitReader(data, off_l * 8)
    br_v = BitReader(data, off_v * 8)

    extras = [0] * osd
    bias = 0
    if osd:
        for i in range(osd):
            extras[i] = br_ref.u(noed * 8)
        bias = br_ref.s(noed * 8)

    raw = []
    for i in range(NG):
        ref_i = br_ref.u(bpv)
        len_i = br_l.u(nbsl) * ligl + rgl
        width_i = br_w.u(nbwg) + rgw
        if i == NG - 1:
            len_i = tllg
        for _j in range(len_i):
            raw.append(ref_i + br_v.u(width_i))
    assert len(raw) == NV, f"raw count {len(raw)} != NV {NV}"

    # 空间差分逆推（post_process 实证；osd=1 分支同式退化）
    if osd == 2:
        raw[0] = extras[0]
        raw[1] = extras[1]
        pen, last = extras[0], extras[1]
        for j in range(2, NV):
            raw[j] = raw[j] + bias + last + last - pen
            pen = last
            last = raw[j]
    elif osd == 1:
        raw[0] = extras[0]
        last = extras[0]
        for j in range(1, NV):
            raw[j] = raw[j] + bias + last
            last = raw[j]

    scale2b = 2.0**B
    scale10 = 10.0 ** (-D)
    vals = [(r * scale2b + R) * scale10 for r in raw]

    g = eccodes.codes_grib_new_from_file(open(path, "rb"))
    ev = [b["value"] for b in eccodes.codes_grib_get_data(g)]
    eccodes.codes_release(g)
    assert len(ev) == NV
    mism = 0
    maxerr = 0.0
    for i in range(NV):
        err = abs(vals[i] - ev[i]) / max(1.0, abs(ev[i]))
        if err > 1e-9:
            mism += 1
            if mism <= 5:
                print(f" MISMATCH[{i}] mine={vals[i]!r} eccodes={ev[i]!r}")
        maxerr = max(maxerr, err)
    for i in range(min(ncheck, NV)):
        print(f" [{i}] {vals[i]!r}")
    status = "PASS" if mism == 0 else f"FAIL({mism})"
    print(
        f"== {path.split('/')[-1]}: NV={NV} tmpl={tmpl} B={B} D={D} bpv={bpv} NG={NG} "
        f"osd={osd} noed={noed} extras={extras} bias={bias}\n FULL-FIELD: {status} mismatches={mism}/{NV} maxrelerr={maxerr:.3e}"
    )
    return mism == 0


if __name__ == "__main__":
    argv = [a for a in sys.argv[1:] if not a.isdigit()]
    narg = [a for a in sys.argv[1:] if a.isdigit()]
    paths = argv or [
        "/root/aws-xcheck/C6_gefs_ap5_f000_HGT10mb.grib2",
        "/root/ncep-xcheck/S1_hrrr_f00_PRES_L25.grib2",
    ]
    n = int(narg[0]) if narg else 8
    ok = all(decode_msg(p, n) for p in paths)
    sys.exit(0 if ok else 1)
