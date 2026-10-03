package gen

import "testing"

func TestGoImportAliases(t *testing.T) {
	g := &gen{bindImports: map[string]string{}}
	seen := map[string]string{}
	for _, path := range []string{"a/b_c", "a_b/c", "a-b.c", "a_b_c", "a_2f_b", "a/b", "strconv", "example.com/é"} {
		alias := g.goImport(path)
		if previous, exists := seen[alias]; exists {
			t.Fatalf("%q and %q share import alias %q", previous, path, alias)
		}
		seen[alias] = path
		if repeated := g.goImport(path); repeated != alias || g.bindImports[path] != alias {
			t.Fatalf("unstable import alias for %q", path)
		}
	}
}
