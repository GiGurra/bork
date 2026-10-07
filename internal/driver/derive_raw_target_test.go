package driver

import (
	"os/exec"
	"testing"
)

func TestDeriveRawTarget(t *testing.T) {
	t.Parallel()
	source := `import "bork/shape"
type Base={number:Int}
pred positive(value:Base){value.number>0}
type Small=Base where positive
metadata type Show[T]={show:(T) uses nothing=>String}
class Label[T]{fn label(value:T):String}
derive fn render[T](value:T):String{parts:List[String]=[comptime for(field in shape.fields[T]()) toString(field.read(value))];parts.join(",")}
derive instance labels[T]:Label[T]{
 metadata Show[T]=Show{show:value=>render[T.RawType](value)}
 fn label(value:T):String{render[T.RawType](value)}
}
derive Label for Small
fn main(){match(shape.metadata[Small,Label,Show[Base]]()){Option.Some(provider)=>println(provider.show(Base{number:-1})),Option.None=>println("missing")}}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(out) != "-1\n" {
		t.Fatalf("raw callback: %s, %v", out, err)
	}
}

func TestDeriveRawTargetDoesNotProveFacts(t *testing.T) {
	t.Parallel()
	const prefix = `type Base={number:Int}
pred positive(value:Base){value.number>0}
type Small=Base where positive
class Probe[T]{fn probe():String}
fn need(value:Small):String{toString(value.number)}
`
	for _, tc := range []struct{ name, body, want string }{
		{"consumer", `derive fn consume[T](value:T.RawType):String{need(value)}
derive instance probe[T]:Probe[T]{fn probe():String{consume[T](Base{number:-1})}}
derive Probe for Small`, "requires value to be positive"},
		{"result", `derive fn unchecked[T](value:T.RawType):T{value}
derive instance probe[T]:Probe[T]{fn probe():String{need(unchecked[T](Base{number:-1}))}}
derive Probe for Small`, "positive"},
		{"annotation", `derive instance probe[T]:Probe[T]{fn probe():String{raw:T.RawType=Base{number:-1};need(raw)}}
derive Probe for Small`, "requires value to be positive"},
		{"unknown target", `derive fn unused[T](value:Missing.RawType):String{"unused"}`, "undefined type in derive definition: Missing.RawType"},
		{"type arguments", `derive fn unused[T](value:T.RawType[Int]):String{"unused"}`, "a derive target reference cannot have type arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) { checkPreludeSource(t, prefix+tc.body+"\nfn main(){}", tc.want) })
	}
}

func TestDeriveRawTargetDictionarySelection(t *testing.T) {
	t.Parallel()
	source := `type Base={number:Int}
pred positive(value:Base){value.number>0}
type Small=Base where positive
class Choice[T]{fn make():T;fn keep(value:T):T}
instance plain:Choice[Base]{fn make():Base{Base{number:-1}};fn keep(value:Base):Base{value}}
instance constrained:Choice[Small]{fn make():Small{Base{number:7}};fn keep(value:Base):Small{Base{number:7}}}
class Probe[T]{fn probe():String}
derive fn raw[T]():String{a=make[T.RawType]();b=keep[T.RawType](a);s"$a,$b"}
derive instance probe[T]:Probe[T]{fn probe():String{raw[T]()}}
derive Probe for Small
fn main(){println(probe[Small]())}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(out) != "Base { number: -1 },Base { number: -1 }\n" {
		t.Fatalf("raw dictionary selection: %s, %v", out, err)
	}
}
