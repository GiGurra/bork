package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

// ComptimeProgram executes one checked recipe and writes a versioned value to
// a compiler-owned file. stdout is never part of the result protocol.
func ComptimeProgram(files []*syntax.File, info *check.Info, node *check.Comptime) ([]byte, error) {
	g := newGen(info)
	g.evalMode = true
	g.comptimeMode = true
	g.comptimeCaptures = map[*check.Var]check.Expr{}
	var captureValue func(*check.Var)
	captureValue = func(v *check.Var) {
		if _, ok := g.comptimeCaptures[v]; ok || v.Let == nil {
			return
		}
		g.comptimeCaptures[v] = v.Let.Value
		check.WalkComptime(v.Let.Value, func(x check.Expr) bool {
			if _, ok := x.(*check.Comptime); ok {
				return false
			}
			if ref, ok := x.(*check.VarRef); ok {
				captureValue(ref.Var)
			}
			return true
		})
	}
	for _, capture := range node.Captures {
		captureValue(capture)
	}
	roots := check.ComptimeHelpers(node)
	encoder := &comptimeEncoder{g: g, names: map[check.Type]string{}}
	entry, err := encoder.function(node.Type())
	if err != nil {
		return nil, err
	}
	runtime, err := parser.ParseFile(token.NewFileSet(), "", comptimeRuntime+"\nconst _ctSchemaVersion="+strconv.Itoa(check.ComptimeSchemaVersion), 0)
	if err != nil {
		return nil, err
	}
	g.extraFuncs = append(g.extraFuncs, runtime.Decls...)
	g.imports["encoding/json"] = true
	g.imports["os"] = true
	recipe := g.ComptimeLambda(node)
	encoded := &ast.CallExpr{Fun: ast.NewIdent(entry), Args: []ast.Expr{&ast.CallExpr{Fun: recipe}, &ast.BasicLit{Kind: token.INT, Value: "0"}}}
	statements := []ast.Stmt{}
	if node.Type() == check.Ok {
		statements = append(statements, &ast.ExprStmt{X: &ast.CallExpr{Fun: recipe}})
		encoded = &ast.CallExpr{Fun: ast.NewIdent(entry), Args: []ast.Expr{g.okValue(), &ast.BasicLit{Kind: token.INT, Value: "0"}}}
	}
	statements = append(statements, &ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_ctEmit"), Args: []ast.Expr{encoded}}})
	main := &ast.FuncDecl{Name: ast.NewIdent("main"), Type: &ast.FuncType{Params: &ast.FieldList{}}, Body: &ast.BlockStmt{List: statements}}
	return generate(g, files, roots, main)
}

func (g *gen) ComptimeLambda(node *check.Comptime) ast.Expr {
	return g.lambda(check.ComptimeLambda(node))
}

type comptimeEncoder struct {
	g     *gen
	names map[check.Type]string
}

func (e *comptimeEncoder) function(t check.Type) (string, error) {
	if name := e.names[t]; name != "" {
		return name, nil
	}
	encoderName := fmt.Sprintf("_ctEncode%d", len(e.names))
	e.names[t] = encoderName
	var typ bytes.Buffer
	if err := printer.Fprint(&typ, token.NewFileSet(), e.g.goType(t)); err != nil {
		return "", err
	}
	kind := strconv.Quote(check.TypeText(t, nil))
	body := ""
	scalar := func(text string) { body = "return _ctValue{Kind:" + kind + ",Text:" + text + "}" }
	switch t := t.(type) {
	case *check.Basic:
		switch {
		case t == check.Ok:
			scalar("\"\"")
		case t == check.String:
			e.g.imports["encoding/base64"] = true
			scalar("base64.StdEncoding.EncodeToString([]byte(value))")
			body = "if len(value)>_ctBudget{panic(\"comptime result exceeds 16 MiB\")};_ctReserve(base64.StdEncoding.EncodedLen(len(value)));" + body
		case t == check.Bool:
			e.g.imports["strconv"] = true
			scalar("strconv.FormatBool(value)")
		case check.IsFloat(t):
			e.g.imports["math"] = true
			e.g.imports["strconv"] = true
			bits := "math.Float64bits(value)"
			if t == check.Float32 {
				bits = "uint64(math.Float32bits(value))"
			}
			scalar("strconv.FormatUint(" + bits + ",16)")
		case check.IsNumeric(t):
			e.g.imports["strconv"] = true
			if t == check.Uint8 || t == check.Uint16 || t == check.Uint32 || t == check.Uint64 {
				scalar("strconv.FormatUint(uint64(value),10)")
			} else {
				scalar("strconv.FormatInt(int64(value),10)")
			}
		default:
			return "", fmt.Errorf("unsupported computation result %s", t)
		}
	case *check.List:
		child, err := e.function(t.Elem)
		if err != nil {
			return "", err
		}
		body = "if len(value)>1000000-_ctNodes {panic(\"comptime result exceeds node limit\")}; out:=_ctValue{Kind:" + kind + ",Nil:value==nil}; for _,item:=range value {out.Items=append(out.Items," + child + "(item,depth+1))}; return out"
	case *check.Map:
		key, err := e.function(t.Key)
		if err != nil {
			return "", fmt.Errorf("map key: %w", err)
		}
		value, err := e.function(t.Value)
		if err != nil {
			return "", fmt.Errorf("map value: %w", err)
		}
		var keyType, valueType bytes.Buffer
		if err := printer.Fprint(&keyType, token.NewFileSet(), e.g.goType(t.Key)); err != nil {
			return "", err
		}
		if err := printer.Fprint(&valueType, token.NewFileSet(), e.g.goType(t.Value)); err != nil {
			return "", err
		}
		body = "if _,ok:=value.impl().(*_mapCore);!ok{panic(\"comptime map result must use insertion order; call inOrder() on sorted maps\")};if value.impl().size()>(1000000-_ctNodes)/2{panic(\"comptime result exceeds node limit\")};out:=_ctValue{Kind:" + kind + "};value.impl().each(func(entry *_mapEntry)bool{out.Items=append(out.Items," + key + "(entry.key.(" + keyType.String() + "),depth+1)," + value + "(entry.val.(" + valueType.String() + "),depth+1));return true});return out"
	case *check.Sealed:
		body = "switch value:=value.(type){"
		for _, variant := range t.Variants {
			var variantType bytes.Buffer
			if err := printer.Fprint(&variantType, token.NewFileSet(), e.g.variantType(variant)); err != nil {
				return "", err
			}
			var items []string
			for _, field := range variant.Fields {
				child, err := e.function(field.Type)
				if err != nil {
					return "", fmt.Errorf("variant %s field %s: %w", variant.Name, field.Name, err)
				}
				items = append(items, child+"(value."+name(field.Name).Name+",depth+1)")
			}
			tag := strconv.Quote(variant.Name)
			body += "case " + variantType.String() + ":_=value;_ctReserve(6*len(" + tag + "));return _ctValue{Kind:" + kind + ",Tag:" + tag + ",Items:[]_ctValue{" + strings.Join(items, ",") + "}};"
		}
		body += "default:panic(\"invalid comptime sealed value\")}"
	case *check.Union:
		body = "switch value:=value.(type){"
		for index, member := range t.Members {
			child, err := e.function(member)
			if err != nil {
				return "", fmt.Errorf("union member %s: %w", member, err)
			}
			var memberType bytes.Buffer
			if err := printer.Fprint(&memberType, token.NewFileSet(), e.g.goType(member)); err != nil {
				return "", err
			}
			body += "case " + memberType.String() + ":return _ctValue{Kind:" + kind + ",Tag:" + strconv.Quote(strconv.Itoa(index)) + ",Items:[]_ctValue{" + child + "(value,depth+1)}};"
		}
		body += "default:panic(\"invalid comptime union value\")}"
	case *check.Record:
		if t.GoMirror != nil {
			return "", fmt.Errorf("go mirror result %s is not supported yet", t)
		}
		var items []string
		for _, field := range t.Fields {
			child, err := e.function(field.Type)
			if err != nil {
				return "", fmt.Errorf("field %s: %w", field.Name, err)
			}
			items = append(items, child+"(value."+name(field.Name).Name+",depth+1)")
		}
		body = "return _ctValue{Kind:" + kind + ",Items:[]_ctValue{" + strings.Join(items, ",") + "}}"
	default:
		return "", fmt.Errorf("comptime literal evaluator does not support %s yet", t)
	}
	source := "package main\nfunc " + encoderName + "(value " + typ.String() + ", depth int) _ctValue {_ctCheck(depth," + kind + ");" + body + "}"
	parsed, err := parser.ParseFile(token.NewFileSet(), "", source, 0)
	if err != nil {
		return "", err
	}
	e.g.extraFuncs = append(e.g.extraFuncs, parsed.Decls...)
	return encoderName, nil
}

const comptimeRuntime = `package main

type _ctValue struct {
 Kind string
 Text string
 Tag string
 Items []_ctValue
 Nil bool
}
var _ctNodes int
var _ctBudget = (16<<20)-128
func _ctReserve(size int){if size>_ctBudget{panic("comptime result exceeds 16 MiB")};_ctBudget-=size}
func _ctCheck(depth int,kind string) {
 _ctReserve(80+6*len(kind))
 if depth>256 {panic("comptime result exceeds depth limit")}
 _ctNodes++
 if _ctNodes>1000000 {panic("comptime result exceeds node limit")}
}
type _ctWriter struct {file *os.File; remaining int}
func (w *_ctWriter) Write(p []byte)(int,error){
 if len(p)>w.remaining {panic("comptime result exceeds 16 MiB")}
 w.remaining-=len(p)
 return w.file.Write(p)
}
func _ctEmit(value _ctValue){
 file,err:=os.OpenFile(os.Args[1],os.O_WRONLY|os.O_CREATE|os.O_TRUNC,0600)
 if err!=nil {panic(err)}
 defer file.Close()
 if err:=json.NewEncoder(&_ctWriter{file:file,remaining:16<<20}).Encode(struct{Version int;Value _ctValue}{_ctSchemaVersion,value});err!=nil {panic(err)}
}
`

// ComptimeFunctions includes helpers referenced by trusted Go bodies as well
// as ordinary typed calls. Generation uses the same reachability inventory.
func ComptimeFunctions(files []*syntax.File, info *check.Info, node *check.Comptime, dictionaries ...*check.Dict) []*check.Func {
	reachable := newGen(info).reachable(check.ComptimeHelpers(node, dictionaries...))
	var out []*check.Func
	for _, file := range files {
		for _, decl := range file.Funcs {
			if fn := info.FuncOf[decl]; reachable[fn] {
				out = append(out, fn)
			}
		}
	}
	return out
}
