package check

import "github.com/GiGurra/bork/internal/diag"

// ComptimePackageBindings inventories package reads through the same helpers,
// dictionaries, renderers and computed fields as package cycle checking.
func ComptimePackageBindings(info *Info, node *Comptime) []*PackageBinding {
	dependencies := newPackageDependencies(info)
	dependencies.tree(node.Body)
	for _, capture := range node.Captures {
		dependencies.tree(capture.Let.Value)
	}
	seen := map[*Func]bool{}
	var visit func(*Func)
	visit = func(fn *Func) {
		if fn == nil || seen[fn] {
			return
		}
		seen[fn] = true
		for binding := range info.packageGraph[fn].values {
			dependencies.values[binding] = true
		}
		for callee := range info.packageGraph[fn].calls {
			visit(callee)
		}
	}
	for fn := range dependencies.calls {
		visit(fn)
	}
	var out []*PackageBinding
	for _, binding := range info.PackageBindings {
		if dependencies.values[binding] {
			out = append(out, binding)
		}
	}
	return out
}

// PackageComptime gives an initializer an independent checked return boundary.
func PackageComptime(binding *PackageBinding) *Comptime {
	at := expr{pos: binding.Decl.Pos, typ: binding.Type}
	return &Comptime{expr: at, Owner: binding.Boundary, Body: &Block{expr: at, Tail: binding.Value.Value}}
}

// BakePackageComptime retains the runtime memo cell, but its recipe now returns
// decoded data instead of recomputing an initializer that comptime already used.
func BakePackageComptime(binding *PackageBinding, node *Comptime) {
	binding.Value.Value = node.Value
	binding.Value.Initializer.Body = node.Value
}

// Check an annotation before a dependent recipe can use the package promise.
func ComptimePackageResult(binding *PackageBinding, node *Comptime, info *Info, diags *diag.List, eval Evaluator) {
	let := *binding.Value
	let.Value, let.Initializer = node.Value, nil
	result := *node
	result.Body = &Block{expr: node.expr, Stmts: []Stmt{&let}, Tail: node.Value}
	ComptimeRecipe(&result, info, diags, eval, nil)
}
