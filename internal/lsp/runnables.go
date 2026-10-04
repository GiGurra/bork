package lsp

import (
	"path/filepath"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

type runnableTest struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	URI   string      `json:"uri"`
	Range sourceRange `json:"range"`
	Path  string      `json:"path"`
}

func discoverTests(path, src string) []runnableTest {
	file := syntax.Parse(path, []byte(src), &diag.List{})
	return parsedTests(path, src, file)
}

func parsedTests(path, src string, file *syntax.File) []runnableTest {
	out := []runnableTest{}
	if file == nil {
		return out
	}
	for _, test := range file.Tests {
		out = append(out, runnableTest{
			ID: fileURI(path) + "#" + test.Name, Name: test.Name, URI: fileURI(path),
			Range: tokenLocation(test.Pos, src).Range,
			Path:  filepath.Dir(path),
		})
	}
	if file.Script {
		for i := range out {
			out[i].Path = path
		}
	}
	return out
}

func codeLenses(path, src string) []any {
	file := syntax.Parse(path, []byte(src), &diag.List{})
	out := []any{}
	add := func(r sourceRange, title, command string, args ...any) {
		out = append(out, map[string]any{"range": r, "command": map[string]any{
			"title": title, "command": command, "arguments": args,
		}})
	}
	if file == nil {
		return out
	}
	if file.Script {
		add(sourceRange{position{}, position{}}, "▶ run", "bork.run", fileURI(path), "script", path)
	} else {
		for _, fn := range file.Funcs {
			if fn.Name == "main" {
				add(tokenLocation(fn.Pos, src).Range, "▶ run", "bork.run", fileURI(path), "run", filepath.Dir(path))
			}
		}
	}
	for _, test := range parsedTests(path, src, file) {
		add(test.Range, "▶ run test", "bork.runTest", test)
	}
	return out
}
