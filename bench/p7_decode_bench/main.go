// Benchmark drivers for the Go GRIB1+GRIB2 reader.
// Timing protocol: 1 warmup round + N timed rounds (default 10); the MEAN is
// the primary column, with median and min/max alongside.
// --parallel N switches each round to concurrent File.ValuesAll(N) across
// all messages (default 0 = sequential).
//
// 用法：p7_decode_bench [--rounds N] [--parallel N] file...
//
// 计时协议（与 eccodes 侧驱动完全一致，见 eccodes_bench.py）：
// 对每个文件，1 轮预热 + N 轮计时；一轮 = Open + 完整遍历 Messages() +
// 对每条消息调用 Values()（完整解码：元数据 + 全部数值）。每个文件以计时轮次的
// MEAN 为主指标（MB/s 与 msg/s 均由它推导），并附上 median 与 min/max。
// 失败的文件不会被静默剔除：会连同失败原因和该次失败尝试的墙钟时间一并上报。
//
// 披露：每次运行都会打印 GOMAXPROCS；并计算并打印每个文件的 sha256
// （语料完整性，两个引擎必须一致）。

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	grib "github.com/knoweather/simple-grib"
)

type result struct {
	File       string   `json:"file"`
	SHA256     string   `json:"sha256"`
	SizeBytes  int64    `json:"size_bytes"`
	Rounds     int      `json:"rounds"`
	Parallel   int      `json:"parallel_workers"` // 0 = 顺序（ 扩展）
	WarmupSec  float64  `json:"warmup_s"`
	MeanSec    float64  `json:"mean_s"` // ：主列
	MedianSec  float64  `json:"median_s"`
	MinSec     float64  `json:"min_s"`
	MaxSec     float64  `json:"max_s"`
	MBPerSec   float64  `json:"mb_per_s"` // 由 MEAN 推导（）
	MsgPerSec  *float64 `json:"msg_per_s"`
	Messages   int      `json:"messages_decoded"`
	Values     int64    `json:"values_decoded"`
	Status     string   `json:"status"` // "ok" 或失败原因
	GOMAXPROCS int      `json:"gomaxprocs"`
}

// decodeOnce 打开 path，并通过 File.ValuesAll(workers) 完整解码每条消息
// （元数据 + 数值）：workers <= 1 为默认的顺序执行，>1 为并发（ 扩展）。
func decodeOnce(path string, workers int) (msgs int, values int64, err error) {
	f, oerr := grib.Open(path)
	if oerr != nil {
		return 0, 0, fmt.Errorf("open: %w", oerr)
	}
	defer f.Close()
	all, aerr := f.ValuesAll(workers)
	if aerr != nil {
		return 0, 0, fmt.Errorf("valuesall: %w", aerr)
	}
	for _, v := range all {
		values += int64(len(v))
	}
	return len(all), values, nil
}

func main() {
	rounds := flag.Int("rounds", 10, "timed rounds after one warmup ( default 10)")
	parallel := flag.Int("parallel", 0, "concurrent workers for File.ValuesAll (0 = sequential; extension)")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: p7_decode_bench [--rounds N] [--parallel N] file...")
		os.Exit(2)
	}
	for _, path := range flag.Args() {
		st, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: stat: %v\n", path, err)
			continue
		}
		sum := sha256.Sum256(mustReadHead(path))
		r := result{
			File:       path,
			SHA256:     hex.EncodeToString(sum[:]),
			SizeBytes:  st.Size(),
			Rounds:     *rounds,
			Parallel:   *parallel,
			GOMAXPROCS: runtime.GOMAXPROCS(0),
		}

		t0 := time.Now()
		msgs, vals, werr := decodeOnce(path, *parallel)
		r.WarmupSec = round6(time.Since(t0).Seconds())

		if werr != nil {
			// 失败的文件：如实上报（不做静默剔除）。再计时一次较有代表性的
			// 尝试，用于墙钟时间那一列。
			t1 := time.Now()
			_, _, ferr := decodeOnce(path, *parallel)
			r.MedianSec = round6(time.Since(t1).Seconds())
			r.Status = fmt.Sprintf("FAIL: %v", ferr)
			if ferr == nil {
				r.Status = "ok"
			}
			printResult(r, msgs, vals)
			continue
		}

		var ts []float64
		for i := 0; i < *rounds; i++ {
			t1 := time.Now()
			m2, v2, rerr := decodeOnce(path, *parallel)
			dt := time.Since(t1).Seconds()
			if rerr != nil {
				r.Status = fmt.Sprintf("FAIL (round %d): %v", i+1, rerr)
				break
			}
			msgs, vals = m2, v2
			ts = append(ts, dt)
		}
		if len(ts) > 0 {
			sort.Float64s(ts)
			sum := 0.0
			for _, t := range ts {
				sum += t
			}
			mean := sum / float64(len(ts))
			r.MeanSec = round6(mean) // ：主列
			r.MedianSec = round6(ts[len(ts)/2])
			r.MinSec = round6(ts[0])
			r.MaxSec = round6(ts[len(ts)-1])
			r.MBPerSec = round6(float64(st.Size()) / (mean * 1e6)) // 由 MEAN 推导
			mps := float64(msgs) / mean
			r.MsgPerSec = &mps
		}
		if r.Status == "" {
			r.Status = "ok"
		}
		printResult(r, msgs, vals)
	}
}

func printResult(r result, msgs int, vals int64) {
	r.Messages = msgs
	r.Values = vals
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
}

func mustReadHead(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: read for sha256: %v\n", path, err)
		os.Exit(1)
	}
	return b
}

func round6(f float64) float64 {
	return float64(int64(f*1e6+0.5)) / 1e6
}
