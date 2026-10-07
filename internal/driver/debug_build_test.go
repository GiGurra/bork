package driver

import (
	"bufio"
	"bytes"
	"context"
	"debug/dwarf"
	"debug/elf"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDebugBuild(t *testing.T) {
	t.Parallel()
	root := fixtureDir(t)
	path := filepath.Join(root, "main.bork")
	if err := os.WriteFile(path, []byte("fn main() {\n  x = 3\n  y = x + 4\n  println(y)\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "program")
	if err := BuildDebug(root, exe); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(output) != "7\n" {
		t.Fatalf("%s: %v", output, err)
	}
	source, err := os.ReadFile(exe + ".bork-debug/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(source, []byte("_borkDebugLineMarker")) {
		t.Fatal("debug markers remain")
	}
	if !bytes.Contains(source, []byte("//line "+path+":3")) {
		t.Fatal("source location missing")
	}
	// Verify the executable, rather than only the directive text.
	binary, err := elf.Open(exe)
	if err != nil {
		t.Skip("ELF DWARF verification requires Linux")
	}
	defer func() { _ = binary.Close() }()
	data, err := binary.DWARF()
	if err != nil {
		t.Fatal(err)
	}
	reader := data.Reader()
	found := map[int]bool{}
	for {
		entry, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		if entry == nil {
			break
		}
		if entry.Tag != dwarf.TagCompileUnit {
			continue
		}
		lines, err := data.LineReader(entry)
		if err != nil {
			t.Fatal(err)
		}
		if lines == nil {
			continue
		}
		var line dwarf.LineEntry
		for {
			err := lines.Next(&line)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if line.File != nil && line.File.Name == path {
				found[line.Line] = true
				if line.Line > 5 {
					t.Fatalf("generated Go line leaked into bork source: %d", line.Line)
				}
			}
		}
	}
	for _, line := range []int{1, 2, 3, 4} {
		if !found[line] {
			t.Errorf("DWARF missing bork line %d", line)
		}
	}
}

type dapFixture struct {
	conn   net.Conn
	reader *bufio.Reader
	seq    int
}

func (d *dapFixture) send(t *testing.T, command string, args any) int {
	t.Helper()
	d.seq++
	data, err := json.Marshal(map[string]any{"seq": d.seq, "type": "request", "command": command, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(d.conn, "Content-Length: %d\r\n\r\n%s", len(data), data); err != nil {
		t.Fatal(err)
	}
	return d.seq
}
func (d *dapFixture) until(t *testing.T, event string, seq int) map[string]any {
	t.Helper()
	message := d.untilAny(t, event, seq)
	if seq > 0 && message["success"] != true {
		t.Fatalf("DAP failure: %v", message)
	}
	return message
}
func (d *dapFixture) untilAny(t *testing.T, event string, seq int) map[string]any {
	t.Helper()
	for {
		n := 0
		for {
			line, err := d.reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if line == "\r\n" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				n, err = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(d.reader, data); err != nil {
			t.Fatal(err)
		}
		var message map[string]any
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		if event != "" && message["event"] == event {
			return message
		}
		if seq > 0 && message["request_seq"] == float64(seq) {
			return message
		}
	}
}

// CI installs the pinned debugger and exercises real DAP breakpoints, stepping,
// inspection and disconnect. Ordinary test runs need no optional debugger.
func TestDebugDAPIntegration(t *testing.T) {
	for _, types := range []bool{true, false} {
		t.Run(fmt.Sprintf("types=%t", types), func(t *testing.T) { testDebugDAPSession(t, types) })
	}
}
func testDebugDAPSession(t *testing.T, types bool) {
	dlv := os.Getenv("BORK_TEST_DLV")
	if dlv == "" {
		t.Skip("set BORK_TEST_DLV to run the real debugger integration")
	}
	root := fixtureDir(t)
	path := filepath.Join(root, "main.bork")
	source, err := os.ReadFile("../../testdata/debug/values.bork")
	if err != nil {
		t.Fatal(err)
	}
	projectionBreakpoint := strings.Count(string(source[:bytes.Index(source, []byte("inner: Inner =>"))]), "\n") + 1
	suffixBreakpoint := strings.Count(string(source[:bytes.Index(source, []byte("fn suffixFrame"))]), "\n") + 1
	breakpoint := strings.Count(string(source[:bytes.Index(source, []byte("println(y)"))]), "\n") + 1
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "program")
	if err := BuildDebug(root, exe); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdoutR, stdoutW := io.Pipe()
	defer func() { _ = stdoutR.Close() }()
	done := make(chan error, 1)
	go func() { err := DebugDAP(ctx, dlv, "127.0.0.1:0", stdoutW, stdoutW); _ = stdoutW.Close(); done <- err }()
	var address string
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutR)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "DAP server listening at: ") {
				ready <- strings.TrimPrefix(line, "DAP server listening at: ")
			}
		}
		ready <- ""
	}()
	select {
	case address = <-ready:
	case <-time.After(30 * time.Second):
		t.Fatal("debug adapter startup timed out")
	}
	if address == "" {
		t.Fatal("debug adapter did not listen")
	}
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	d := dapFixture{conn: conn, reader: bufio.NewReader(conn)}
	seq := d.send(t, "initialize", map[string]any{"adapterID": "bork", "linesStartAt1": true, "columnsStartAt1": true, "pathFormat": "path", "supportsVariableType": types})
	d.until(t, "", seq)
	d.send(t, "launch", map[string]any{"mode": "exec", "program": exe, "cwd": root})
	d.until(t, "initialized", 0)
	seq = d.send(t, "setBreakpoints", map[string]any{"source": map[string]any{"path": path}, "breakpoints": []any{map[string]any{"line": breakpoint}, map[string]any{"line": suffixBreakpoint}, map[string]any{"line": projectionBreakpoint}}})
	response := d.until(t, "", seq)
	body := response["body"].(map[string]any)
	bp := body["breakpoints"].([]any)[0].(map[string]any)
	if bp["verified"] != true || bp["line"] != float64(breakpoint) {
		t.Fatalf("breakpoint: %v", bp)
	}
	seq = d.send(t, "configurationDone", map[string]any{})
	d.until(t, "", seq)
	stopped := d.until(t, "stopped", 0)
	thread := stopped["body"].(map[string]any)["threadId"]
	seq = d.send(t, "stackTrace", map[string]any{"threadId": thread})
	response = d.until(t, "", seq)
	frame := response["body"].(map[string]any)["stackFrames"].([]any)[0].(map[string]any)
	if frame["line"] != float64(breakpoint) || frame["source"].(map[string]any)["path"] != path {
		t.Fatalf("stack frame: %v", frame)
	}
	seq = d.send(t, "evaluate", map[string]any{"expression": "y", "frameId": frame["id"], "context": "watch"})
	response = d.until(t, "", seq)
	if response["body"].(map[string]any)["result"] != "7" {
		t.Fatalf("inspect y: %v", response)
	}
	seq = d.send(t, "scopes", map[string]any{"frameId": frame["id"]})
	response = d.until(t, "", seq)
	scopes := response["body"].(map[string]any)["scopes"].([]any)
	var session []debugSnapshot
	for _, scope := range scopes {
		seq = d.send(t, "variables", map[string]any{"variablesReference": scope.(map[string]any)["variablesReference"]})
		response = d.until(t, "", seq)
		name := scope.(map[string]any)["name"].(string)
		session = append(session, debugSnapshot{Request: "scope " + name, Variables: debugVariables(response)})
	}
	for _, expression := range []string{"payload", "shape", "some", "none", "nested", "recordUnion", "scalarUnion", "someFloat", "boxed", "listUnion"} {
		seq = d.send(t, "evaluate", map[string]any{"expression": expression, "frameId": frame["id"], "context": "watch"})
		response = d.until(t, "", seq)
		value := debugValue(response["body"].(map[string]any), "result")
		session = append(session, debugSnapshot{Request: "evaluate " + expression, Value: &value})
		ref := response["body"].(map[string]any)["variablesReference"]
		if ref != float64(0) {
			seq = d.send(t, "variables", map[string]any{"variablesReference": ref})
			response = d.until(t, "", seq)
			session = append(session, debugSnapshot{Request: "expand " + expression, Variables: debugVariables(response)})

			if expression == "listUnion" {
				vars := response["body"].(map[string]any)["variables"].([]any)
				if len(vars) != 1 {
					t.Fatalf("list union lost its paging node: %v", vars)
				}
				child := vars[0].(map[string]any)
				if child["indexedVariables"] != float64(80) {
					t.Fatalf("list union lost element count: %v", child)
				}
				seq = d.send(t, "variables", map[string]any{"variablesReference": child["variablesReference"], "filter": "indexed", "start": 64, "count": 16})
				page := d.until(t, "", seq)
				items := page["body"].(map[string]any)["variables"].([]any)
				if len(items) != 16 || items[0].(map[string]any)["name"] != "[64]" || items[15].(map[string]any)["name"] != "[79]" {
					t.Fatalf("list union paging: %v", items)
				}
			}
			if expression == "nested" {
				for _, item := range response["body"].(map[string]any)["variables"].([]any) {
					child := item.(map[string]any)
					seq = d.send(t, "variables", map[string]any{"variablesReference": child["variablesReference"]})
					expanded := d.until(t, "", seq)
					session = append(session, debugSnapshot{Request: "expand nested." + child["name"].(string), Variables: debugVariables(expanded)})
				}
			}
		}
	}

	for _, test := range []struct{ expression, want string }{
		{"fraction + 0.5", "1.6"}, {"fraction32 + 0.5", "1.6"}, {"boxed.item + 0.5", "2.5"}, {"boxed.item * 1.0", "2.0"}, {"fraction + 1.0 == fraction", "false"},
		{"boundary + 1.0 == boundary", "true"}, {"(boundary + 1.0) + 1.0 == boundary", "true"}, {"boundary - -1.0 == boundary", "true"},
		{"boundary * 1.0000000000000002 == 9007199254740994.0", "true"}, {"boundary / 3.0 == 3002399751580330.5", "true"},
		{"boundary + tiny == boundary", "true"}, {"boundary - tiny == boundary", "true"}, {"boundary32 + tiny32 == boundary32", "true"}, {"boundary32 - tiny32 == boundary32", "true"}, {"boundary32 + 1.0 == boundary32", "true"}, {"(boundary32 + 1.0) + 1.0 == boundary32", "true"}, {"boundary32 - -1.0 == boundary32", "true"},
		{"boundary32 * 1.0000001 == 16777218.0", "true"}, {"boundary32 / 3.0 == 5592405.5", "true"},
		{"tiny * 1.5 == 1e-323", "true"}, {"tiny32 * 1.5 == 2.8e-45", "true"}, {"-fraction < 0.0", "true"},
		{"numbers.get(0)", "Some(3)"}, {"numbers.get(range - 2)", "Some(7)"}, {"numbers.get(-1)", "None"}, {"numbers.get(2)", "None"}, {"numbers.get(9223372036854775807)", "None"}, {"emptyNumbers.get(0)", "None"}, {"fraction32 == 1.1", "true"}, {"fraction32 > 1.1", "false"}, {"fraction == 1.1", "true"}, {"fraction > 1.1", "false"}, {"suffix.range_", "4"}, {"^unsigned == 255", "true"}, {"-signed == signed", "true"}, {"range + chan", "6"}, {"nested.inner.range + y * 2", "19"}, {"boxed.item > 1.5", "true"}, {"!(range > 4) && y == 7", "true"}, {"^range & 7", "4"},
		{"(1 + 2) * 3", "9"}, {"1 / 2", "0"}, {"1.0", "1.0"}, {"'å'", "229"}, {"-range", "-3"}, {`"hé" + "llo"`, `"héllo"`},
	} {
		for _, context := range []string{"repl", "watch", "hover"} {
			seq = d.send(t, "evaluate", map[string]any{"expression": test.expression, "frameId": frame["id"], "context": context})
			response = d.until(t, "", seq)
			if got := response["body"].(map[string]any)["result"]; got != test.want {
				t.Fatalf("%s in %s: %v; want %s", test.expression, context, response, test.want)
			}
			if test.expression == "numbers.get(0)" {
				seq = d.send(t, "variables", map[string]any{"variablesReference": response["body"].(map[string]any)["variablesReference"]})
				payload := d.until(t, "", seq)
				children := debugVariables(payload)
				if len(children) != 1 || children[0].Name != "0" || children[0].Value != "3" {
					t.Fatalf("list get payload expansion: %v", children)
				}
			}
			if types {
				wantType := map[string]string{"1.0": "Float", "'å'": "Rune", "(1 + 2) * 3": "Int", "numbers.get(0)": "Option[Int]", "numbers.get(-1)": "Option[Int]", "fraction + 0.5": "Float", "fraction32 + 0.5": "Float32"}[test.expression]
				if wantType != "" && response["body"].(map[string]any)["type"] != wantType {
					t.Fatalf("literal result type for %s: %v", test.expression, response)
				}
			}
		}
	}
	for _, expression := range []string{"negativeZero + 1.0", "notANumber + 1.0", "infinity + 1.0", "boundary - boundary", "boundary / 0.0", "tiny / 2.0", "tiny32 / 2.0", "huge * 2.0", "huge32 * 2.0", "false && boundary + 1.0 > boundary", "suffix.range", "range_", "missing", "nested.inner.nope", "chan_", "nested.inner.range_", "chan + true", "println(y)", "shapes.get(0)", "shapes[0]", "numbers.get(true)", "floatNumbers.get(0)", "float32Numbers.get(0)", "numbers.get(0).getOr(1)", "nested.inner.label.runeAt(0)", "some.value", "nested == nested", "y = 1", "1; println(y)"} {
		seq = d.send(t, "evaluate", map[string]any{"expression": expression, "frameId": frame["id"], "context": "repl"})
		response = d.untilAny(t, "", seq)
		if response["success"] != false || response["message"] == "" {
			t.Fatalf("accepted unsupported expression %s: %v", expression, response)
		}
	}
	checkDebugGolden(t, session, types)
	seq = d.send(t, "next", map[string]any{"threadId": thread})
	d.until(t, "", seq)
	d.until(t, "stopped", 0)
	seq = d.send(t, "stackTrace", map[string]any{"threadId": thread})
	response = d.until(t, "", seq)
	frame = response["body"].(map[string]any)["stackFrames"].([]any)[0].(map[string]any)
	if frame["line"] != float64(breakpoint+1) {
		t.Fatalf("step landed on: %v", frame)
	}

	seq = d.send(t, "continue", map[string]any{"threadId": thread})
	d.until(t, "", seq)
	d.until(t, "stopped", 0)
	seq = d.send(t, "stackTrace", map[string]any{"threadId": thread})
	response = d.until(t, "", seq)
	suffixFrame := response["body"].(map[string]any)["stackFrames"].([]any)[0].(map[string]any)
	seq = d.send(t, "evaluate", map[string]any{"expression": "range_", "frameId": suffixFrame["id"], "context": "watch"})
	response = d.until(t, "", seq)
	if response["body"].(map[string]any)["result"] != "4" {
		t.Fatalf("source suffix name: %v", response)
	}
	seq = d.send(t, "evaluate", map[string]any{"expression": "range", "frameId": suffixFrame["id"], "context": "watch"})
	if response = d.untilAny(t, "", seq); response["success"] != false {
		t.Fatalf("reserved alias exposed nonexistent source name: %v", response)
	}
	seq = d.send(t, "continue", map[string]any{"threadId": thread})
	d.until(t, "", seq)
	d.until(t, "stopped", 0)
	seq = d.send(t, "stackTrace", map[string]any{"threadId": thread})
	response = d.until(t, "", seq)
	projectionFrame := response["body"].(map[string]any)["stackFrames"].([]any)[0].(map[string]any)
	seq = d.send(t, "evaluate", map[string]any{"expression": "number + inner.range", "frameId": projectionFrame["id"], "context": "watch"})
	response = d.until(t, "", seq)
	if response["body"].(map[string]any)["result"] != "15" {
		t.Fatalf("checked Option and union payload locals: %v", response)
	}
	seq = d.send(t, "disconnect", map[string]any{"terminateDebuggee": true})
	d.until(t, "", seq)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("debug adapter did not exit")
	}
}

func TestDebugBuildRawGo(t *testing.T) {
	t.Parallel()
	root := fixtureDir(t)
	if err := os.WriteFile(filepath.Join(root, "bork.mod"), []byte("module example.com/debugraw\nunsafe \"example.com/debugraw\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := "fn raw(): String unsafe go {\n return `first\n//line dummy:1\n//bork-debug-generated\nsecond`\n}\nfn main(){println(raw())}\n"
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "program")
	if err := BuildDebug(root, exe); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(output) != "first\n//line dummy:1\n//bork-debug-generated\nsecond\n" {
		t.Fatalf("raw string changed: %q (%v)", output, err)
	}
}

// Exclude session-specific references, sequence numbers and addresses from the
// golden while retaining whether expansion is available.
type debugValueSnapshot struct {
	Name       string `json:"name,omitempty"`
	Type       string `json:"type"`
	Value      string `json:"value"`
	Expandable bool   `json:"expandable"`
}
type debugSnapshot struct {
	Request   string               `json:"request"`
	Value     *debugValueSnapshot  `json:"value,omitempty"`
	Variables []debugValueSnapshot `json:"variables,omitempty"`
}

func debugValue(v map[string]any, key string) debugValueSnapshot {
	name, _ := v["name"].(string)
	typ, _ := v["type"].(string)
	value, _ := v[key].(string)
	ref, _ := v["variablesReference"].(float64)
	return debugValueSnapshot{Name: name, Type: typ, Value: value, Expandable: ref > 0}
}
func debugVariables(response map[string]any) []debugValueSnapshot {
	var out []debugValueSnapshot
	for _, v := range response["body"].(map[string]any)["variables"].([]any) {
		out = append(out, debugValue(v.(map[string]any), "value"))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func checkDebugGolden(t *testing.T, session []debugSnapshot, types bool) {
	t.Helper()
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := "../../testdata/debug/session.json"
	if !types {
		path = "../../testdata/debug/session-no-types.json"
	}
	if *update {
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, data) {
		t.Fatalf("debug session differs (-update to regenerate):\n%s", data)
	}
}
