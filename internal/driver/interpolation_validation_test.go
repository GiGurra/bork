package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/check"
)

const validatorBuilder = `type Builder={}
fn Tag(parts:StaticParts):Builder{Builder{}}
fn (b:Builder) Interpolate[T](value:T):Builder{b}
fn (b:Builder) Finish():Int{1}
`

func validatorFixture(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	for name, value := range map[string]string{ModFile: "module example.com/validator\nunsafe \"example.com/validator\"\n", "main.bork": source} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestInterpolationValidatorBatchAndMemo(t *testing.T) {
	source := validatorBuilder + `instance validation:InterpolationValidator[Builder]{
 fn validateInterpolation(parts:StaticParts,holes:List[InterpolationHole]):List[InterpolationIssue]{[]}
 }
 fn main(){a=Tag"same ${1}";b=Tag"same ${2}";c=Tag"different ${3}";println(a,b,c)}`
	dir := validatorFixture(t, source)
	_, info, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.InterpolationBatches) != 1 {
		t.Fatalf("batches=%d", len(info.InterpolationBatches))
	}
	batch := info.InterpolationBatches[0]
	if len(batch.Sites) != 3 || len(batch.Recipe.Body.Tail.(*check.ListLit).Elems) != 2 {
		t.Fatal("calls not memoized by parts and kinds")
	}
	session := NewSession()
	for range 2 {
		if _, err := session.Emit(dir); err != nil {
			t.Fatal(err)
		}
	}
	if stats := session.Stats(); stats.Hits != 0 || stats.Bypasses != 2 {
		t.Fatalf("uncertified evaluator results reused: %+v", stats)
	}
}

func TestInterpolationValidatorCompletedComptimeDependency(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell launcher")
	}
	counter := filepath.Join(t.TempDir(), "builds")
	source := validatorBuilder + `fn dependency():Int{comptime{7}}
 instance validation:InterpolationValidator[Builder]{
 fn validateInterpolation(parts:StaticParts,holes:List[InterpolationHole]):List[InterpolationIssue]{
 if(dependency()==7){[]}else{[InterpolationIssue{hole:Option.None,message:"missing baked value"}]}
 }}
 fn main(){println(dependency(),Tag"${1}")}`
	dir := validatorFixture(t, source)
	loaded, module, err := loadCompilationInputs(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := captureGoContext()
	launcher := filepath.Join(t.TempDir(), "go")
	// Observe evaluator builds outside the language, preserving validator purity.
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = build ]; then echo build >> '%s'; fi\nexec '%s' \"$@\"\n", counter, ctx.tool)
	if err := os.WriteFile(launcher, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx.tool = launcher
	_, err = checkLoadedProgramObserved(loaded, module, ctx, captureEmbedsSnapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	builds, err := os.ReadFile(counter)
	if err != nil || string(builds) != "build\nbuild\n" {
		t.Fatalf("completed dependency evaluated again: %q: %v", builds, err)
	}
}

func TestInterpolationValidatorLimitsAndDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"bad index", `[InterpolationIssue{hole:Option.Some{value:3},message:"bad"}]`, "returned invalid hole index 3"},
		{"panic", `panic("validator panic")`, "validator panic"},
		{"timeout", `spin();[]`, "evaluation exceeded 150ms"},
		{"message", `[InterpolationIssue{hole:Option.Some{value:0},message:"custom boundary"}]`, "custom boundary (validator example.com/validator.validation)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := validatorBuilder + `fn spin() unsafe go{for{}}
 instance validation:InterpolationValidator[Builder]{
 fn validateInterpolation(parts:StaticParts,holes:List[InterpolationHole]):List[InterpolationIssue]{` + tc.body + `}}
 fn main(){_=Tag"${42}"}`
			dir := validatorFixture(t, source)
			loaded, module, err := loadCompilationInputs(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := captureGoContext()
			ctx.evalLimit = 150 * time.Millisecond
			_, err = checkLoadedProgramObserved(loaded, module, ctx, captureEmbedsSnapshot, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestInterpolationWithoutValidatorHasNoEvaluator(t *testing.T) {
	dir := validatorFixture(t, validatorBuilder+`fn main(){println(Tag"${1}")}`)
	loaded, module, err := loadCompilationInputs(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	usage := &goUsage{}
	program, err := checkLoadedProgramTracked(loaded, module, captureGoContext(), captureEmbedsSnapshot, usage, nil)
	if err != nil {
		t.Fatal(err)
	}
	if usage.evaluator || len(program.info.InterpolationBatches) != 0 {
		t.Fatal("plain interpolator paid evaluator cost")
	}
}

func TestInterpolationValidatorOwnerAndMetadata(t *testing.T) {
	dir := validatorFixture(t, `import renamed "example.com/validator/lib"
fn main(){local=renamed.Tag;_=renamed.Tag"${1} ${renamed.Value{}}";_=local"${2} ${renamed.Value{}}"}`)
	lib := filepath.Join(dir, "lib")
	if err := os.Mkdir(lib, 0700); err != nil {
		t.Fatal(err)
	}
	source := `type Value={}
type Builder[T]={}
fn Tag(parts:StaticParts):Builder[Int]{Builder[Int]{}}
fn (b:Builder[T]) Interpolate[T,V](value:V):Builder[T]{b}
fn (b:Builder[T]) Finish[T]():Bool{true}
instance validation[T:Ord]:InterpolationValidator[Builder[T]]{
 fn validateInterpolation(parts:StaticParts,holes:List[InterpolationHole]):List[InterpolationIssue]{
 first=holes.get(0).getOr(InterpolationHole{kinds:[]})
 second=holes.get(1).getOr(InterpolationHole{kinds:[]})
 builtin=match(first.kinds.get(0).getOr(InterpolationKind.Unknown)){InterpolationKind.Builtin{name}=>name=="Int",_=>false}
 named=match(second.kinds.get(0).getOr(InterpolationKind.Unknown)){InterpolationKind.Named{packagePath,name}=>packagePath=="example.com/validator/lib"&&name=="Value",_=>false}
 if(builtin&&named){[]}else{[InterpolationIssue{hole:Option.None,message:"wrong metadata"}]}
 }
}`
	if err := os.WriteFile(filepath.Join(lib, "lib.bork"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	_, info, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.InterpolationBatches) != 1 || len(info.InterpolationBatches[0].Sites) != 2 {
		t.Fatal("qualified/local prefixes lost validator")
	}
}

func TestInterpolationValidatorPurity(t *testing.T) {
	source := validatorBuilder + `instance validation:InterpolationValidator[Builder]{
 fn validateInterpolation(parts:StaticParts,holes:List[InterpolationHole]):List[InterpolationIssue]{println("impure");[]}
 }
 fn main(){_=Tag"${1}"}`
	_, _, err := Check(validatorFixture(t, source))
	if err == nil || !strings.Contains(err.Error(), "io") {
		t.Fatalf("impure validator accepted: %v", err)
	}
}
