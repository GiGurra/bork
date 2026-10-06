package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDerivePlanSessionWarmAfterRuntimeEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	prefix := `import "bork/shape"
class Labels[T] { fn labels(x: T): List[String] }
derive instance labels[T]: Labels[T] {
 fn labels(x: T): List[String] {
  [comptime for (field in shape.fields[T]()) field.name]
 }
}
type Row = { first: Int, second: String } derive(Labels)
`
	write := func(main string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(prefix+main), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cacheRoot := t.TempDir()
	newSession := func() *Session {
		return &Session{plans: &derivePlanCache{entries: map[string][]byte{}, root: cacheRoot}}
	}
	write(`fn main() { println(labels(Row { first: 1, second: "a" })) }`)
	session := newSession()
	if _, err := session.Emit(dir); err != nil {
		t.Fatal(err)
	}
	cold := session.Stats()
	if cold.DerivePlanMisses == 0 || cold.DerivePlanHits != 0 {
		t.Fatalf("expected a cold expansion, got %+v", cold)
	}
	write(`fn main() { println(labels(Row { first: 2, second: "b" })) }`)
	warmSource, err := session.Emit(dir)
	if err != nil {
		t.Fatal(err)
	}
	warm := session.Stats()
	if warm.DerivePlanHits <= cold.DerivePlanHits || warm.ProofMisses != cold.ProofMisses {
		t.Fatalf("expected expansion reuse without a new evaluator batch: cold=%+v warm=%+v", cold, warm)
	}
	// A fresh session recovers the immutable plan from the content store.
	other := newSession()
	contentSource, err := other.Emit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other.Stats().DerivePlanHits == 0 || !bytes.Equal(warmSource, contentSource) {
		t.Fatalf("content replay differs or missed: %+v", other.Stats())
	}
}

func TestDerivePlanReplayRefreshesTemplatePositions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	template := `class Labels[T] { fn labels(x: T): List[String] }
derive instance labels[T]: Labels[T] {
 fn labels(x: T): List[String] {
  names = [comptime for (field in shape.fields[T]()) field.name]
  assert(!names.isEmpty())
  names
 }
}
type Row = { value: Int } derive(Labels)
`
	write := func(main string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("import \"bork/shape\"\n"+main+"\n"+template), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`fn main() { println(labels(Row { value: 1 })) }`)
	session := &Session{plans: &derivePlanCache{entries: map[string][]byte{}}}
	if _, err := session.Emit(dir); err != nil {
		t.Fatal(err)
	}
	write("fn main() {\n println(labels(Row { value: 2 }))\n}\n")
	warm, err := session.Emit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if session.Stats().DerivePlanHits == 0 {
		t.Fatalf("expected shifted-template cache hit: %+v", session.Stats())
	}
	fresh := &Session{plans: &derivePlanCache{entries: map[string][]byte{}}}
	cold, err := fresh.Emit(dir)
	if err != nil || !bytes.Equal(warm, cold) {
		t.Fatalf("replayed source positions differ from cold expansion: %v", err)
	}
}

func TestDerivePlanInvalidatesShapeAndCandidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := `import "bork/shape"
class Labels[T] { fn labels(x: T): List[String] }
derive instance labels[T]: Labels[T] {
 fn labels(x: T): List[String] { [comptime for (field in shape.fields[T]()) field.name] }
}
type Row = { first: Int, second: String } derive(Labels)
fn main() {}`
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(source)
	session := &Session{plans: &derivePlanCache{entries: map[string][]byte{}}}
	if _, err := session.Emit(dir); err != nil {
		t.Fatal(err)
	}
	first := session.Stats()
	write(strings.Replace(source, "first: Int, second: String", "second: String, first: Int", 1))
	if _, err := session.Emit(dir); err != nil {
		t.Fatal(err)
	}
	second := session.Stats()
	if second.DerivePlanMisses <= first.DerivePlanMisses || second.DerivePlanHits != first.DerivePlanHits {
		t.Fatalf("field-order edit reused stale metadata: before=%+v after=%+v", first, second)
	}
	// Adding a visible competing dictionary changes the inventory even when
	// this particular Labels template does not require that dictionary.
	write(source + "\nclass Extra[T] { fn extra(x: T): Int }\ninstance extraInt: Extra[Int] { fn extra(x: Int): Int { 0 } }\n")
	if _, err := session.Emit(dir); err != nil {
		t.Fatal(err)
	}
	if after := session.Stats(); after.DerivePlanMisses <= second.DerivePlanMisses || after.DerivePlanHits != second.DerivePlanHits {
		t.Fatalf("candidate edit reused old inventory: before=%+v after=%+v", second, after)
	}
}

func TestDerivePlanFailureDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := `import "bork/shape"
class Labels[T] { fn labels(x: T): List[String] }
derive instance labels[T]: Labels[T] {
 fn labels(x: T): List[String] { [comptime for (field in shape.fields[T]()) field.name] }
}
type Row = { value: Int } derive(Labels)
pred positive(n: Int) { n > 0 }
fn required(n: Int where positive): Int { n }
fn main() { println(required(-1)) }`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	session := &Session{plans: &derivePlanCache{entries: map[string][]byte{}, root: t.TempDir()}}
	if _, err := session.Emit(dir); err == nil {
		t.Fatal("expected predicate failure after expansion")
	}
	if len(session.plans.entries) != 0 {
		t.Fatal("failed program published an expansion plan")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(source, "required(-1)", "required(1)", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Emit(dir); err != nil {
		t.Fatal(err)
	}
	if stats := session.Stats(); stats.DerivePlanHits != 0 || stats.DerivePlanMisses == 0 {
		t.Fatalf("failed plan entered the cache: %+v", stats)
	}
}

func TestDerivePlanDeclinesAliasedFieldTypeActions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := `import "bork/shape"
class Labels[T] { fn labels(x: T): List[String] }
derive instance labels[T]: Labels[T] {
 fn labels(x: T): List[String] {
  comptime for (field in shape.fields[T]()) {
   typ = field.Type
  }
  ["ok"]
 }
}
type Row = { value: Int } derive(Labels)
fn main() { println(labels(Row { value: 1 })) }`
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(source)
	session := &Session{plans: &derivePlanCache{entries: map[string][]byte{}}}
	if _, err := session.Emit(dir); err != nil {
		t.Fatal(err)
	}
	write(strings.Replace(source, "value: 1", "value: 2", 1))
	warm, err := session.Emit(dir)
	if err != nil {
		t.Fatal(err)
	}
	cold, err := (&Session{plans: &derivePlanCache{entries: map[string][]byte{}}}).Emit(dir)
	if err != nil || !bytes.Equal(warm, cold) {
		t.Fatalf("aliased type action replay differs: %v", err)
	}
	if stats := session.Stats(); stats.DerivePlanHits != 0 || stats.DerivePlanDeclines == 0 {
		t.Fatalf("field type aliases must decline until action replay is supported: %+v", stats)
	}
	if len(session.plans.entries) != 0 {
		t.Fatal("field type action entered the syntax-only cache")
	}
}

func TestDerivePlanReplayWithSeparatedSourceEndpoints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := `import "bork/shape"
class Labels[T] { fn labels(x: T): List[String] }
fn accept(x: String): String { x }
derive instance labels[T]: Labels[T] {
 fn labels(x: T): List[String] {
  names = [comptime for (field in shape.fields[T]()) field.name]
  first = accept("x")
  _ = first
  assert(!names.isEmpty())
  names
 }
}
type Row = { value: Int } derive(Labels)
fn main() { println(1) }`
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(source)
	session := &Session{plans: &derivePlanCache{entries: map[string][]byte{}}}
	if _, err := session.Emit(dir); err != nil {
		t.Fatal(err)
	}
	write(strings.Replace(strings.Replace(source, "accept(\"x\")", "accept (\"x\")", 1), "println(1)", "println(2)", 1))
	warm, err := session.Emit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if session.Stats().DerivePlanHits == 0 {
		t.Fatalf("whitespace changed normalized plan identity: %+v, entries %d", session.Stats(), len(session.plans.entries))
	}
	cold, err := (&Session{plans: &derivePlanCache{entries: map[string][]byte{}}}).Emit(dir)
	if err != nil || !bytes.Equal(warm, cold) {
		t.Fatalf("source endpoints differ from cold expansion: %v", err)
	}
}
