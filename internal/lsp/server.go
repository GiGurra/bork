package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
	"github.com/GiGurra/bork/internal/modcache"
)

type document struct {
	text    string
	version int
}
type packageState struct {
	session  *driver.Session
	analysis *driver.EditorAnalysis
	stale    bool
}
type server struct {
	out                   io.Writer
	version               string
	docs                  map[string]document
	packages              map[string]*packageState
	diagnostics           map[string][]diag.Diagnostic
	initialized, shutdown bool
}
type documentParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Text    string `json:"text"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	Position       position `json:"position"`
	ContentChanges []struct {
		Text  string       `json:"text"`
		Range *sourceRange `json:"range"`
	} `json:"contentChanges"`
	NewName string `json:"newName"`
	Context struct {
		IncludeDeclaration bool     `json:"includeDeclaration"`
		Only               []string `json:"only"`
	} `json:"context"`
	Range sourceRange `json:"range"`
}

// Serve runs a serialized compiler behind a responsive framing reader. Changes
// are coalesced; a semantic request immediately checks the latest buffers.
func Serve(in io.Reader, out io.Writer) error {
	return ServeWithVersion(in, out, "")
}

// ServeWithVersion includes the running compiler version in LSP initialization.
func ServeWithVersion(in io.Reader, out io.Writer, version string) error {
	s := &server{out: out, version: version, docs: map[string]document{}, packages: map[string]*packageState{}, diagnostics: map[string][]diag.Diagnostic{}}
	type incoming struct {
		m   message
		err error
	}
	messages := make(chan incoming, 16)
	done := make(chan struct{})
	defer close(done)
	go func() {
		r := bufio.NewReader(in)
		for {
			m, err := readMessage(r)
			select {
			case messages <- incoming{m, err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	var timer *time.Timer
	var tick <-chan time.Time
	dirty := false
	flush := func() error {
		if !dirty {
			return nil
		}
		dirty = false
		return s.check()
	}
	for {
		select {
		case <-tick:
			tick = nil
			if err := flush(); err != nil {
				return err
			}
		case item := <-messages:
			if item.err != nil {
				if errors.Is(item.err, io.EOF) {
					return nil
				}
				return item.err
			}
			m := item.m
			if m.Method == "exit" {
				if s.shutdown {
					return nil
				}
				return fmt.Errorf("client exited without shutdown")
			}
			if len(m.ID) > 0 && m.Method != "initialize" && m.Method != "shutdown" && m.Method != "textDocument/codeLens" && m.Method != "bork/tests" {
				if err := flush(); err != nil {
					return err
				}
			}
			result, rpcErr, changed := s.handle(m)
			if len(m.ID) > 0 {
				response := map[string]any{"jsonrpc": "2.0", "id": m.ID}
				if rpcErr != nil {
					response["error"] = rpcErr
				} else {
					response["result"] = result
				}
				if err := writeMessage(out, response); err != nil {
					return err
				}
			} else if rpcErr != nil {
				if err := s.notify("window/logMessage", map[string]any{"type": 2, "message": rpcErr.Message}); err != nil {
					return err
				}
			}
			if changed {
				dirty = true
				if m.Method == "textDocument/didChange" {
					if timer != nil {
						timer.Stop()
					}
					timer = time.NewTimer(150 * time.Millisecond)
					tick = timer.C
				} else {
					if err := flush(); err != nil {
						return err
					}
				}
			}
		}
	}
}
func (s *server) notify(method string, params any) error {
	return writeMessage(s.out, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
func (s *server) handle(m message) (any, *rpcError, bool) {
	if m.Method == "initialize" {
		if s.initialized {
			return nil, &rpcError{-32600, "already initialized"}, false
		}
		s.initialized = true
		return map[string]any{"capabilities": map[string]any{
			"positionEncoding": "utf-16", "textDocumentSync": map[string]any{"openClose": true, "change": 1, "save": map[string]any{"includeText": false}},
			"hoverProvider": true, "definitionProvider": true, "documentFormattingProvider": true,
			"referencesProvider": true, "renameProvider": map[string]any{"prepareProvider": true},
			"documentSymbolProvider": true, "completionProvider": map[string]any{"triggerCharacters": []string{"."}},
			"codeLensProvider":   map[string]any{"resolveProvider": false},
			"codeActionProvider": map[string]any{"codeActionKinds": []string{"quickfix"}},
		}, "serverInfo": map[string]any{"name": "bork", "version": s.version}}, nil, false
	}
	if !s.initialized {
		return nil, &rpcError{-32002, "server not initialized"}, false
	}
	if m.Method == "shutdown" {
		s.shutdown = true
		return nil, nil, false
	}
	if s.shutdown {
		return nil, &rpcError{-32600, "server has shut down"}, false
	}
	switch m.Method {
	case "initialized", "$/cancelRequest", "$/setTrace":
		return nil, nil, false
	case "workspace/didChangeWatchedFiles":
		return nil, nil, true
	}
	switch m.Method {
	case "textDocument/didOpen", "textDocument/didChange", "textDocument/didSave", "textDocument/didClose",
		"textDocument/hover", "textDocument/definition", "textDocument/completion", "textDocument/references",
		"textDocument/rename", "textDocument/prepareRename", "textDocument/formatting", "textDocument/documentSymbol", "textDocument/codeAction", "textDocument/codeLens", "bork/tests":
	default:
		return nil, &rpcError{-32601, "method not found"}, false
	}
	var p documentParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return nil, &rpcError{-32602, "invalid parameters"}, false
	}
	path, err := filePath(p.TextDocument.URI)
	if err != nil {
		return nil, &rpcError{-32602, err.Error()}, false
	}
	switch m.Method {
	case "textDocument/didOpen":
		s.docs[path] = document{p.TextDocument.Text, p.TextDocument.Version}
		return nil, nil, true
	case "textDocument/didChange":
		old, ok := s.docs[path]
		if !ok || p.TextDocument.Version <= old.version {
			return nil, nil, false
		}
		if len(p.ContentChanges) == 0 {
			return nil, nil, false
		}
		for _, change := range p.ContentChanges {
			if change.Range != nil {
				return nil, &rpcError{-32602, "server requires full-document changes"}, false
			}
			old.text = change.Text
		}
		old.version = p.TextDocument.Version
		s.docs[path] = old
		return nil, nil, true
	case "textDocument/didSave":
		return nil, nil, true
	case "textDocument/didClose":
		delete(s.docs, path)
		return nil, nil, true
	}
	result, err := s.feature(m.Method, path, p)
	if err != nil {
		return nil, &rpcError{-32602, err.Error()}, false
	}
	return result, nil, false
}
func (s *server) source(path string) string {
	if d, ok := s.docs[path]; ok {
		return d.text
	}
	src, _ := os.ReadFile(path)
	return string(src)
}
func (s *server) state(path string) *packageState {
	if pkg := s.packages[analysisPath(path, s.source(path))]; pkg != nil && pkg.analysis != nil {
		return pkg
	}
	for _, dir := range sortedKeys(s.packages) {
		pkg := s.packages[dir]
		if pkg.analysis != nil {
			if _, ok := pkg.analysis.Sources()[path]; ok {
				return pkg
			}
		}
	}
	return nil
}

func (s *server) check() error {
	overlays := map[string]string{}
	dirs := map[string]bool{}
	cache := modcache.Root()
	for path, doc := range s.docs {
		if modcache.Contains(cache, path) {
			continue
		}
		overlays[path] = doc.text
		dirs[analysisPath(path, doc.text)] = true
	}
	next := map[string][]diag.Diagnostic{}
	// Check every open package, including reverse users of an edited import.
	for _, dir := range sortedKeys(dirs) {
		pkg := s.packages[dir]
		if pkg == nil {
			pkg = &packageState{session: driver.NewSession()}
			s.packages[dir] = pkg
		}
		analysis, err := pkg.session.Analyze(dir, overlays)
		pkg.stale = err != nil
		var ds []diag.Diagnostic
		if err == nil {
			pkg.analysis = analysis
			ds = analysis.Warnings()
		} else {
			var de *driver.DiagError
			if errors.As(err, &de) {
				ds = de.Diags.Sorted()
			} else {
				for path := range s.docs {
					if analysisPath(path, s.source(path)) == dir {
						ds = append(ds, diag.Diagnostic{Pos: diag.Pos{File: path, Line: 1, Col: 1}, Msg: err.Error(), Code: "lsp.check"})
						break
					}
				}
			}
		}
		for _, d := range ds {
			path, err := filepath.Abs(d.Pos.File)
			if err != nil || d.Pos.File == "" {
				continue
			}
			if _, ok := overlays[path]; !ok {
				if _, err := os.Stat(path); err != nil {
					continue
				}
			}
			if !slices.ContainsFunc(next[path], func(old diag.Diagnostic) bool { return old.Pos == d.Pos && old.Code == d.Code && old.Msg == d.Msg }) {
				next[path] = append(next[path], d)
			}
		}
	}
	for dir := range s.packages {
		if !dirs[dir] {
			delete(s.packages, dir)
		}
	}
	paths := map[string]bool{}
	for path := range s.docs {
		paths[path] = true
	}
	for path := range s.diagnostics {
		paths[path] = true
	}
	for path := range next {
		paths[path] = true
	}
	s.diagnostics = next
	for _, path := range sortedKeys(paths) {
		ds := make([]any, 0, len(next[path]))
		src := s.source(path)
		for _, d := range next[path] {
			end := d.End
			if end.File == "" {
				end = d.Pos
			}
			r := sourceRange{lspPosition(src, d.Pos), lspPosition(src, end)}
			severity := 1
			if d.Severity == "warning" {
				severity = 2
			}
			ds = append(ds, map[string]any{"range": r, "severity": severity, "code": d.Code, "source": "bork", "message": d.Msg})
		}
		params := map[string]any{"uri": fileURI(path), "diagnostics": ds}
		if doc, ok := s.docs[path]; ok {
			params["version"] = doc.version
		}
		if err := s.notify("textDocument/publishDiagnostics", params); err != nil {
			return err
		}
	}
	return nil
}
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func analysisPath(path, src string) string {
	if strings.HasPrefix(src, "#!") {
		return path
	}
	return filepath.Dir(path)
}
