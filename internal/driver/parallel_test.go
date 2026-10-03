package driver

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestParallelTests runs the tests_parallel case with --parallel: the
// report is the one it gives run one test at a time.
func TestParallelTests(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "cases", "tests_parallel")
	want, err := os.ReadFile(filepath.Join(dir, "expected_test_output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	code, err := Test(dir, &out, TestOptions{Parallel: 4})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(out.String(), dir+string(filepath.Separator), "")
	if got+"exit code 1\n" != string(want) || code != 1 {
		t.Errorf("got (exit code %d):\n%s\nwant:\n%s", code, got, want)
	}
}

// TestParallelTestsOverlap checks that --parallel N runs N tests at
// once: each waits until all three have started. It also checks that
// assertSnapshot, called where no test can be told (Go code dropped
// the labels), says so.
func TestParallelTestsOverlap(t *testing.T) {
	dir := t.TempDir()
	// The tests meet in a directory: each adds a file to it.
	meet := t.TempDir()
	files := map[string]string{
		"bork.mod": "module example.com/overlap\nunsafe \"example.com/overlap\"\n",
		"main.bork": `fn arrive(dir: String) uses io + clock: Bool unsafe go {
  import "os"
  import "time"
  f, err := os.CreateTemp(dir, "arrived-*")
  if err != nil {
    panic(err)
  }
  f.Close()
  deadline := time.Now().Add(10 * time.Second)
  for {
    entries, _ := os.ReadDir(dir)
    if len(entries) >= 3 {
      return true
    }
    if time.Now().After(deadline) {
      return false
    }
    time.Sleep(time.Millisecond)
  }
}

fn unlabelled(f: () uses io => Unit) uses io + state unsafe go {
  import "context"
  import "runtime/pprof"
  done := make(chan any)
  go func() {
    defer func() { done <- recover() }()
    pprof.SetGoroutineLabels(context.Background())
    f()
  }()
  if r := <-done; r != nil {
    panic(r)
  }
}

fn main() { println(arrive("")) }

test "one" { assert(arrive(MEET)) }
test "two" { assert(arrive(MEET)) }
test "three" { assert(arrive(MEET)) }
test "no test here" { unlabelled(() => assertSnapshot(1)) }
`,
	}
	files["main.bork"] = strings.ReplaceAll(files["main.bork"], "MEET", strconv.Quote(meet))
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out strings.Builder
	code, err := Test(dir, &out, TestOptions{Parallel: 3})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(out.String(), dir+string(filepath.Separator), "")
	want := "ok    one\nok    two\nok    three\nFAIL  no test here\n      main.bork:41:40: assertSnapshot cannot tell which test is running here: with --parallel, call it from the test, or a task the test started\n3 passed, 1 failed\n"
	if got != want || code != 1 {
		t.Errorf("got (exit code %d):\n%s\nwant:\n%s", code, got, want)
	}
}
