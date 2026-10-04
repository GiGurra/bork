package check

import (
	"go/constant"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

func executionAuditProgram(t *testing.T, source string) *Info {
	t.Helper()
	diags := &diag.List{}
	files := prelude.Parse(diags)
	file := syntax.Parse("execution-test.bork", []byte(source), diags)
	file.Package = "example.com/execution"
	files = append(files, file)
	info := Program(files, file.Package, diags, nil)
	if diags.Len() != 0 {
		t.Fatal(diags.Error())
	}
	return info
}
func executionAuditIntQuery(info *Info, name string) Query {
	return Query{Pred: info.Funcs[name], Args: []constant.Value{constant.MakeInt64(1)}}
}
func TestExecutionAuditTransitiveDependencies(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"pure helper", "fn twice(n:Int):Int{n+n}\npred p(n:Int){twice(n)>0}", ""},
		{"unsafe helper", "fn bad(n:Int):Int unsafe go{return n}\npred p(n:Int){bad(n)>0}", "unsafe"},
		{"skipped unsafe", "fn bad():Bool unsafe go{return true}\npred p(n:Int){if(n>0){true}else{bad()}}", "unsafe"},
		{"parameter default", "pred defaultOk(n:Int) unsafe go{return true}\nfn value(n:Int where defaultOk=1):Int{n}\npred p(n:Int){value()>0}", "unsafe"},
		{"record default", "pred defaultOk(n:Int) unsafe go{return true}\ntype R={value:Int where defaultOk=1}\npred p(n:Int){R{}.value>0}", "unsafe"},
		{"closed alias callback", "fn bad():Bool unsafe go{return true}\npred p(n:Int){cb=bad;cb()}", "unsafe"},
		{"pure local callback", "pred p(n:Int){cb=()=>n>0;cb()}", ""},
		{"higher order decline", "fn call(cb:()=>Bool):Bool{cb()}\npred p(n:Int){call(()=>n>0)}", "effectful"},
		{"generic decline", "fn id[T](n:T):T{n}\npred p(n:Int){id(n)>0}", "generic"},
		{"deferred local", "pred p(n:Int){lazy answer=n>0;answer}", "deferred"},
		{"runtime package memo", "lazy value=1\npred p(n:Int){value>0}", "runtime"},
		{"user len is not intrinsic", "fn byteLength(s:String):Int unsafe go{return int64(len(s))}\npred p(n:Int){byteLength(\"x\")>0}", "unsafe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := executionAuditProgram(t, tc.source)
			audit := AuditExecutionQueries(info, []Query{executionAuditIntQuery(info, "p")})
			if tc.want == "" {
				if audit.Decline != "" {
					t.Fatal(audit.Decline)
				}
			} else if !strings.Contains(audit.Decline, tc.want) {
				t.Fatalf("want decline %q, got %+v", tc.want, audit)
			}
		})
	}
}
func TestExecutionAuditIntrinsicIdentityAndMutation(t *testing.T) {
	info := executionAuditProgram(t, "pred p(s:String){s.byteLength()>0}")
	query := Query{Pred: info.Funcs["p"], Args: []constant.Value{constant.MakeString(string([]byte{0xff, 'x'}))}}
	audit := AuditExecutionQueries(info, []Query{query})
	if audit.Decline != "" || len(audit.Intrinsics) != 1 || audit.Intrinsics[0].Contract != "string-byte-length-v1" {
		t.Fatalf("intrinsic did not qualify: %+v", audit)
	}
	var intrinsic *Func
	for _, fn := range info.FuncOf {
		if fn.Prelude && fn.Decl.Name == "byteLength" {
			intrinsic = fn
			break
		}
	}
	if intrinsic == nil {
		t.Fatal("missing prelude intrinsic")
	}
	intrinsic.Decl.GoBody.Body = "return int64(len(s))+1"
	if got := AuditExecutionQueries(info, []Query{query}); !strings.Contains(got.Decline, "unsafe") {
		t.Fatalf("changed intrinsic qualified: %+v", got)
	}
}
func TestExecutionAuditAllQueryLeavesAndTypedArguments(t *testing.T) {
	info := executionAuditProgram(t, "pred good(n:Int){n>0}\npred bad(n:Int) unsafe go{return true}")
	good, bad := executionAuditIntQuery(info, "good"), executionAuditIntQuery(info, "bad")
	for _, query := range []Query{{And: []Query{good, bad}}, {Or: []Query{good, bad}}} {
		if audit := AuditExecutionQueries(info, []Query{query}); !strings.Contains(audit.Decline, "unsafe") {
			t.Fatalf("omitted query leaf: %+v", audit)
		}
	}
	query := good
	query.Values = []Expr{&Call{expr: expr{typ: Int}, Func: info.Funcs["good"]}}
	query.Args = nil
	if audit := AuditExecutionQueries(info, []Query{query}); !strings.Contains(audit.Decline, "closed") {
		t.Fatalf("runtime argument qualified: %+v", audit)
	}
	if audit := AuditExecutionQueries(info, []Query{{Pred: good.Pred, And: []Query{good}}}); audit.Decline == "" {
		t.Fatal("ambiguous batch qualified")
	}
}

type unknownExecutionExpr struct{ expr }
type unknownExecutionStmt struct{}

func (*unknownExecutionStmt) stmtNode() {}
func TestExecutionAuditUnknownNodesAndBudgets(t *testing.T) {
	info := executionAuditProgram(t, "fn f():Int{comptime{1}}")
	node := info.Comptimes[0]
	original := node.Body.Tail
	node.Body.Tail = &unknownExecutionExpr{expr: expr{typ: Int}}
	if audit := AuditComptimeExecution(info, node); !strings.Contains(audit.Decline, "unknown typed expression") {
		t.Fatalf("unknown expression qualified: %+v", audit)
	}
	node.Body.Tail = original
	node.Body.Stmts = []Stmt{&unknownExecutionStmt{}}
	if audit := AuditComptimeExecution(info, node); !strings.Contains(audit.Decline, "unknown typed statement") {
		t.Fatalf("unknown statement qualified: %+v", audit)
	}
	node.Body.Stmts = nil
	x := original
	for range executionAuditDepthLimit + 1 {
		x = &Unary{expr: expr{typ: Int}, Op: syntax.Minus, X: x}
	}
	node.Body.Tail = x
	if audit := AuditComptimeExecution(info, node); !strings.Contains(audit.Decline, "budget") {
		t.Fatalf("deep audit qualified: %+v", audit)
	}
}
func TestExecutionAuditClosedCaptureAndPrior(t *testing.T) {
	info := executionAuditProgram(t, "fn f():Int{n=2;first=comptime{n+1};comptime{first+1}}")
	first, second := info.Comptimes[0], info.Comptimes[1]
	if audit := AuditComptimeExecution(info, first); audit.Decline != "" {
		t.Fatal(audit.Decline)
	}
	if audit := AuditComptimeExecution(info, second); !strings.Contains(audit.Decline, "prior") {
		t.Fatalf("unevaluated prior qualified: %+v", audit)
	}
	first.Value = &Const{expr: expr{typ: Int}, Value: constant.MakeInt64(3)}
	if audit := AuditComptimeExecution(info, second); audit.Decline != "" || len(audit.Priors) != 1 {
		t.Fatalf("validated prior not inventoried: %+v", audit)
	}
}

func TestExecutionAuditResolvedDictionaryMethods(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "pure", true: "unsafe"}[foreign], func(t *testing.T) {
			method := "fn valid(n:Int):Bool{n>0}"
			if foreign {
				method = "fn valid(n:Int):Bool unsafe go{return true}"
			}
			info := executionAuditProgram(t, "class Check[T]{fn valid(n:T):Bool}\ninstance intCheck:Check[Int]{"+method+"}\npred p(n:Int){n>0}")
			var selected *ClassInstance
			for _, inst := range info.ClassInstances {
				if inst.Name == "intCheck" {
					selected = inst
					break
				}
			}
			if selected == nil {
				t.Fatal("missing selected instance")
			}
			auditor := newExecutionAuditor(info)
			auditor.dict(&Dict{Class: selected.Class, Type: Int, Inst: selected}, 0)
			if foreign {
				if !strings.Contains(auditor.report.Decline, "unsafe") {
					t.Fatalf("unsafe dictionary escaped: %+v", auditor.report)
				}
			} else if auditor.report.Decline != "" {
				t.Fatal(auditor.report.Decline)
			}
		})
	}
}

func TestExecutionAuditBuildEmbeddedUnknownTargetsAndTypes(t *testing.T) {
	info := executionAuditProgram(t, "fn helper(n:Int):Int{n}\nfn f():Int{comptime{helper(1)}}")
	node := info.Comptimes[0]
	call := node.Body.Tail.(*Call)
	call.BuildRead = &BuildRead{}
	if got := AuditComptimeExecution(info, node); !strings.Contains(got.Decline, "build") {
		t.Fatalf("build read qualified: %+v", got)
	}
	call.BuildRead = nil
	call.Embedded = &Embedded{}
	if got := AuditComptimeExecution(info, node); !strings.Contains(got.Decline, "asset") {
		t.Fatalf("asset read qualified: %+v", got)
	}
	call.Embedded = nil
	original := call.Inst
	call.Inst = &Instance{Func: info.Funcs["f"], Params: original.Params, Result: original.Result}
	if got := AuditComptimeExecution(info, node); !strings.Contains(got.Decline, "target") {
		t.Fatalf("mismatched target qualified: %+v", got)
	}
	call.Inst = original
	recursive := &List{}
	recursive.Elem = recursive
	auditor := newExecutionAuditor(info)
	auditor.typ(recursive, 0)
	if auditor.nodes > executionAuditNodeLimit {
		t.Fatal("recursive type escaped budget")
	}
}

func TestExecutionAuditVariableKindsAndDeclarationIdentity(t *testing.T) {
	info := executionAuditProgram(t, "pred valid(n:Int){n>0}")
	for _, kind := range []VarKind{VarKind(999), VarScope, VarAmbient, VarLet, VarDefaultField} {
		a := newExecutionAuditor(info)
		a.expr(&VarRef{expr: expr{typ: Int}, Var: &Var{Kind: kind, Type: Int}}, 0, false)
		if a.report.Decline == "" {
			t.Fatalf("variable kind %d qualified", kind)
		}
	}
	original := info.Funcs["valid"]
	spoof := *original
	a := newExecutionAuditor(info)
	a.function(&spoof, 0)
	if !strings.Contains(a.report.Decline, "identity") {
		t.Fatalf("spoof qualified: %+v", a.report)
	}
	delete(info.FuncOf, original.Decl)
	a = newExecutionAuditor(info)
	a.function(original, 0)
	if !strings.Contains(a.report.Decline, "identity") {
		t.Fatalf("removed identity qualified: %+v", a.report)
	}
}

func TestExecutionAuditBoundedTypeRendering(t *testing.T) {
	info := executionAuditProgram(t, "pred valid(n:Int){n>0}")
	typ := Type(Int)
	for range 30 {
		typ = &Map{Key: typ, Value: typ}
	}
	a := newExecutionAuditor(info)
	a.typ(typ, 0)
	if a.report.Decline != "" {
		t.Fatalf("DAG static walk declined early: %s", a.report.Decline)
	}
	if text := a.typeText(typ); text != "" || !strings.Contains(a.report.Decline, "budget") {
		t.Fatalf("unbounded DAG rendered: len=%d decline=%s", len(text), a.report.Decline)
	}
	recursive := &List{}
	recursive.Elem = recursive
	a = newExecutionAuditor(info)
	a.typ(recursive, 0)
	if !strings.Contains(a.report.Decline, "cyclic") {
		t.Fatalf("structural cycle qualified: %+v", a.report)
	}
}

func TestExecutionAuditInvocationSignatureAndOwnedReport(t *testing.T) {
	info := executionAuditProgram(t, "fn helper(n:Int):Int{n}\nfn f():Int{comptime{helper(1)}}")
	node := info.Comptimes[0]
	report := AuditComptimeExecution(info, node)
	if report.Decline != "" || len(report.Declarations) == 0 {
		t.Fatalf("pure invocation declined: %+v", report)
	}
	oldName := report.Declarations[0].Name
	call := node.Body.Tail.(*Call)
	call.Func.Decl.Name = "mutated"
	if report.Declarations[0].Name != oldName {
		t.Fatal("report retained mutable declaration")
	}
	saved := call.Inst.Params
	call.Inst.Params = []Type{String}
	if got := AuditComptimeExecution(info, node); !strings.Contains(got.Decline, "signature") {
		t.Fatalf("mismatched signature qualified: %+v", got)
	}
	call.Inst.Params = saved
}

func TestExecutionAuditCallbackAliasVariableKinds(t *testing.T) {
	info := executionAuditProgram(t, "pred p(n:Int){cb=()=>n>0;cb()}")
	call := info.Funcs["p"].Body.Tail.(*CallValue)
	ref := call.Fun.(*VarRef)
	ref.Var.Kind = VarKind(999)
	if got := AuditExecutionQueries(info, []Query{executionAuditIntQuery(info, "p")}); !strings.Contains(got.Decline, "variable kind") {
		t.Fatalf("unknown callback alias qualified: %+v", got)
	}
}
