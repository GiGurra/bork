package driver

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDebugListMissingVariants(t *testing.T) {
	dlv := os.Getenv("BORK_TEST_DLV")
	if dlv == "" {
		t.Skip("set BORK_TEST_DLV to run the real debugger integration")
	}
	for _, test := range []struct{ name, variant, expression string }{
		{"only Some", ".Some(1)", "xs.get(-1)"},
		{"only None", ".None", "xs.get(0)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := fixtureDir(t)
			path := filepath.Join(root, "main.bork")
			source := "fn main() {\n xs = [1]\n value: Option[Int] = " + test.variant + "\n println(xs, value)\n}\n"
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
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
			go func() { _ = DebugDAP(ctx, dlv, "127.0.0.1:0", stdoutW, stdoutW); _ = stdoutW.Close() }()
			ready := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(stdoutR)
				for scanner.Scan() {
					if line := scanner.Text(); strings.HasPrefix(line, "DAP server listening at: ") {
						ready <- strings.TrimPrefix(line, "DAP server listening at: ")
						return
					}
				}
				ready <- ""
			}()
			var address string
			select {
			case address = <-ready:
			case <-time.After(30 * time.Second):
				t.Fatal("debugger startup timed out")
			}
			if address == "" {
				t.Fatal("debugger did not listen")
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
			seq := d.send(t, "initialize", map[string]any{"adapterID": "bork", "supportsVariableType": true, "pathFormat": "path", "linesStartAt1": true, "columnsStartAt1": true})
			d.until(t, "", seq)
			d.send(t, "launch", map[string]any{"mode": "exec", "program": exe, "cwd": root})
			d.until(t, "initialized", 0)
			seq = d.send(t, "setBreakpoints", map[string]any{"source": map[string]any{"path": path}, "breakpoints": []any{map[string]any{"line": 4}}})
			d.until(t, "", seq)
			seq = d.send(t, "configurationDone", map[string]any{})
			d.until(t, "", seq)
			stopped := d.until(t, "stopped", 0)
			seq = d.send(t, "stackTrace", map[string]any{"threadId": stopped["body"].(map[string]any)["threadId"]})
			response := d.until(t, "", seq)
			frame := response["body"].(map[string]any)["stackFrames"].([]any)[0].(map[string]any)
			seq = d.send(t, "evaluate", map[string]any{"expression": test.expression, "frameId": frame["id"], "context": "watch"})
			response = d.untilAny(t, "", seq)
			message, _ := response["message"].(string)
			if response["success"] != false || !strings.Contains(message, "concrete variant type") || !strings.Contains(message, "inspect the list's children") {
				t.Fatalf("missing variant guidance: %v", response)
			}
			seq = d.send(t, "disconnect", map[string]any{"terminateDebuggee": true})
			d.until(t, "", seq)
		})
	}
}
