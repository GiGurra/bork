package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForkParameterLifetimeHint(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, hint string }{
		{"atom", `fn work(s: Scope, a: Atom[Int]) uses state { _ = fork(s, () => current(a)) }`, "a: Atom[Int] in s"},
		{"callback", `fn work(s: Scope, callback: () => Int) { _ = fork(s, callback) }`, "callback: () => Int in s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "main.bork")
			source := tc.source + "\nfn main() {}\n"
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(path)
			if err == nil || !strings.Contains(err.Error(), tc.hint) || strings.Contains(err.Error(), "attach it first") {
				t.Fatalf("expected parameter lifetime hint %q, got %v", tc.hint, err)
			}
			fixed := strings.Replace(source, strings.TrimSuffix(tc.hint, " in s"), tc.hint, 1)
			if err := os.WriteFile(path, []byte(fixed), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Check(path); err != nil {
				t.Fatalf("suggested signature does not check: %s\n%v", fixed, err)
			}
		})
	}
}
