package driver

import (
	"crypto/sha256"
	"fmt"
	"go/constant"
	"runtime"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/gen"
	"github.com/GiGurra/bork/internal/stdvalidators"
	"github.com/GiGurra/bork/internal/syntax"
)

const nativeInterpolationMode = "compiler-interpolator-v1"

type nativeInterpolationPlan struct {
	namespace [sha256.Size]byte
	calls     []stdvalidators.Call
}

// This mode executes compiler-owned intrinsic code. It makes no live-SDK or
// evaluated-result freshness claim, and remains evaluator-bearing for Session.
func prepareNativeInterpolation(files []*syntax.File, info *check.Info, node *check.Comptime, ctx *goContext) (*nativeInterpolationPlan, bool) {
	if ctx.err != nil || ctx.driverErr != nil || ctx.driver != "off" || ctx.values["GOOS"] != runtime.GOOS || ctx.values["GOARCH"] != runtime.GOARCH || ctx.values["GOFLAGS"] != "" || ctx.values["GOEXPERIMENT"] != "" || ctx.values["GODEBUG"] != "" || ctx.values["GOFIPS140"] != "off" || (runtime.GOARCH == "amd64" && ctx.values["GOAMD64"] != "v1") || ctx.values["GOVERSION"] != runtime.Version() || ctx.processValue("GOCACHEPROG") != "" || ctx.moduleHook != nil {
		return nil, false
	}
	values, ok := node.Body.Tail.(*check.ListLit)
	if !ok || len(node.Body.Stmts) != 0 || len(node.Captures) != 0 || len(values.Elems) == 0 {
		return nil, false
	}
	sources := map[string]string{}
	for _, file := range files {
		digest := sha256.Sum256([]byte(file.Source))
		sources[file.Path] = fmt.Sprintf("%x", digest)
	}
	definitions := map[string]string{}
	for _, helper := range gen.ComptimeFunctions(files, info, node) {
		encoded, err := gen.ArtifactDefinition(helper)
		if err != nil {
			return nil, false
		}
		digest := sha256.Sum256(append(encoded, []byte(gen.ArtifactSignature(helper))...))
		definitions[helper.Decl.Pos.String()] = fmt.Sprintf("%x", digest)
	}
	plan := &nativeInterpolationPlan{}
	inputBytes := 0
	expectedDefinitions := map[string]string{}
	for _, value := range values.Elems {
		call, ok := value.(*check.Call)
		if !ok || call.Func == nil || call.Func.Of == nil || call.Inst == nil || call.Inst.Func != call.Func || len(call.Inst.TypeArgs) != 0 || len(call.Inst.Dicts) != 0 || len(call.Args) != 2 {
			return nil, false
		}
		ci := call.Func.Of
		if ci.Class == nil || !ci.Class.Prelude || ci.Class.Name != "InterpolationValidator" || len(ci.TypeParams) != 0 || len(ci.Constraints) != 0 || ci.Methods[0] != call.Func {
			return nil, false
		}
		binding, ok := stdvalidators.Lookup(ci.Pkg.Path, ci.Name, ci.Type.String(), gen.ArtifactSignature(call.Func))
		if !ok {
			return nil, false
		}
		descriptor := binding.Descriptor()
		for _, source := range descriptor.Sources {
			if sources[source.Path] != source.Digest {
				return nil, false
			}
		}
		for _, definition := range descriptor.Definitions {
			expectedDefinitions[definition.Path] = definition.Digest
			if definitions[definition.Path] != definition.Digest {
				return nil, false
			}
		}
		request, ok := nativeInterpolationRequest(call)
		if !ok {
			return nil, false
		}
		inputBytes += 32
		for _, part := range request.Parts {
			inputBytes += 16 + len(part)
		}
		for _, hole := range request.Holes {
			inputBytes += 32
			for _, kind := range hole {
				inputBytes += 32 + len(kind.Tag) + len(kind.PackagePath) + len(kind.Name)
			}
		}
		if inputBytes > stdvalidators.InputLimit {
			return nil, false
		}
		plan.calls = append(plan.calls, stdvalidators.Call{Binding: binding, Request: request})
	}
	if len(expectedDefinitions) != len(definitions) {
		return nil, false
	}
	namespace, err := compilerArtifactNamespace(nativeInterpolationMode, "artifact-abi-1")
	if err != nil {
		return nil, false
	}
	plan.namespace = namespace
	return plan, true
}

func nativeInterpolationRequest(call *check.Call) (stdvalidators.Request, bool) {
	request := stdvalidators.Request{}
	parts, ok := call.Args[0].(*check.RecordLit)
	if !ok || len(parts.Fields) != 1 || parts.Fields[0].Name != "values" {
		return request, false
	}
	list, ok := parts.Fields[0].Value.(*check.ListLit)
	if !ok {
		return request, false
	}
	text := func(value check.Expr) (string, bool) {
		c, ok := value.(*check.Const)
		if !ok || c.Value.Kind() != constant.String {
			return "", false
		}
		return constant.StringVal(c.Value), true
	}
	for _, value := range list.Elems {
		s, ok := text(value)
		if !ok {
			return request, false
		}
		request.Parts = append(request.Parts, s)
	}
	holes, ok := call.Args[1].(*check.ListLit)
	if !ok || len(request.Parts) != len(holes.Elems)+1 {
		return request, false
	}
	for _, value := range holes.Elems {
		hole, ok := value.(*check.RecordLit)
		if !ok || len(hole.Fields) != 1 || hole.Fields[0].Name != "kinds" {
			return request, false
		}
		kinds, ok := hole.Fields[0].Value.(*check.ListLit)
		if !ok || len(kinds.Elems) == 0 {
			return request, false
		}
		var out []stdvalidators.Kind
		for _, value := range kinds.Elems {
			kind := stdvalidators.Kind{}
			switch value := value.(type) {
			case *check.VariantValue:
				if value.Variant.Name != "Unknown" {
					return request, false
				}
				kind.Tag = "Unknown"
			case *check.RecordLit:
				if value.Variant == nil {
					return request, false
				}
				kind.Tag = value.Variant.Name
				if kind.Tag != "Named" && kind.Tag != "Builtin" {
					return request, false
				}
				for _, field := range value.Fields {
					s, ok := text(field.Value)
					if !ok {
						return request, false
					}
					switch field.Name {
					case "name":
						kind.Name = s
					case "packagePath":
						kind.PackagePath = s
					default:
						return request, false
					}
				}
			default:
				return request, false
			}
			out = append(out, kind)
		}
		request.Holes = append(request.Holes, out)
	}
	return request, true
}

// Compiler intrinsic attempts are deliberately uncertified and distinct from
// live-Go closure observations. Preparation owns no token on fallback.
func runNativeInterpolation(plan *nativeInterpolationPlan, ctx *goContext, usage *goUsage) ([]byte, error) {
	if usage != nil {
		usage.evaluator = true
		if usage.execution == nil {
			usage.execution = &executionTracker{}
		}
		token := usage.execution.begin(nil)
		defer usage.execution.decline(token, executionClosureUnavailable)
	}
	return stdvalidators.Run(plan.calls, ctx.comptimeLimit())
}
