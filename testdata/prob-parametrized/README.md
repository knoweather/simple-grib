# testdata/prob-parametrized

GRIB2 夹具：产品定义模板 **4.112** 与 **4.123**（概率块位置随 time-range
数量 NT 平移的两个模板），各一件 NT=1、一件 NT=3。

Fixtures for GRIB2 product definition templates 4.112 and 4.123 — the two
templates whose probability block sits at a parametrized offset
(`61+(NT-1)*12` and `66+(NT-1)*12`) — one NT=1 and one NT=3 message each.

## 内容 / Contents

| 文件 | 模板 | NT | 字节数 |
|---|---|---|---|
| `p112_nt1.grib2` | 4.112 | 1 | 237 |
| `p112_nt3.grib2` | 4.112 | 3 | 261 |
| `p123_nt1.grib2` | 4.123 | 1 | 264 |
| `p123_nt3.grib2` | 4.123 | 3 | 288 |

写入的哨兵值（全部经 ecCodes `codes_get` 读回确认）：

| 键 | 值 |
|---|---|
| `typeOfStatisticalProcessing` | 1 |
| `numberOfTimeRanges` | 1 / 3 |
| `probabilityType` | 5 |
| `scaleFactorOfLowerLimit` / `scaledValueOfLowerLimit` | 2 / 4444 |
| `scaleFactorOfUpperLimit` / `scaledValueOfUpperLimit` | 3 / 5555 |
| `numberOfForecastsInEnsemble`（仅 4.123） | 3333 |

## 来源与重建 / Provenance and regeneration

由 `python eccodes`（ecCodes **2.47.0** 的 Python 绑定）以 samples 里的
`GRIB2.tmpl`（regular_ll_sfc）为底生成；生成脚本同目录
`make_fixtures.py`，samples 目录指向 ecCodes 源码发行的 `samples/`：

```powershell
$env:ECCODES_SAMPLES_PATH = "<eccodes>/samples"
python make_fixtures.py
```

`make_fixtures.py` 同时打印 ecCodes 的 `codes_get_offset`（0-based 消息内
绝对偏移），据此可换算 section 4 的相对位置：四个夹具的 section 4 均从
0-based 109 开始，于是

- `numberOfTimeRanges` 相对 41（= 八位组 42）——两个模板都是；
- `typeOfStatisticalProcessing` 相对 46（= 八位组 47）——首条记录；
- `probabilityType` 相对 60 / 65（= 八位组 61 / 66），NT 每多一条前进 12。

`product_prob_parametrized_test.go` 用这四件夹具做双端对照。

## 为什么需要 NT=3 / Why NT=3

NT≥2 时重复块里有 NT 条 12 字节记录，ecCodes 的标量键
`typeOfStatisticalProcessing` 取**最后一条**：把三条记录手改成 11/22/33 后
`codes_get` 读出 33（`codes_get_array` 才是 `[11 22 33]`）。本库的
`spec.StatProcessOffsetFor` 就是按这个语义前进 `(NT-1)*12`；NT=3 的夹具
正是这条语义的回归锚点（若退化成读首条记录，会读到 255 而不是 1）。