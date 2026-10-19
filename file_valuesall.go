// Concurrent decoding helper for File. io.ReaderAt is safe to use from
// multiple goroutines, so large multi-message files (e.g. pangu-style
// post-processing) can decode in parallel when the caller opts in.

package grib

import (
	"fmt"
	"sync"
)

// ValuesAll decodes every message in the file and returns one []float32 per
// message in file order. The worker count is optional:
//
//   - f.ValuesAll() — sequential decoding (default, workers=1, no goroutines)
//   - f.ValuesAll(n), n>1 — decode with at most n concurrent goroutines
//
// Semantics: n <= 1 selects the sequential default; n greater than the
// message count is clamped to it; more than one argument is a programming
// error and returns an error (fail-loud). For automatic parallelism pass
// runtime.GOMAXPROCS(0) explicitly — there is no implicit magic.
//
// Concurrency safety: io.ReaderAt is required to be goroutine-safe; each
// worker builds its own Message from a freshly read buffer, and a Message is
// immutable after construction. Do not call pack.Register concurrently while
// ValuesAll is running (the unpacker registry is shared global state).
//
// Memory: all decoded results are held simultaneously (e.g. a pangu file with
// 69 messages / 71.6M values is about 287 MB of float32). For a streaming
// footprint use Messages() + Values() instead.
//
// Errors: the error of the first failing message in file order is returned
// (the same error a sequential walk would hit at the same position); other
// messages are not reported individually. Bit-exactness with the sequential
// path is pinned by TestA_P8A_ParallelValuesBitExact.
func (f *File) ValuesAll(workers ...int) ([][]float32, error) {
	w := 1 // default: sequential
	switch len(workers) {
	case 0:
		// No argument: sequential default.
	case 1:
		if workers[0] > w {
			w = workers[0]
		}
	default:
		return nil, fmt.Errorf("grib: ValuesAll takes at most one worker-count argument, got %d", len(workers))
	}

	n := len(f.msgs)
	if n == 0 {
		return nil, nil
	}
	if w > n {
		w = n
	}
	results := make([][]float32, n)
	errs := make([]error, n)

	if w == 1 {
		for i := 0; i < n; i++ {
			results[i], errs[i] = decodeOne(f, i)
		}
		return collect(results, errs)
	}

	sem := make(chan struct{}, w)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i], errs[i] = decodeOne(f, i)
		}(i)
	}
	wg.Wait()
	return collect(results, errs)
}

// decodeOne decodes message i through the regular Message(i)+Values() path so
// workers and the sequential walk exercise identical code.
func decodeOne(f *File, i int) ([]float32, error) {
	m, err := f.Message(i)
	if err != nil {
		return nil, err
	}
	return m.Values()
}

// collect returns the decoded values, or the first per-message error in file
// order. The error is returned unwrapped so it is byte-identical to what a
// sequential Messages()+Values() walk would produce at the same position
// (fail-loud carries no extra context here).
func collect(results [][]float32, errs []error) ([][]float32, error) {
	for i := range errs {
		if errs[i] != nil {
			return nil, errs[i]
		}
	}
	return results, nil
}
