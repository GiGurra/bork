package check

import (
	"go/constant"
	"slices"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Embedded is a compile-time asset request. The driver fills Files before
// evaluating facts or generating Go; calls share this captured snapshot.
type Embedded struct {
	Pos   diag.Pos
	Kind  string
	Path  string
	Files []EmbeddedFile
}

// EmbeddedFile holds captured data and its relative name in a directory
// snapshot. StagePath is the compiler-owned path used by Go's embed directive.
type EmbeddedFile struct {
	Name      string
	StagePath string
	Data      []byte
}

func embedIntrinsic(fn *Func) bool {
	return fn.Pkg.Path == "bork/embed" && !fn.Decl.IsMethod &&
		(fn.Decl.Name == "ReadBytes" || fn.Decl.Name == "ReadString" || fn.Decl.Name == "Directory")
}

func (c *checker) checkEmbeds() {
	for expr, inst := range c.info.funcRefs {
		if embedIntrinsic(inst.Func) {
			c.diags.AddCode(expr.Position(), "embed.direct-call", "embed.%s must be called directly with a constant path; it cannot be used as a function value", inst.Func.Decl.Name)
		}
	}
	c.info.embedCalls = map[*syntax.Call]*Embedded{}
	for call, fn := range c.info.callFuncs {
		if !embedIntrinsic(fn) {
			continue
		}
		args := c.info.args(call)
		if len(args) != 1 {
			continue // The ordinary call checker reports arity errors.
		}
		value := c.info.constantOf(args[0])
		if value == nil || value.Kind() != constant.String {
			c.diags.AddCode(call.Pos, "embed.constant", "embed.%s requires a compile-time constant String path", fn.Decl.Name)
			continue
		}
		request := &Embedded{Pos: call.Pos, Kind: fn.Decl.Name, Path: constant.StringVal(value)}
		c.info.embedCalls[call] = request
		c.info.Embeds = append(c.info.Embeds, request)
	}
	slices.SortFunc(c.info.Embeds, func(a, b *Embedded) int {
		if a.Pos.File != b.Pos.File {
			if a.Pos.File < b.Pos.File {
				return -1
			}
			return 1
		}
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line - b.Pos.Line
		}
		return a.Pos.Col - b.Pos.Col
	})
}
