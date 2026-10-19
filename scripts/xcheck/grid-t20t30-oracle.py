#!/usr/bin/env python3
"""grid-t20t30-oracle.py — 独立参考解码 oracle：GRIB2 template 3.20 (polar stereographic)
/ 3.30 (Lambert conformal conic) 网格坐标。

用途（ 验收线④「坐标 vs eccodes lat/lon 口径钉死」）：
  1. 独立字节解析 sec3 模板字段（不经任何解码库；布局 = COORDINATION.md 板面 B 报告 +
     eccodes template.3.20.def / template.3.30.def 双证，2026-10-05）；
  2. eccodes (the Linux bench host, python-eccodes) 读同组键交叉核对（逐字段打印）；
  3. 输出全网格 lat/lon 向量摘要 + sha256 digest（稳定金锚，重放可比对）。

公式权威源 = eccodes master geo/iterator/PolarStereographic.cc（Snyder mcs/tcs 式，
LaD=标准纬圈，grid 原点 (0,0)=首点、x0/y0 存 false easting/northing）与
LambertConformal.cc（Snyder 7-1 n/f/rho0_bare 式 + xy2lonlat 逆推；GRIB-405: Dy 不随
jScansPositively 变号）。

已知真值锚（the Linux bench host）：
  t20: S1 HRRR PRES L25 md5=76114c7bb6705cc4b4df06ff3dfd28ac
       coord-digest sha256=8eaac3c1995b76f8d7cb4d157d25f3b3db786db2585b59ac9d975006c6f49819
       （1,193,781 点；LaD=60 orient=225 Dx=Dy=3km scanMode=64）
       合成 fixture testdata/gribw-xcheck/t20_grid_3x3.grib2
       coord-digest sha256=51895186748c7104374d39f90728ff1efa33714974242aa211cee6686ea3d460
       （LaD=60 orient=0 Dx=Dy=500km scanMode=192；第二点手算 Snyder 交叉验证吻合）
       parity fixture testdata/gribw-xcheck/t20_even_4x3.grib2（Ni even×Nj odd，NV=12）
       coord-digest sha256=b81357f291eb432ae758476e922a91fef692d997b5984e554bb1548418c097b0
  t30: 合成 fixture testdata/gribw-xcheck/t30_grid_3x3.grib2
       coord-digest sha256=0d79958624827c884fb981bb3211d6ed16969dc7074f6dd6b789b2f0550914bd
       （LaD=45 LoV=260 Latin1/2=30/60 Dx=Dy=300km scanMode=192——双标准纬圈分支）
       parity fixture testdata/gribw-xcheck/t30_even_3x4.grib2（Ni odd×Nj even，NV=12）
       coord-digest sha256=76182bd500bffbc8644180bd47dc6c0acac3256379c71caaec462e023feb1ec4
       真样本 C38 HRRR conus APCP md5=8d69916c12608a546bb6366edc3e5039
       coord-digest sha256=1a76ed119dd37ef0a7902a1fdcebb0854af76f5573d6b843627f6da4140bc038
       （Ni×Nj=1799×1059；LaD=Latin1=Latin2=38.5°——单标准纬圈 n=sin(Latin1) 退化分支、
        LoV=262.5 Dx=Dy=3km scanMode=64）
       注：公开 AWS 桶（noaa-nam-pds 2026 = regular_ll、gfs.pnw 空目录）无 t30，但取样台账
       C38（HRRR wrfnat conus）即 t30 真样本——早期「t30 缺如」结论系未查台账，已更正。

Go 侧 golden 口径（packed float64 BE sha256，2026-10-06 预计算 @ the Linux bench host eccodes 2.41，
供 build-end 翻转 TestB_GridT30Baseline / t20 golden 直接粘贴）：
   S1 (t20, n=1193781): lat_f64be=482fdb6c2452e08fece2c8704076851dea388dfbea4436ea919dcacf868ec165
                         lon_f64be=32eecf54b153fb710b2db5bfb42c53325c49b27f1f498068920dde4a2df2dc2b
   C38 (t30, n=1905141): lat_f64be=e7e83427cb7a9abbac52101c5d2b0f9bee9cca3156ac9658967a66db985ad5b8
                          lon_f64be=8c43b60a10e552d20ce5333627e2840931f1f225fda76bcfdd4f6e011c6505fc

用法: python3 grid-t20t30-oracle.py <file.grib2> [<file2> ...]
退出码: 0 = 全部文件解析+解码成功; 1 = 任一失败。
"""
import eccodes
import hashlib
import struct
import sys


def _sec3(path):
    d = open(path, 'rb').read()
    p = 16
    while p + 5 <= len(d):
        ln, snum = struct.unpack('>IB', d[p:p+5])
        if snum == 3:
            return d[p:p+ln]
        if snum == 7:
            break
        p += ln
    raise ValueError(f'{path}: section 3 not found')


def parse_t20(sec):
    """Independent byte parse of template 3.20 (offsets 0-based into section)."""
    return {
        'shape': sec[14],
        'Nx': struct.unpack('>I', sec[30:34])[0],
        'Ny': struct.unpack('>I', sec[34:38])[0],
        'La1_deg': struct.unpack('>i', sec[38:42])[0] / 1e6,
        'Lo1_deg': struct.unpack('>I', sec[42:46])[0] / 1e6,
        'resFlags': sec[46],
        'LaD_deg': struct.unpack('>i', sec[47:51])[0] / 1e6,
        'orient_deg': struct.unpack('>i', sec[51:55])[0] / 1e6,
        'Dx_m': struct.unpack('>I', sec[55:59])[0] * 1e-3,
        'Dy_m': struct.unpack('>I', sec[59:63])[0] * 1e-3,
        'projCentreFlag': sec[63],
        'scanMode': sec[64],
    }


def parse_t30(sec):
    """Independent byte parse of template 3.30 (offsets 0-based into section)."""
    return {
        'shape': sec[14],
        'Nx': struct.unpack('>I', sec[30:34])[0],
        'Ny': struct.unpack('>I', sec[34:38])[0],
        'La1_deg': struct.unpack('>i', sec[38:42])[0] / 1e6,
        'Lo1_deg': struct.unpack('>I', sec[42:46])[0] / 1e6,
        'resFlags': sec[46],
        'LaD_deg': struct.unpack('>i', sec[47:51])[0] / 1e6,
        'LoV_deg': struct.unpack('>I', sec[51:55])[0] / 1e6,
        'Dx_m': struct.unpack('>I', sec[55:59])[0] * 1e-3,
        'Dy_m': struct.unpack('>I', sec[59:63])[0] * 1e-3,
        'projCentreFlag': sec[63],
        'scanMode': sec[64],
        'Latin1_deg': struct.unpack('>i', sec[65:69])[0] / 1e6,
        'Latin2_deg': struct.unpack('>i', sec[69:73])[0] / 1e6,
    }


def _ecc_key(g, k):
    try:
        return eccodes.codes_get_double(g, k)
    except Exception as e:
        return f'ERR {str(e)[:40]}'


def run(path):
    sec = _sec3(path)
    tmpl = struct.unpack('>H', sec[12:14])[0]
    if tmpl not in (20, 30):
        print(f'== {path}: SKIP tmpl={tmpl} (oracle covers 20/30 only)')
        return True
    f = parse_t20(sec) if tmpl == 20 else parse_t30(sec)
    print(f'== {path}: sec3 len={len(sec)} tmpl={tmpl}')
    for k, v in f.items():
        print(f' byte-parse {k} = {v}')

    g = eccodes.codes_grib_new_from_file(open(path, 'rb'))
    keys = (['Ni', 'Nj', 'latitudeOfFirstGridPoint', 'longitudeOfFirstGridPoint',
             'latitudeWhereDxAndDyAreSpecified', 'orientationOfTheGridInDegrees',
             'DxInMetres', 'DyInMetres', 'projectionCentreFlag'] if tmpl == 20 else
            ['Ni', 'Nj', 'latitudeOfFirstGridPoint', 'longitudeOfFirstGridPoint',
             'latitudeWhereDxAndDyAreSpecified', 'LoVInDegrees',
             'DxInMetres', 'DyInMetres', 'projectionCentreFlag',
             'firstLatitude', 'secondLatitude'])
    print(' -- eccodes cross-check --')
    for k in keys:
        print(f' {k} = {_ecc_key(g, k)}')

    data = eccodes.codes_grib_get_data(g)
    lats = [b['lat'] for b in data]
    lons = [b['lon'] for b in data]
    n = len(lats)
    expect_n = f['Nx'] * f['Ny']
    ok = (n == expect_n)
    print(f' -- coordinates: n={n} (expect {expect_n}) --')
    print(f' first3 = {list(zip([round(a, 6) for a in lats[:3]], [round(b, 6) for b in lons[:3]]))}')
    print(f' last3 = {list(zip([round(a, 6) for a in lats[-3:]], [round(b, 6) for b in lons[-3:]]))}')
    print(f' lat range [{min(lats):.6f}, {max(lats):.6f}] lon range [{min(lons):.6f}, {max(lons):.6f}]')
    vec = ''.join(f'{la:.10f},{lo:.10f};' for la, lo in zip(lats, lons))
    print(' coord-digest sha256 =', hashlib.sha256(vec.encode()).hexdigest())
    eccodes.codes_release(g)
    return ok


if __name__ == '__main__':
    paths = sys.argv[1:] or []
    if not paths:
        print(__doc__.strip().splitlines()[-1])
        sys.exit(2)
    ok = all(run(p) for p in paths)
    sys.exit(0 if ok else 1)
