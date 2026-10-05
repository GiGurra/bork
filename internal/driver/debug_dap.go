package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GiGurra/bork/internal/gen"
)

func debugDAPRelay(ctx context.Context, binary, address string, stdout, stderr io.Writer) error {
	parentCtx := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	cmd := exec.CommandContext(ctx, binary, "dap", "--listen", "127.0.0.1:0")
	configureDebugProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stderr = stderr
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("debug adapter: %w", err)
	}
	exited := make(chan struct{})
	var processErr error
	go func() { processErr = cmd.Wait(); close(exited) }()
	defer func() { cancel(); <-exited }()
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		sent := false
		for scanner.Scan() {
			line := scanner.Text()
			if !sent && strings.HasPrefix(line, "DAP server listening at: ") {
				ready <- strings.TrimPrefix(line, "DAP server listening at: ")
				sent = true
			} else {
				_, _ = fmt.Fprintln(stderr, line)
			}
		}
		if !sent {
			ready <- ""
		}
	}()
	var upstream string
	select {
	case upstream = <-ready:
	case <-exited:
		return fmt.Errorf("debug adapter exited before listening: %v", processErr)
	case <-ctx.Done():
		return nil
	}
	if upstream == "" {
		return fmt.Errorf("debug adapter exited before listening")
	}
	if _, err = fmt.Fprintf(stdout, "DAP server listening at: %s\n", listener.Addr()); err != nil {
		return err
	}
	go func() {
		select {
		case <-exited:
			cancel()
		case <-ctx.Done():
		}
		_ = listener.Close()
	}()
	client, err := listener.Accept()
	if err != nil {
		if parentCtx.Err() != nil {
			return nil
		}
		select {
		case <-exited:
			if processErr != nil {
				return fmt.Errorf("debug adapter: %w", processErr)
			}
			return nil
		default:
			return err
		}
	}
	defer func() { _ = client.Close() }()
	delve, err := net.Dial("tcp", upstream)
	if err != nil {
		return err
	}
	defer func() { _ = delve.Close() }()
	go func() { <-ctx.Done(); _ = client.Close(); _ = delve.Close() }()
	relay := dapRelay{upstream: delve, pending: map[float64]float64{}, references: map[float64]gen.DebugType{}, internal: map[float64]map[string]any{}}
	done := make(chan error, 2)
	go func() { done <- relay.copy(delve, client, true, stderr) }()
	go func() { done <- relay.copy(client, delve, false, stderr) }()
	err = <-done
	canceled := ctx.Err() != nil
	cancel()
	<-done
	if err != nil && err != io.EOF && !canceled {
		return err
	}
	return nil
}

type dapRelay struct {
	mu          sync.Mutex
	metadata    *gen.DebugMap
	writeMu     sync.Mutex
	upstream    io.Writer
	pending     map[float64]float64
	references  map[float64]gen.DebugType
	internal    map[float64]map[string]any
	internalSeq float64
}

func readDAP(reader *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if line == "\r\n" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			length, err = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
			if err != nil {
				return nil, err
			}
		}
	}
	if length < 0 || length > 64<<20 {
		return nil, fmt.Errorf("invalid DAP content length %d", length)
	}
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	return data, err
}

func (r *dapRelay) copy(dst io.Writer, src io.Reader, requests bool, stderr io.Writer) error {
	reader := bufio.NewReader(src)
	for {
		data, err := readDAP(reader)
		if err != nil {
			return err
		}
		var msg map[string]any
		if err = json.Unmarshal(data, &msg); err != nil {
			return err
		}
		r.mu.Lock()
		if requests && msg["command"] == "launch" {
			r.metadata = nil
			if args, ok := msg["arguments"].(map[string]any); ok {
				program, _ := args["program"].(string)
				if !filepath.IsAbs(program) {
					cwd, _ := args["cwd"].(string)
					program = filepath.Join(cwd, program)
				}
				path := program + ".bork-debug/debug-map.json"
				bytes, readErr := os.ReadFile(path)
				var metadata gen.DebugMap
				if readErr == nil {
					if readErr = json.Unmarshal(bytes, &metadata); readErr == nil && metadata.Version == 1 {
						r.metadata = &metadata
					} else {
						_, _ = fmt.Fprintf(stderr, "bork debug: cannot read compatible debug map %s\n", path)
					}
				}
			}
		}
		if requests && msg["command"] == "variables" {
			if args, ok := msg["arguments"].(map[string]any); ok {
				seq, _ := msg["seq"].(float64)
				ref, _ := args["variablesReference"].(float64)
				r.pending[seq] = ref
			}
		}
		if !requests && msg["type"] == "event" && msg["event"] == "continued" {
			clear(r.references)
		}
		changed := false
		if !requests {
			skip, err := r.expand(msg)
			if err != nil {
				r.mu.Unlock()
				return err
			}
			if skip {
				r.mu.Unlock()
				continue
			}
			changed = r.rewrite(msg)
			if msg["type"] == "response" && msg["command"] == "variables" {
				seq, _ := msg["request_seq"].(float64)
				delete(r.pending, seq)
			}
		}
		r.mu.Unlock()
		if changed {
			data, err = json.Marshal(msg)
			if err != nil {
				return err
			}
		}
		if requests {
			r.writeMu.Lock()
		}
		_, err = fmt.Fprintf(dst, "Content-Length: %d\r\n\r\n%s", len(data), data)
		if requests {
			r.writeMu.Unlock()
		}
		if err != nil {
			return err
		}
	}
}

func (r *dapRelay) rewrite(msg map[string]any) bool {
	if r.metadata == nil || msg["type"] != "response" || msg["success"] != true {
		return false
	}
	body, ok := msg["body"].(map[string]any)
	if !ok {
		return false
	}
	switch msg["command"] {
	case "variables":
		vars, ok := body["variables"].([]any)
		if !ok {
			return false
		}
		out := make([]any, 0, len(vars))
		for _, item := range vars {
			v, ok := item.(map[string]any)
			if !ok {
				out = append(out, item)
				continue
			}
			name, _ := v["name"].(string)
			seq, _ := msg["request_seq"].(float64)
			parent := r.references[r.pending[seq]]
			if field, ok := parent.Fields[name]; ok {
				v["name"] = field.Name
			} else if label, ok := r.metadata.Names[name]; ok {
				v["name"] = label
			}
			if r.hidden(name) {
				continue
			}
			r.value(v, "value")
			if f, ok := parent.Fields[name]; ok && (f.Type == "Float" || f.Type == "Float32") {
				if value, ok := v["value"].(string); ok {
					v["value"] = floatPreview(value)
				}
			}
			if typ, ok := r.typeInfo(name); ok {
				v["name"] = typ.Name
			}
			out = append(out, v)
		}
		body["variables"] = out
		seq, _ := msg["request_seq"].(float64)
		delete(r.pending, seq)
	case "evaluate":
		r.value(body, "result")
	case "stackTrace":
		frames, ok := body["stackFrames"].([]any)
		if !ok {
			return false
		}
		for _, item := range frames {
			frame, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, _ := frame["name"].(string)
			if source, ok := frame["source"].(map[string]any); ok && source["path"] == r.metadata.Source {
				frame["presentationHint"] = "subtle"
			}
			if i := strings.IndexByte(name, '('); i >= 0 {
				name = name[:i]
			}
			if i := strings.IndexByte(name, '['); i >= 0 {
				name = name[:i]
			}
			if label, exists := r.metadata.Functions[name]; exists {
				if label == "" {
					frame["presentationHint"] = "subtle"
				} else {
					frame["name"] = label
				}
			}
		}
	case "scopes": // References and standard scope names are already language neutral.
	default:
		return false
	}
	return true
}

func (r *dapRelay) hidden(name string) bool {
	if strings.HasPrefix(name, "(") && strings.HasSuffix(name, ")") {
		name = name[1 : len(name)-1]
	}
	for _, prefix := range r.metadata.HiddenPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func (r *dapRelay) typeInfo(name string) (gen.DebugType, bool) {
	if i := strings.IndexByte(name, '('); i >= 0 {
		name = name[:i]
	}
	if t, ok := r.metadata.Types[strings.ReplaceAll(name, " ", "")]; ok {
		return t, true
	}
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i]
	}
	t, ok := r.metadata.Types[name]
	return t, ok
}

func (r *dapRelay) value(v map[string]any, key string) {
	rawType, _ := v["type"].(string)
	text, _ := v[key].(string)
	if rawType == "" {
		rawType = previewType(text)
	}
	t, known := r.typeInfo(rawType)
	if strings.HasPrefix(rawType, "interface {}(") {
		t = gen.DebugType{Kind: "union"}
		known = true
	}
	if known {
		if ref, ok := v["variablesReference"].(float64); ok && ref > 0 {
			r.references[ref] = t
		}
	}
	if _, ok := v[key].(string); ok {
		v[key] = r.pretty(text)
		if i := strings.IndexByte(rawType, '('); i >= 0 && strings.HasSuffix(rawType, ")") {
			if dynamic, ok := r.typeInfo(rawType[i+1 : len(rawType)-1]); ok {
				// Interface counts describe Delve's synthetic data node, not bork fields.
				delete(v, "namedVariables")
				delete(v, "indexedVariables")
				if dynamic.Kind == "scalar" || (dynamic.Kind == "option" || dynamic.Kind == "variant") && len(dynamic.Fields) == 0 {
					v["variablesReference"] = float64(0)
				}
			}
		}
		if rawType == "float64" || rawType == "float32" {
			v[key] = floatPreview(text)
		}
	}
	if typ, ok := v["type"].(string); ok {
		if i := strings.IndexByte(typ, '('); i >= 0 {
			if typ[:i] == "interface {}" && strings.HasSuffix(typ, ")") {
				typ = typ[i+1 : len(typ)-1]
			} else {
				typ = typ[:i]
			}
		}
		v["type"] = r.prettyType(typ)
	}
}

// Delve exposes an interface's dynamic value as a synthetic data child. Fetch
// its fields only when the client expands the interface, keeping inspection lazy.
func (r *dapRelay) expand(msg map[string]any) (bool, error) {
	if r.metadata == nil || msg["type"] != "response" || msg["command"] != "variables" {
		return false, nil
	}
	seq, _ := msg["request_seq"].(float64)
	if original, ok := r.internal[seq]; ok {
		delete(r.internal, seq)
		originalSeq, _ := original["request_seq"].(float64)
		if msg["success"] != true {
			for k := range msg {
				delete(msg, k)
			}
			for k, v := range original {
				msg[k] = v
			}
		} else {
			msg["request_seq"] = originalSeq
			r.pending[originalSeq] = r.pending[seq]
		}
		delete(r.pending, seq)
		return false, nil
	}
	if msg["success"] != true {
		return false, nil
	}
	if r.references[r.pending[seq]].Kind != "union" {
		return false, nil
	}
	body, ok := msg["body"].(map[string]any)
	if !ok {
		return false, nil
	}
	vars, ok := body["variables"].([]any)
	if !ok || len(vars) != 1 {
		return false, nil
	}
	child, ok := vars[0].(map[string]any)
	if !ok || child["name"] != "data" {
		return false, nil
	}
	rawType, _ := child["type"].(string)
	if rawType == "" {
		text, _ := child["value"].(string)
		rawType = previewType(text)
	}
	typ, ok := r.typeInfo(rawType)
	if !ok {
		return false, nil
	}
	ref, _ := child["variablesReference"].(float64)
	if ref == 0 {
		body["variables"] = []any{}
		return false, nil
	}
	if r.upstream == nil {
		return false, nil
	}
	r.references[ref] = typ
	r.internalSeq--
	request := map[string]any{"seq": r.internalSeq, "type": "request", "command": "variables", "arguments": map[string]any{"variablesReference": ref}}
	data, err := json.Marshal(request)
	if err != nil {
		return false, err
	}
	r.internal[r.internalSeq] = msg
	r.pending[r.internalSeq] = ref
	r.writeMu.Lock()
	_, err = fmt.Fprintf(r.upstream, "Content-Length: %d\r\n\r\n%s", len(data), data)
	r.writeMu.Unlock()
	return true, err
}
