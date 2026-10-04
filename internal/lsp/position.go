package lsp

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/GiGurra/bork/internal/diag"
)

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type sourceRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}
type location struct {
	URI   string      `json:"uri"`
	Range sourceRange `json:"range"`
}
type textEdit struct {
	Range   sourceRange `json:"range"`
	NewText string      `json:"newText"`
}

func filePath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Host != "" && u.Host != "localhost" {
		return "", fmt.Errorf("expected a local file URI")
	}
	path := u.Path
	if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.Abs(filepath.FromSlash(path))
}
func fileURI(path string) string {
	path, _ = filepath.Abs(path)
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func byteOffset(src string, p position) (int, error) {
	if p.Line < 0 || p.Character < 0 {
		return 0, fmt.Errorf("negative source position")
	}
	start := 0
	for line := 0; line < p.Line; line++ {
		i := strings.IndexByte(src[start:], '\n')
		if i < 0 {
			return 0, fmt.Errorf("line outside source")
		}
		start += i + 1
	}
	offset, units := start, 0
	for offset < len(src) && src[offset] != '\n' && src[offset] != '\r' && units < p.Character {
		r, size := utf8.DecodeRuneInString(src[offset:])
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > p.Character {
			return 0, fmt.Errorf("position splits surrogate pair")
		}
		units += width
		offset += size
	}
	if units != p.Character {
		return 0, fmt.Errorf("column outside source")
	}
	return offset, nil
}
func compilerPosition(path, src string, p position) (diag.Pos, error) {
	offset, err := byteOffset(src, p)
	if err != nil {
		return diag.Pos{}, err
	}
	start := strings.LastIndexByte(src[:offset], '\n') + 1
	return diag.Pos{File: path, Line: p.Line + 1, Col: offset - start + 1}, nil
}
func lspPosition(src string, p diag.Pos) position {
	lines := strings.Split(src, "\n")
	line := max(0, min(p.Line-1, len(lines)-1))
	col := max(0, min(p.Col-1, len(lines[line])))
	units := 0
	for _, r := range lines[line][:col] {
		units++
		if r > 0xffff {
			units++
		}
	}
	return position{Line: line, Character: units}
}
func endPosition(src string) position {
	lines := strings.Split(src, "\n")
	return lspPosition(src, diag.Pos{Line: len(lines), Col: len(lines[len(lines)-1]) + 1})
}
