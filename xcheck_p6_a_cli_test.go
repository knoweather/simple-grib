//
// 独立推导来源：CLI 期望值不取自 CLI 自身输出，而是同一 fixture 经库 API
// （Open/Message/Grid/Point/DataRep/Values）现场计算的值——两端任一漂移即失败。
// oracle.json（eccodes the Linux bench host 现场提取）由 xcheck_p6_a_grid_test.go 独立锚定库侧，
// 本测试闭合 CLI↔库 一致性环：oracle → 库 → CLI 三方一致。
//
// fixture 集：p6a_ll_basic（常规 ll）、p6a_ps / p6a_lcc（投影模板 20/30）、
// p6a_bitmap（sec6 bitmap，值路径只含有效点 §5.14）、p6a_ieee（5.4 IEEE float32）、
// p6a_unimpl_t10（未实现模板 fail-loud：points=0、无 firstPoint、error 非空）。

package grib

import (
	"bytes"
	"encoding/json"
	"math"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var (
	p6aCLIBinOnce sync.Once
	p6aCLIBinPath string
	p6aCLIBinErr  error
)

// p6aCLIBinary builds cmd/gribdump once per test binary run.
func p6aCLIBinary(t *testing.T) string {
	t.Helper()
	p6aCLIBinOnce.Do(func() {
		dir := t.TempDir()
		bin := filepath.Join(dir, "gribdump")
		if runtime.GOOS == "windows" {
			bin += ".exe" // go build appends the platform suffix
		}
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/gribdump")
		out, err := cmd.CombinedOutput()
		if err != nil {
			p6aCLIBinErr = err
			t.Logf("go build output: %s", out)
			return
		}
		p6aCLIBinPath = bin
	})
	if p6aCLIBinErr != nil {
		t.Fatalf("build gribdump: %v", p6aCLIBinErr)
	}
	return p6aCLIBinPath
}

// p6aCLIFile mirrors the JSON output model of cmd/gribdump (main.go fileOut).
type p6aCLIFile struct {
	File     string `json:"file"`
	Messages []struct {
		Index int `json:"index"`
		Grid  *struct {
			Template   struct{ Code int } `json:"template"`
			Points     int                `json:"points"`
			Declared   int                `json:"declaredPoints"`
			FirstPoint *[2]float64        `json:"firstPoint"`
			LastPoint  *[2]float64        `json:"lastPoint"`
		} `json:"grid"`
		DataRep *struct {
			Template  struct{ Code int } `json:"template"`
			NumValues int                `json:"numberOfValues"`
		} `json:"dataRep"`
		Values     []float32 `json:"values"`
		ValueCount int       `json:"valueCount"`
		Error      string    `json:"error"`
	} `json:"messages"`
}

func p6aRunCLI(t *testing.T, bin, fixture string, nValues int) p6aCLIFile {
	t.Helper()
	args := []string{"-json", "-values", strconv.Itoa(nValues), filepath.Join(p6aDir, fixture+".grib2")}
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("gribdump %s: %v\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	var out p6aCLIFile
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("parse gribdump JSON: %v\nstdout head: %.400s", err, stdout.String())
	}
	if len(out.Messages) != 1 {
		t.Fatalf("gribdump messages = %d, want 1", len(out.Messages))
	}
	return out
}

func TestA_P6A_CLIConsistency(t *testing.T) {
	bin := p6aCLIBinary(t)
	const tol = 1e-9

	for _, name := range []string{"p6a_ll_basic", "p6a_ps", "p6a_lcc", "p6a_bitmap", "p6a_ieee"} {
		name := name
		t.Run(name, func(t *testing.T) {
			fl, err := Open(filepath.Join(p6aDir, name+".grib2"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer fl.Close()
			m, err := fl.Message(0)
			if err != nil {
				t.Fatalf("Message(0): %v", err)
			}
			g, err := m.Grid()
			if err != nil {
				t.Fatalf("Grid: %v", err)
			}
			dr, err := m.DataRep()
			if err != nil {
				t.Fatalf("DataRep: %v", err)
			}
			libVals, err := m.Values()
			if err != nil {
				t.Fatalf("Values: %v", err)
			}

			out := p6aRunCLI(t, bin, name, len(libVals))
			mo := out.Messages[0]

			if mo.Grid == nil {
				t.Fatal("CLI grid missing")
			}
			if got := mo.Grid.Template.Code; got != g.TemplateNumber() {
				t.Errorf("CLI grid template = %d, want %d", got, g.TemplateNumber())
			}
			if got := mo.Grid.Points; got != g.NumPoints() {
				t.Errorf("CLI points = %d, want %d (library NumPoints)", got, g.NumPoints())
			}
			if got := mo.Grid.Declared; got != g.NumPointsDeclared() {
				t.Errorf("CLI declaredPoints = %d, want %d", got, g.NumPointsDeclared())
			}

			n := g.NumPoints()
			if n > 0 {
				if mo.Grid.FirstPoint == nil || mo.Grid.LastPoint == nil {
					t.Fatal("CLI firstPoint/lastPoint missing for a supported grid")
				}
				la0, lo0, err := g.Point(0)
				if err != nil {
					t.Fatalf("Point(0): %v", err)
				}
				laN, loN, err := g.Point(n - 1)
				if err != nil {
					t.Fatalf("Point(%d): %v", n-1, err)
				}
				if d := math.Abs(mo.Grid.FirstPoint[0] - la0); d > tol {
					t.Errorf("CLI firstPoint lat = %.10f, want %.10f (library)", mo.Grid.FirstPoint[0], la0)
				}
				if d := p6aLonDiff(mo.Grid.FirstPoint[1], lo0); d > tol {
					t.Errorf("CLI firstPoint lon = %.10f, want %.10f (wrap delta %g)", mo.Grid.FirstPoint[1], lo0, d)
				}
				if d := math.Abs(mo.Grid.LastPoint[0] - laN); d > tol {
					t.Errorf("CLI lastPoint lat = %.10f, want %.10f", mo.Grid.LastPoint[0], laN)
				}
				if d := p6aLonDiff(mo.Grid.LastPoint[1], loN); d > tol {
					t.Errorf("CLI lastPoint lon = %.10f, want %.10f (wrap delta %g)", mo.Grid.LastPoint[1], loN, d)
				}
			}

			if mo.DataRep == nil {
				t.Fatal("CLI dataRep missing")
			}
			if got := mo.DataRep.Template.Code; got != dr.TemplateNumber {
				t.Errorf("CLI dataRep template = %d, want %d", got, dr.TemplateNumber)
			}
			if got := mo.DataRep.NumValues; got != dr.NumValues {
				t.Errorf("CLI numberOfValues = %d, want %d", got, dr.NumValues)
			}

			if len(mo.Values) != len(libVals) {
				t.Fatalf("CLI values len = %d, want %d (library Values)", len(mo.Values), len(libVals))
			}
			for i := range libVals {
				if mo.Values[i] != libVals[i] {
					t.Fatalf("CLI value[%d] = %v, want %v (library float32 exact)", i, mo.Values[i], libVals[i])
				}
			}
			if mo.ValueCount != len(libVals) {
				t.Errorf("CLI valueCount = %d, want %d", mo.ValueCount, len(libVals))
			}
		})
	}

	t.Run("p6a_unimpl_t10_fail_loud", func(t *testing.T) {
		out := p6aRunCLI(t, bin, "p6a_unimpl_t10", 5)
		mo := out.Messages[0]
		if mo.Grid == nil {
			t.Fatal("CLI grid missing (metadata must stay readable)")
		}
		if got := mo.Grid.Template.Code; got != 10 {
			t.Errorf("CLI grid template = %d, want 10", got)
		}
		if got := mo.Grid.Points; got != 0 {
			t.Errorf("CLI points = %d, want 0 for unimplemented template", got)
		}
		if mo.Grid.FirstPoint != nil || mo.Grid.LastPoint != nil {
			t.Error("CLI firstPoint/lastPoint must be absent when Point() fails")
		}
		if !strings.HasPrefix(mo.Error, "values:") {
			t.Errorf("CLI error = %q, want a fail-loud values: diagnostic", mo.Error)
		}
	})
}
