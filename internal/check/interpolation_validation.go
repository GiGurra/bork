package check

import (
	"encoding/json"
	"fmt"
	"go/constant"
	"sort"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// InterpolationSite retains only a constant validator call and source positions.
// Runtime factories, builders, and hole values never enter the recipe.
type InterpolationSite struct {
	Prefix    diag.Pos
	Holes     []diag.Pos
	Validator string
	Package   string
	Call      *Call
	Key       string
	Index     int
}

// InterpolationBatch evaluates distinct calls once for a source package.
type InterpolationBatch struct {
	Recipe *Comptime
	Sites  []*InterpolationSite
}

func nominalOwner(t Type) *Package {
	switch t := t.(type) {
	case *Record:
		return t.Pkg
	case *Sealed:
		return t.Pkg
	case *Opaque:
		return t.Pkg
	case *Resource:
		return t.Pkg
	}
	return nil
}

func (c *checker) checkInterpolationValidators() {
	for site, factory := range c.info.interpolatorFactories {
		c.interpolatorValidator(site, c.info.types[factory])
	}
}

func (c *checker) interpolatorValidator(site *syntax.Interp, builder Type) {
	builder = c.zonk(builder)
	owner := nominalOwner(builder)
	if owner == nil {
		return
	}
	class := c.preludePkg.classes["InterpolationValidator"]
	found := false
	for _, inst := range owner.instances {
		if inst.Class == class {
			if _, matches := matchHead(inst, builder); matches {
				found = true
				if len(inst.Constraints) != 0 {
					c.diags.AddCode(site.Pos, "interpolation.validator", "interpolation validator %s cannot have a constrained instance head", inst.Name)
					return
				}
			}
		}
	}
	if !found {
		return
	}
	// Root selection and every bound dictionary use the library owner's scope.
	// Imported validators cannot replace the owner's construction contract.
	previous, scope := c.pkg, owner.inScope
	c.pkg = owner
	owner.inScope = nil
	for _, inst := range scope {
		if inst.Class != class || inst.Pkg == owner {
			owner.inScope = append(owner.inScope, inst)
		}
	}
	dict := c.dict(class, builder, site.Pos, 0)
	owner.inScope, c.pkg = scope, previous
	if dict == nil {
		return
	}
	var closed func(*Dict) bool
	closed = func(d *Dict) bool {
		if d == nil || d.Param != nil {
			return false
		}
		for _, t := range d.TypeArgs {
			if hasTypeParam(t) {
				return false
			}
		}
		for _, arg := range d.Args {
			if !closed(arg) {
				return false
			}
		}
		return true
	}
	if !closed(dict) {
		c.diags.AddCode(site.Pos, "interpolation.validator", "interpolation validator %s needs closed type arguments and dictionaries", dict.Inst.Name)
		return
	}
	if len(dict.Inst.Methods[0].Needs) != 0 {
		c.diags.AddCode(site.Pos, "interpolation.validator", "interpolation validator %s cannot require ambient values", dict.Inst.Name)
		return
	}
	c.info.interpolatorValidators[site] = dict
}

type interpolationKind struct {
	Kind    string
	Package string
	Name    string
}

func interpolationKinds(t Type) []interpolationKind {
	if union, ok := t.(*Union); ok {
		var kinds []interpolationKind
		for _, member := range union.Members {
			kinds = append(kinds, interpolationKinds(member)...)
		}
		return kinds
	}
	if basic, ok := t.(*Basic); ok {
		return []interpolationKind{{Kind: "Builtin", Name: basic.name}}
	}
	if owner := nominalOwner(t); owner != nil {
		name := ""
		switch t := t.(type) {
		case *Record:
			name = t.Name
		case *Sealed:
			name = t.Name
		case *Opaque:
			name = t.Name
		case *Resource:
			name = t.Name
		}
		return []interpolationKind{{Kind: "Named", Package: owner.Path, Name: name}}
	}
	return []interpolationKind{{Kind: "Unknown"}}
}

func (l *lowerer) interpolationSite(source *syntax.Interp, dict *Dict, parts Expr) {
	at := source.Pos
	prelude := dict.Class.Pkg
	holeType := prelude.types["InterpolationHole"].typ.(*Record)
	kindType := prelude.types["InterpolationKind"].typ.(*Sealed)
	holes := &ListLit{expr: expr{pos: at, typ: &List{Elem: holeType}}}
	text := func(s string) Expr { return &Const{expr: expr{pos: at, typ: String}, Value: constant.MakeString(s)} }
	var metadata [][]interpolationKind
	site := &InterpolationSite{Prefix: at, Validator: dict.Inst.Name}
	if dict.Inst.Pkg.Path != "" {
		site.Validator = dict.Inst.Pkg.Path + "." + site.Validator
	}
	if file := l.sources[at.File]; file != nil {
		site.Package = file.Package
	}
	for _, hole := range source.Exprs {
		site.Holes = append(site.Holes, hole.Position())
		kinds := interpolationKinds(l.info.types[hole])
		metadata = append(metadata, kinds)
		values := &ListLit{expr: expr{pos: at, typ: &List{Elem: kindType}}}
		for _, kind := range kinds {
			variant := kindType.Variant(kind.Kind)
			var value Expr = &VariantValue{expr: expr{pos: at, typ: kindType}, Variant: variant, Text: "InterpolationKind." + kind.Kind}
			if kind.Kind != "Unknown" {
				record := &RecordLit{expr: expr{pos: at, typ: kindType}, Variant: variant}
				for _, field := range variant.Fields {
					content := kind.Name
					if field.Name == "packagePath" {
						content = kind.Package
					}
					record.Fields = append(record.Fields, &FieldValue{Name: field.Name, Field: field, Value: text(content)})
				}
				value = record
			}
			values.Elems = append(values.Elems, value)
		}
		holes.Elems = append(holes.Elems, &RecordLit{expr: expr{pos: at, typ: holeType}, Record: holeType, Fields: []*FieldValue{{Name: "kinds", Field: holeType.Fields[0], Value: values}}})
	}
	fn := dict.Inst.Methods[0]
	bound := map[*TypeParam]Type{}
	for i, tp := range fn.TypeParams {
		bound[tp] = dict.TypeArgs[i]
	}
	inst := &Instance{Func: fn, TypeArgs: dict.TypeArgs, Dicts: dict.Args, Result: subst(fn.Result, bound)}
	for _, param := range fn.Params {
		inst.Params = append(inst.Params, subst(param, bound))
	}
	site.Call = &Call{expr: expr{pos: at, typ: inst.Result}, Func: fn, Inst: inst, Args: []Expr{parts, holes}}
	// Pointer identities are valid only inside this build. No cross-build reuse
	// is authorized without a certified execution receipt.
	var identity strings.Builder
	var dictionary func(*Dict)
	dictionary = func(d *Dict) {
		fmt.Fprintf(&identity, "%p/%p/", d.Inst, d.Class)
		for _, t := range d.TypeArgs {
			fmt.Fprintf(&identity, "%s;", typeKey(t))
		}
		for _, arg := range d.Args {
			dictionary(arg)
		}
	}
	dictionary(dict)
	descriptor, _ := json.Marshal(struct {
		Parts []string
		Kinds [][]interpolationKind
	}{source.Parts, metadata})
	site.Key = identity.String() + string(descriptor)
	l.interpolationSites = append(l.interpolationSites, site)
}

func (l *lowerer) interpolationBatches() {
	sort.SliceStable(l.interpolationSites, func(i, j int) bool {
		a, b := l.interpolationSites[i].Prefix, l.interpolationSites[j].Prefix
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	batches := map[string]*InterpolationBatch{}
	memo := map[string]*Call{}
	for _, site := range l.interpolationSites {
		batch := batches[site.Package]
		if batch == nil {
			values := &ListLit{expr: expr{pos: site.Prefix, typ: &List{Elem: site.Call.Type()}}}
			body := &Block{expr: expr{pos: site.Prefix, typ: values.Type()}, Tail: values, End: site.Prefix}
			batch = &InterpolationBatch{Recipe: &Comptime{expr: expr{pos: site.Prefix, typ: body.Type()}, Body: body, Owner: &Func{Decl: &syntax.FuncDecl{Pos: site.Prefix, Name: "interpolation"}, Pkg: site.Call.Func.Pkg, Result: body.Type()}}}
			batches[site.Package] = batch
			l.info.InterpolationBatches = append(l.info.InterpolationBatches, batch)
		}
		if shared := memo[site.Key]; shared != nil {
			site.Call = shared
		} else {
			memo[site.Key] = site.Call
		}
		values := batch.Recipe.Body.Tail.(*ListLit)
		site.Index = -1
		for i, value := range values.Elems {
			if value == site.Call {
				site.Index = i
				break
			}
		}
		if site.Index < 0 {
			site.Index = len(values.Elems)
			values.Elems = append(values.Elems, site.Call)
		}
		batch.Sites = append(batch.Sites, site)
	}
}

// InterpolationDiagnostics maps metadata-only results back to original holes.
func InterpolationDiagnostics(batch *InterpolationBatch, diags *diag.List) {
	result, ok := batch.Recipe.Value.(*ListLit)
	if !ok || len(result.Elems) != len(batch.Recipe.Body.Tail.(*ListLit).Elems) {
		diags.AddCode(batch.Recipe.Pos(), "interpolation.validator", "interpolation validator returned an invalid batch result")
		return
	}
	for _, site := range batch.Sites {
		issues, ok := result.Elems[site.Index].(*ListLit)
		if !ok {
			diags.AddCode(site.Prefix, "interpolation.validator", "invalid result from validator %s", site.Validator)
			continue
		}
		for _, issue := range issues.Elems {
			record := issue.(*RecordLit)
			pos := site.Prefix
			message := ""
			for _, field := range record.Fields {
				if field.Name == "message" {
					message = constant.StringVal(field.Value.(*Const).Value)
				}
				if field.Name == "hole" {
					if some, ok := field.Value.(*RecordLit); ok {
						index, valid := constant.Int64Val(some.Fields[0].Value.(*Const).Value)
						if !valid || index < 0 || index >= int64(len(site.Holes)) {
							diags.AddCode(site.Prefix, "interpolation.validator", "validator %s returned invalid hole index %d", site.Validator, index)
							message = ""
							break
						}
						pos = site.Holes[index]
					}
				}
			}
			if message != "" {
				diags.AddCode(pos, "interpolation.validation", "%s (validator %s)", message, site.Validator)
			}
		}
	}
}
