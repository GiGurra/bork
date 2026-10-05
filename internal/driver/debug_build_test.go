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
			if message["success"] != true {
				t.Fatalf("DAP failure: %s", data)
			}
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
	seq = d.send(t, "setBreakpoints", map[string]any{"source": map[string]any{"path": path}, "breakpoints": []any{map[string]any{"line": breakpoint}}})
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
	for _, expression := range []string{"shape", "some", "none", "nested", "recordUnion", "scalarUnion", "someFloat", "boxed"} {
		seq = d.send(t, "evaluate", map[string]any{"expression": expression, "frameId": frame["id"], "context": "watch"})
		response = d.until(t, "", seq)
		value := debugValue(response["body"].(map[string]any), "result")
		session = append(session, debugSnapshot{Request: "evaluate " + expression, Value: &value})
		ref := response["body"].(map[string]any)["variablesReference"]
		if ref != float64(0) {
			seq = d.send(t, "variables", map[string]any{"variablesReference": ref})
			response = d.until(t, "", seq)
			session = append(session, debugSnapshot{Request: "expand " + expression, Variables: debugVariables(response)})
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
