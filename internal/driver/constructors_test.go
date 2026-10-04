package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDescribeGeneratedConstructor(t *testing.T) {
	t.Parallel()
	source := `pred positive(n: Int) { n > 0 }
type Config = private {
  // Request budget in bytes.
  budget: Int where positive = 2048
  name: String
} where valid
pred valid(c: Config) { c.budget >= 1024 }
fn New = Config.new
fn main() { _ = New(name: "local") }
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Describe(path+":9:17", "")
	if err != nil {
		t.Fatal(err)
	}
	callable := result.Callable
	if callable == nil || !callable.NamedArguments || !callable.ParameterNamesAreAPI || len(callable.Parameters) != 2 {
		t.Fatalf("missing generated callable: %+v", callable)
	}
	budget := callable.Parameters[0]
	if budget.Name != "budget" || budget.Type != "Int" || budget.Default != "2048" || budget.Doc != "Request budget in bytes." {
		t.Fatalf("missing field metadata: %+v", budget)
	}
	if callable.Parameters[1].Name != "name" || callable.Parameters[1].Default != "" {
		t.Fatalf("missing required field: %+v", callable.Parameters[1])
	}
	if got := strings.Join(callable.Requires, "; "); got != "completed Config requires valid; budget requires positive" {
		t.Fatalf("unexpected requirements: %s", got)
	}
}
