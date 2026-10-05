package driver

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Ask the selected C frontend for its project header closure on a cold build.
// System header paths are explicitly outside the fast contract.
// This only preprocesses inputs; it neither compiles nor links a second object.
func (inventory *buildInventory) captureCInputs(pkg buildPackage, ctx *goContext) bool {
	command := ctx.values["CC"]
	if command == "" {
		command = "cc"
	}
	parts, ok := splitBuildWords(command)
	if !ok || len(parts) == 0 {
		return false
	}
	compiler, err := exec.LookPath(parts[0])
	if err != nil {
		return false
	}
	absolute, err := filepath.Abs(compiler)
	if err != nil || !inventory.captureFile(absolute) {
		return false
	}
	var configFlags []string
	if len(pkg.CgoPkgConfig) > 0 {
		config := ctx.values["PKG_CONFIG"]
		if config == "" {
			config = ctx.processValue("PKG_CONFIG")
		}
		if config == "" {
			config = "pkg-config"
		}
		configParts, ok := splitBuildWords(config)
		if !ok || len(configParts) == 0 {
			return false
		}
		resolved, err := exec.LookPath(configParts[0])
		if err != nil {
			return false
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil || !inventory.captureFile(absolute) {
			return false
		}
		args := append(append([]string(nil), configParts[1:]...), "--cflags", "--")
		args = append(args, pkg.CgoPkgConfig...)
		cmd := exec.Command(configParts[0], args...)
		cmd.Dir = pkg.Dir
		cmd.Env = ctx.env
		output, err := cmd.Output()
		if err != nil || len(output) > buildReceiptLimit {
			return false
		}
		configFlags, ok = splitBuildWords(string(output))
		if !ok {
			return false
		}
	}
	implicitFlags, ok := splitBuildWords(ctx.values["GOGCCFLAGS"])
	if !ok {
		return false
	}
	flags := append(append([]string(nil), implicitFlags...), pkg.CgoCPPFLAGS...)
	flags = append(flags, pkg.CgoCFLAGS...)
	flags = append(flags, configFlags...)
	for _, key := range []string{"CGO_CPPFLAGS", "CGO_CFLAGS"} {
		extra, ok := splitBuildWords(ctx.values[key])
		if !ok {
			return false
		}
		flags = append(flags, extra...)
	}
	preprocess := func(source []byte, path string, language string) bool {
		cpp := language == "c++"
		invocation := parts
		selectedFlags := flags
		if cpp {
			cxx := ctx.values["CXX"]
			if cxx == "" {
				cxx = "c++"
			}
			var ok bool
			invocation, ok = splitBuildWords(cxx)
			if !ok || len(invocation) == 0 {
				return false
			}
			resolved, err := exec.LookPath(invocation[0])
			if err != nil {
				return false
			}
			absolute, err := filepath.Abs(resolved)
			if err != nil || !inventory.captureFile(absolute) {
				return false
			}
			selectedFlags = append(append(append([]string(nil), implicitFlags...), pkg.CgoCPPFLAGS...), pkg.CgoCXXFLAGS...)
			selectedFlags = append(selectedFlags, configFlags...)
			for _, key := range []string{"CGO_CPPFLAGS", "CGO_CXXFLAGS"} {
				extra, ok := splitBuildWords(ctx.values[key])
				if !ok {
					return false
				}
				selectedFlags = append(selectedFlags, extra...)
			}
		}

		args := append(append([]string(nil), invocation[1:]...), selectedFlags...)
		if !inventory.captureIncludeSearch(selectedFlags, ctx, pkg.Dir) {
			return false
		}
		args = append(args, "-M", "-MT", "bork-inputs", "-I", pkg.Dir, "-x", language)
		if path != "" {
			args = append(args, path)
		} else {
			args = append(args, "-")
		}
		cmd := exec.Command(invocation[0], args...)
		cmd.Dir = pkg.Dir
		cmd.Env = ctx.env
		cmd.Stdin = bytes.NewReader(source)
		output, err := cmd.Output()
		if err != nil || len(output) > buildReceiptLimit {
			return false
		}
		if len(bytes.TrimSpace(output)) == 0 {
			return true
		}
		index := bytes.IndexByte(output, ':')
		if index < 0 {
			return false
		}
		dependencies, ok := splitMakeDependencies(string(output[index+1:]))
		if !ok {
			return false
		}
		for _, name := range dependencies {
			if name == "-" || name == "<stdin>" {
				continue
			}
			path := name
			if !filepath.IsAbs(path) {
				path = filepath.Join(pkg.Dir, path)
			}
			if withinBuildDirectory(ctx.values["GOROOT"], path) || systemBuildHeader(path) {
				continue
			}
			if !inventory.captureFile(path) {
				return false
			}
			identity, ok := buildDirectoryStat(filepath.Dir(path))
			if !ok {
				return false
			}
			inventory.Directories[filepath.Dir(path)] = identity
		}
		return true
	}
	for _, name := range pkg.CgoFiles {
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkg.Dir, name), nil, parser.ParseComments)
		if err != nil {
			return false
		}
		for _, decl := range parsed.Decls {
			general, ok := decl.(*ast.GenDecl)
			if !ok || general.Tok != token.IMPORT {
				continue
			}
			for _, spec := range general.Specs {
				imp, ok := spec.(*ast.ImportSpec)
				if !ok || imp.Path.Value != `"C"` {
					continue
				}
				doc := imp.Doc
				if doc == nil {
					doc = general.Doc
				}
				if doc == nil {
					continue
				}
				var preamble strings.Builder
				for _, line := range strings.Split(doc.Text(), "\n") {
					if strings.HasPrefix(strings.TrimSpace(line), "#cgo ") {
						continue
					}
					preamble.WriteString(line)
					preamble.WriteByte('\n')
				}
				if !preprocess([]byte(preamble.String()), "", "c") {
					return false
				}
			}
		}
	}
	for _, group := range []struct {
		files    []string
		language string
	}{{pkg.CFiles, "c"}, {pkg.CXXFiles, "c++"}, {pkg.MFiles, "objective-c"}} {
		for _, name := range group.files {
			data, err := os.ReadFile(filepath.Join(pkg.Dir, name))
			if err != nil {
				return false
			}
			// This header is generated from the recorded cgo Go declarations.
			lines := strings.Split(string(data), "\n")
			for i, line := range lines {
				if strings.Contains(line, `#include "_cgo_export.h"`) {
					lines[i] = ""
				}
			}
			if !preprocess([]byte(strings.Join(lines, "\n")), "", group.language) {
				return false
			}
		}
	}
	return true
}

func withinBuildDirectory(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return root != "" && err == nil && (relative == "." || filepath.IsLocal(relative))
}

// Command flags use whitespace with optional quotes, as Go's cgo settings do.
func splitBuildWords(input string) ([]string, bool) {
	var result []string
	var word strings.Builder
	quote := byte(0)
	active := false
	for i := 0; i < len(input); i++ {
		char := input[i]
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				word.WriteByte(char)
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			active = true
			continue
		}
		if char == ' ' || char == '\t' || char == '\r' || char == '\n' {
			if active {
				result = append(result, word.String())
				word.Reset()
				active = false
			}
			continue
		}
		word.WriteByte(char)
		active = true
	}
	if quote != 0 {
		return nil, false
	}
	if active {
		result = append(result, word.String())
	}
	return result, true
}

func splitMakeDependencies(input string) ([]string, bool) {
	var result []string
	var word strings.Builder
	active := false
	for i := 0; i < len(input); i++ {
		char := input[i]
		if char == '\\' {
			i++
			if i >= len(input) {
				return nil, false
			}
			if input[i] == '\n' {
				continue
			}
			word.WriteByte(input[i])
			active = true
			continue
		}
		if char == ' ' || char == '\t' || char == '\r' || char == '\n' {
			if active {
				result = append(result, word.String())
				word.Reset()
				active = false
			}
			continue
		}
		word.WriteByte(char)
		active = true
	}
	if active {
		result = append(result, word.String())
	}
	return result, true
}

func systemBuildHeader(path string) bool {
	for _, root := range []string{"/usr/include", "/usr/local/include", "/usr/lib/gcc", "/Library/Developer", "/Applications/Xcode.app/Contents/Developer", "/System/Library"} {
		if withinBuildDirectory(root, path) {
			return true
		}
	}
	return strings.HasPrefix(path, "/usr/lib/") && strings.Contains(path, "/lib/clang/")
}

func (inventory *buildInventory) captureIncludeSearch(flags []string, ctx *goContext, dir string) bool {
	var roots []string
	for i := 0; i < len(flags); i++ {
		flag := flags[i]
		switch {
		case flag == "-I" || flag == "-iquote" || flag == "-isystem" || flag == "-idirafter" || flag == "-iframework" || flag == "-F":
			i++
			if i >= len(flags) {
				return false
			}
			roots = append(roots, flags[i])
		case strings.HasPrefix(flag, "-I") && len(flag) > 2:
			roots = append(roots, flag[2:])
		default:
			for _, prefix := range []string{"-iquote", "-isystem", "-idirafter", "-iframework", "-F"} {
				if strings.HasPrefix(flag, prefix) && len(flag) > len(prefix) {
					roots = append(roots, flag[len(prefix):])
					break
				}
			}

		}
	}
	for _, key := range []string{"CPATH", "C_INCLUDE_PATH", "CPLUS_INCLUDE_PATH", "OBJC_INCLUDE_PATH"} {
		if value := ctx.processValue(key); value != "" {
			roots = append(roots, filepath.SplitList(value)...)
		}
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			root = filepath.Join(dir, root)
		}
		if systemBuildHeader(root) || withinBuildDirectory(ctx.values["GOROOT"], root) {
			continue
		}
		if _, err := os.Stat(root); os.IsNotExist(err) {
			inventory.Missing = append(inventory.Missing, root)
			continue
		} else if err != nil {
			return false
		}
		if !inventory.captureIncludeDirectories(root) {
			return false
		}
	}
	return len(inventory.Missing) <= goStageInventoryLimit
}
