package syntax

import (
	"runtime"
	"sync"

	"github.com/GiGurra/bork/internal/diag"
)

// ParseFiles parses independent sources concurrently when there is enough work
// to amortize worker setup. Files and diagnostics retain the input order.
func ParseFiles(paths []string, srcs [][]byte, embedded bool, diags *diag.List) []*File {
	parseOne := Parse
	if embedded {
		parseOne = ParseEmbedded
	}
	sourceBytes := 0
	for _, src := range srcs {
		sourceBytes += len(src)
	}
	workers := min(runtime.GOMAXPROCS(0), len(paths), 8)
	if len(paths) < 4 || sourceBytes < 32*1024 {
		workers = 1
	}
	return parseFiles(paths, srcs, diags, parseOne, workers)
}

func parseFiles(paths []string, srcs [][]byte, diags *diag.List, parseOne func(string, []byte, *diag.List) *File, workers int) []*File {
	files := make([]*File, len(paths))
	if workers <= 1 {
		for i, path := range paths {
			files[i] = parseOne(path, srcs[i], diags)
		}
		return files
	}
	diagnostics := make([]*diag.List, len(paths))
	jobs := make(chan int, len(paths))
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			for i := range jobs {
				local := &diag.List{}
				files[i] = parseOne(paths[i], srcs[i], local)
				diagnostics[i] = local
			}
		})
	}
	group.Wait()
	for _, local := range diagnostics {
		diags.Append(local)
	}
	return files
}
