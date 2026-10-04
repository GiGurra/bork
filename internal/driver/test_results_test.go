package driver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStructuredTestResults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := `test "pass" { println("program output"); assert(true) }
test "fail" { assert(false) }
test "other" { assert(false) }
test "rule invalid" { assert(true) }
pred yes(b: Bool) { b }
pred no(b: Bool) { !b }
rule invalid(b: Bool) { yes(b) => no(b) }
test "" { assert(true) }
`
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, err := Test(dir, &out, TestOptions{JSON: true, Filter: "pass"})
	if err != nil || code != 0 {
		t.Fatalf("selected: %d %v %s", code, err, &out)
	}
	var event struct {
		Action, Name, File, Message string
		Line                        int
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event.Action != "pass" || event.Name != "pass" || event.File != path || event.Line != 1 {
		t.Fatalf("result: %+v", event)
	}
	out.Reset()
	code, err = Test(dir, &out, TestOptions{JSON: true, Filter: "fail"})
	if err != nil || code != 1 {
		t.Fatalf("failing: %d %v %s", code, err, &out)
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event.Action != "fail" || !strings.Contains(event.Message, "assertion failed") {
		t.Fatalf("result: %+v", event)
	}
	out.Reset()
	code, err = Test(dir, &out, TestOptions{JSON: true, Filter: "rule invalid"})
	if err != nil || code != 0 {
		t.Fatalf("rule label collision: %d %v %s", code, err, &out)
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event.Action != "pass" || event.Line != 4 {
		t.Fatalf("wrong declaration: %+v", event)
	}

	out.Reset()
	code, err = Test(dir, &out, TestOptions{JSON: true, FilterSet: true})
	if err != nil || code != 0 {
		t.Fatalf("empty name: %d %v %s", code, err, &out)
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event.Name != "" || event.Line != 8 {
		t.Fatalf("wrong empty declaration: %+v", event)
	}

	out.Reset()
	code, err = Test(dir, &out, TestOptions{JSON: true, Filter: "missing"})
	if err != nil || code != 1 || out.Len() != 0 {
		t.Fatalf("unmatched: %d %v %s", code, err, &out)
	}
}
