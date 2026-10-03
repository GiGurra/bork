package check

import (
	"crypto/sha256"
	"fmt"
	"go/constant"
	"slices"
	"strconv"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// BuildRead is a direct intrinsic request. The driver fills Data from a rooted,
// frozen inventory before any compile-time execution.
type BuildRead struct {
	Pos        diag.Pos
	Kind, Path string
	Data       []byte
	Captured   bool
}

func (read *BuildRead) literalText() string {
	if read.Kind == "ReadString" {
		return strconv.Quote(string(read.Data))
	}
	return fmt.Sprintf("build.ReadBytes(%q; %d bytes; sha256 %x)", read.Path, len(read.Data), sha256.Sum256(read.Data))
}

func buildIntrinsic(fn *Func) bool {
	return fn.Pkg.Path == "bork/build" && !fn.Decl.IsMethod &&
		(fn.Decl.Name == "ReadBytes" || fn.Decl.Name == "ReadString")
}

func (c *checker) checkBuildReads() {
	for expr, inst := range c.info.funcRefs {
		if buildIntrinsic(inst.Func) {
			c.diags.AddCode(expr.Position(), "build.direct-call", "build.%s must be called directly with a constant path; it cannot be used as a function value", inst.Func.Decl.Name)
		}
	}
	c.info.buildCalls = map[*syntax.Call]*BuildRead{}
	for call, fn := range c.info.callFuncs {
		if !buildIntrinsic(fn) {
			continue
		}
		args := c.info.args(call)
		if len(args) != 1 {
			continue
		}
		value := c.info.constantOf(args[0])
		if value == nil || value.Kind() != constant.String {
			c.diags.AddCode(call.Pos, "build.constant", "build.%s requires a compile-time constant String path", fn.Decl.Name)
			continue
		}
		request := &BuildRead{Pos: call.Pos, Kind: fn.Decl.Name, Path: constant.StringVal(value)}
		c.info.buildCalls[call] = request
		c.info.BuildReads = append(c.info.BuildReads, request)
	}
	slices.SortFunc(c.info.BuildReads, func(a, b *BuildRead) int {
		if a.Pos.File < b.Pos.File {
			return -1
		}
		if a.Pos.File > b.Pos.File {
			return 1
		}
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line - b.Pos.Line
		}
		return a.Pos.Col - b.Pos.Col
	})
}
