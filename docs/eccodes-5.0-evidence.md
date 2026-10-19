# eccodes 源码取证（ / ）—— ，2026-10-04

来源：`ecmwf/eccodes`（原 `ecmwf/codes` 已改名），develop 分支，2026-10-04 从 the Linux bench host 抓取。
注意：仓库结构已大改（libcodes → src/eccodes/accessor + geo/iterator）。

## ：模板 5.0 解码公式 —— eccodes 实际公式

**`src/eccodes/geo/...` 无关；核心在两个文件：**

1) `src/eccodes/accessor/DataSimplePacking.cc` — `DataSimplePacking::unpack<T>()`：
```cpp
s = codes_power<T>(binary_scale_factor, 2); // s = 2^E
d = codes_power<T>(-decimal_scale_factor, 10); // d = 10^(−D)
...
grib_decode_array<T>(buf, &pos, bits_per_value, reference_value, s, d, n_vals, val);
```

2) `src/eccodes/grib_bits_any_endian_simple.h` — `grib_decode_array<T>()`（慢速参考路径与快速路径同式）：
```cpp
x = ((lvalue * s) + reference_value) * d;
// 即 value = (raw × 2^E + R) × 10^(−D)
```

**结论**：
- E（binary scale factor）参与解码：raw 先乘 2^E。
- 10^(−D) 作用于**整个和**（raw×2^E + R）——即 **R 以预缩放形式存储**（参考值 273.15、D=2 → 文件里存 27315.0）。
- 旧式 `value = R + raw×10^(−D)` 两处不符（缺 2^E；10^(−D) 位置错）。
- **NaN sentinel**：eccodes simple packing 解码路径对 raw==0xFFFFFFFF **无特殊处理**（按普通值解）；WMO CSV notes 里也没有模板 5.0 的 all-ones→missing 规则（Note 38/39 只针对 complex packing 的 group reference）。
- WMO Note 56（Template_notes.csv）："Negative values of E or D shall be represented according to Regulation 92.1.5." → E/D 带符号 ✓（我们的 int16 解释正确）。

simple-grib 已按 eccodes 公式实现（pack/simple.go，单测含预缩放 R、E±、组合算例）。

## ：Gaussian 网格纬度 —— N = Nj（不是 Ni）

`src/eccodes/geo/iterator/Gaussian.cc` — `Gaussian::init()`：
```cpp
long trunc; /* number of parallels between a pole and the equator */
...
size = trunc * 2;
ret = grib_get_gaussian_latitudes(trunc, lats); // 填 2×trunc 个纬度
```

`src/eccodes/geo/grib_geography.cc` — `compute_gaussian_latitudes(long trunc, double* lats)`：
```cpp
const long nlat = trunc * 2;
...
for (legi = 0; legi < nlat; legi++) { // Legendre 递推算到 P_nlat
 legfonc = ((2.0*(legi+1)-1.0)*root*mem1 - legi*mem2)/(legi+1); ...
}
conv = legfonc / ((nlat*(mem2 - root*legfonc)) / (1.0 - root*root)); // Newton（P'_nlat）
...
lats[jlat] = asin(root) * rad2deg; // 北半球：纬度 = asin(正根)
lats[nlat - 1 - jlat] = -lats[jlat]; // 南半球镜像
```

**结论**：Legendre 多项式次数 = **nlat = 2×trunc = Nj**（全球网格总纬圈数）；
只解正根（北半球），纬度 = asin(x_i)，南半球镜像。T63 → Nj=128 → P_128。
 "N = Ni（每圈点数）" 有误，应为 **N = Nj**。

simple-grib 的 `legendreRoots(n)`/`gaussianColatitudes(n)` 已参数化：T6b 接线时传 **Nj**
（arccos 形式与 eccodes 的 asin 形式等价：lat = 90° − θ）。

## ：scan mode bit16 —— 未取证到

新架构里 scanMode 位解析不在 G2Grid.cc/GridSpec.cc/Latlon.cc（iterator 消费的是
派生 accessor jScansPositively 等，原始位解析位置未定位）。P1 维持 fail-loud 拒绝 bit16，
 真实数据验证时再定。

## 抓取命令备忘（the Linux bench host）
```bash
# 仓库已改名 ecmwf/codes → ecmwf/eccodes，默认分支 develop（不是 main！）
curl -sf https://raw.githubusercontent.com/ecmwf/eccodes/develop/src/eccodes/grib_bits_any_endian_simple.h
curl -sf https://raw.githubusercontent.com/ecmwf/eccodes/develop/src/eccodes/accessor/DataSimplePacking.cc
curl -sf https://raw.githubusercontent.com/ecmwf/eccodes/develop/src/eccodes/geo/grib_geography.cc
# 全树检索：
curl -s "https://api.github.com/repos/ecmwf/eccodes/git/trees/develop?recursive=1"
```
