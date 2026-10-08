package driver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestListCarryReuse(t *testing.T) {
	cases := []struct {
		name, body, want string
		reuse            bool
	}{

		{"nil_concat", `out = nilList(); for i in range(0, 1) { _ = i; out = out.concat([]) }; println(isNil(out))`, `false`, true},
		{"nil_entry", `out = nilList(); for i in range(0, 0) { out = out.append(i) }; println(isNil(out))`, `true`, true},
		{"empty_entry", `out: List[Int] = []; for i in range(0, 0) { out = out.append(i) }; println(isNil(out))`, `false`, true},
		{"append_twice", `out: List[Int] = []; for i in range(0, 3) { out = out.append(i).append(i) }; println(out)`, `[0, 0, 1, 1, 2, 2]`, true},
		{"break_after", `out: List[Int] = []; for i in range(0, 9) { out = out.append(i); if i == 2 { break } }; println(out)`, `[0, 1, 2]`, true},
		{"two_branches", `out: List[Int] = []; for i in range(0, 4) { if i % 2 == 0 { out = out.append(i) } else { out = out.concat([9]) } }; println(out)`, `[0, 9, 2, 9]`, true},
		{"read_after_transfer", `out: List[Int] = []; for i in range(0, 3) { prior = out; out = prior.append(i); println(prior.length()) }; println(out)`, `0
1
2
[0, 1, 2]`, false},
		{"branch_snapshot", `out: List[Int] = []; saved: List[List[Int]] = []; for i in range(0, 4) { if i % 2 == 0 { saved = saved.append(out) }; out = out.append(i) }; println(saved, out)`, `[[], [0, 1]] [0, 1, 2, 3]`, false},
		{"self_concat", `out = [1]; for i in range(0, 2) { _ = i; out = out.concat(out) }; println(out)`, `[1, 1, 1, 1]`, false},
		{"header_post", `out: List[Int] = []; for (i = 0, n = 0; i < 3; i = i + 1, n = out.length()) { out = out.append(n) }; println(out)`, `[0, 1, 2]`, true},
		{"nested", `out: List[Int] = []; for i in range(0, 2) { for j in range(0, 2) { out = out.append(i + j) } }; println(out)`, `[0, 1, 1, 2]`, true},
		{"append", `out: List[Int] = []; for i in range(0, 5) { out = out.append(i) }; println(out)`, `[0, 1, 2, 3, 4]`, true},
		{"if_value", `out: List[Int] = []; for i in range(0, 5) { out = if i % 2 == 0 { out.append(i) } else { out } }; println(out)`, `[0, 2, 4]`, true},
		{"if_statement", `out: List[Int] = []; for i in range(0, 5) { if i % 2 == 0 { out = out.append(i) } }; println(out)`, `[0, 2, 4]`, true},
		{"match_value", `out: List[Int] = []; for i in range(0, 5) { out = match i % 2 { 0 => out.append(i), _ => out } }; println(out)`, `[0, 2, 4]`, true},
		{"match_statement", `out: List[Int] = []; for i in range(0, 5) { match i % 2 { 0 => { out = out.append(i) }, _ => {} } }; println(out)`, `[0, 2, 4]`, true},
		{"concat", `out: List[Int] = []; for i in range(0, 3) { out = out.concat([i, i]) }; println(out)`, `[0, 0, 1, 1, 2, 2]`, true},
		{"break", `out: List[Int] = []; for i in range(0, 9) { if i == 3 { break }; out = out.append(i) }; println(out)`, `[0, 1, 2]`, true},
		{"continue", `out: List[Int] = []; for i in range(0, 5) { if i % 2 == 1 { continue }; out = out.append(i) }; println(out)`, `[0, 2, 4]`, true},
		{"while", `out: List[Int] = []; for out.length() < 5 { out = out.append(out.length()); if out.length() == 3 { break } }; println(out)`, `[0, 1, 2]`, true},
		{"entry_alias", `original = range(0, 4).drop(1); out = original; for i in range(0, 3) { out = out.append(i) }; println(original, out)`, `[1, 2, 3] [1, 2, 3, 0, 1, 2]`, true},
		{"snapshot", `out: List[Int] = []; saved: List[List[Int]] = []; for i in range(0, 5) { saved = saved.append(out); out = out.append(i) }; println(saved, out)`, `[[], [0], [0, 1], [0, 1, 2], [0, 1, 2, 3]] [0, 1, 2, 3, 4]`, false},
		{"fork", `out: List[Int] = []; for i in range(0, 3) { other = out.append(99); out = out.append(i); println(other) }; println(out)`, `[99]
[0, 99]
[0, 1, 99]
[0, 1, 2]`, false},
		{"closure", `out: List[Int] = []; fs: List[() => List[Int]] = []; for i in range(0, 3) { fs = fs.append(() => out); out = out.append(i) }; println(fs.map(f => f()), out)`, `[[], [0], [0, 1]] [0, 1, 2]`, false},
		{"slice_alias", `out: List[Int] = []; saved: List[List[Int]] = []; for i in range(0, 4) { out = out.append(i); saved = saved.append(out.drop(1)) }; println(saved, out)`, `[[], [1], [1, 2], [1, 2, 3]] [0, 1, 2, 3]`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "bork.mod"), []byte("module example.com/listreuse\nunsafe \"example.com/listreuse\"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte("fn nilList(): List[Int] unsafe go { return nil }\nfn isNil(xs: List[Int]): Bool unsafe go { return xs == nil }\nfn main() uses io { "+tc.body+" }\n"), 0644); err != nil {
				t.Fatal(err)
			}
			src, err := Emit(dir)
			if err != nil {
				t.Fatal(err)
			}
			file, err := parser.ParseFile(token.NewFileSet(), "program.go", src, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := false
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "main" {
					continue
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok || len(call.Args) == 0 {
						return true
					}
					fun, ok := call.Fun.(*ast.Ident)
					if !ok || fun.Name != "append" {
						return true
					}
					recv, ok := call.Args[0].(*ast.Ident)
					if ok && strings.HasSuffix(recv.Name, "_out") {
						got = true
					}
					return true
				})
			}
			if got != tc.reuse {
				t.Errorf("out reuse = %v, want %v", got, tc.reuse)
			}
			exe := filepath.Join(t.TempDir(), "program")
			if err := Build(dir, exe); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(exe).CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if strings.TrimSpace(string(out)) != tc.want {
				t.Errorf("got %s, want %s", out, tc.want)
			}
		})
	}
}
