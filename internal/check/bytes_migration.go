package check

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

var migrationIdentifiers = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// removedBytesCall only handles unresolved names; local and declared functions
// with these spellings keep their ordinary meaning.
func (c *checker) removedBytesCall(call *syntax.Call, id *syntax.Ident) bool {
	target := ""
	switch id.Name {
	case "bytes":
		target = "toBytes"
	case "utf8Bytes":
		target = "Utf8"
	case "utf8String":
		target = "ParseUtf8"
	default:
		return false
	}
	code := "migration." + id.Name
	replacement := "bork/encoding." + target
	if id.Name == "bytes" {
		replacement = "List[Byte].toBytes()"
	}
	c.diags.AddCode(id.Pos, code, "%s was removed; use %s", id.Name, replacement)
	var file *syntax.File
	for _, f := range c.files {
		if f.Path == id.Pos.File {
			file = f
			break
		}
	}
	if file == nil || len(call.Args) != 1 || len(call.TypeArgs) != 0 {
		return true
	}
	var edits []diag.TextEdit
	if id.Name == "bytes" {
		if call.Pipe.File != "" {
			edits = []diag.TextEdit{
				{Start: call.PipeStart, End: call.PipeStart, Replacement: "("},
				{Start: call.PipeEnd, End: call.PipeTargetEnd, Replacement: ").toBytes()"},
			}
		} else {
			arg := call.Arguments[0]
			edits = []diag.TextEdit{
				{Start: call.Start, End: arg.ValueStart, Replacement: "("},
				{Start: arg.End, End: call.End, Replacement: ").toBytes()"},
			}
		}
	} else {
		alias := "encoding"
		imported := false
		for name, pkg := range c.pkg.imports {
			if pkg.Path == "bork/encoding" && c.lookup(name) == nil {
				alias = name
				c.pkg.used[name] = true
				imported = true
				break
			}
		}
		if !imported {
			// Import names cannot be rebound anywhere in the package, even
			// inside an interpolation or after this call. Conservatively
			// reserve source words, including comments and strings.
			reserved := map[string]bool{}
			for _, source := range c.files {
				if source.Prelude || source.Package != file.Package {
					continue
				}
				for _, name := range migrationIdentifiers.FindAllString(source.Source, -1) {
					reserved[name] = true
				}
			}
			used := func(name string) bool {
				if reserved[name] {
					return true
				}
				if c.lookup(name) != nil || c.pkg.imports[name] != nil || c.packageBindingNamed(name) != nil || c.isTypeName(name) {
					return true
				}
				_, exists := c.funcNamed(name)
				return exists
			}
			for n := 2; used(alias); n++ {
				alias = fmt.Sprintf("encoding%d", n)
			}
			text := "import "
			if alias != "encoding" {
				text += alias + " "
			}
			text += "\"bork/encoding\"\n"
			at := diag.Pos{File: file.Path, Line: 1, Col: 1}
			// Keep a script's shebang first; inserting on a fresh line also works
			// when existing imports share a line with declarations or comments.
			if strings.HasPrefix(file.Source, "#!") {
				at.Line = 2
			}
			edits = append(edits, diag.TextEdit{Start: at, End: at, Replacement: text})
		}
		end := id.Pos
		end.Col += len(id.Name)
		edits = append(edits, diag.TextEdit{Start: id.Pos, End: end, Replacement: alias + "." + target})
	}
	c.diags.Suggest(id.Pos, code, call.End, diag.Fix{Message: "replace " + id.Name + " with " + target, Edits: edits})
	return true
}
