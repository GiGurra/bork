package driver

import (
	"os/exec"
	"testing"
)

const metadataFamilyClass = `import "bork/shape"
metadata type Show[T] = { show: (T) uses nothing => String }
class Label[T] { fn label(value: T): String }
`

// A family is one key for every target: generic and concrete queries find
// the provider of the selected dictionary.
func TestMetadataFamilyQueries(t *testing.T) {
	t.Parallel()
	source := metadataFamilyClass + `pred small(n: Int) { n < 10 }
type Small = Int where small
instance integer: Label[Int] {
 metadata Show[Int] = Show { show: value => "int " + toString(value) }
 fn label(value: Int): String { "int" }
}
instance option[A: Label]: Label[Option[A]] {
 metadata Show[Option[A]] = Show { show: value => match (value) { Option.Some(inner) => "some " + describe(inner), Option.None => "none" } }
 fn label(value: Option[A]): String { "option" }
}
derive instance record[T]: Label[T] {
 metadata Show[T] = Show { show: value => shape.name[T]() }
 fn label(value: T): String { shape.name[T]() }
}
type Point = { x: Int } derive (Label)
fn describe[T: Label](value: T): String {
 match (shape.metadata[T, Label, Show[T]]()) {
  Option.Some(provider) => provider.show(value)
  Option.None => "missing"
 }
}
fn main() {
 println(describe(3))
 println(describe(Option.Some(Option.Some(4))))
 println(describe(Point { x: 1 }))
 s: Small = 5
 println(describe[Small](s))
 println(shape.metadata[Option[Int], Label, Show[Option[Int]]]().map(provider => provider.show(Option.None)).getOr("missing"))
 println(shape.metadata[Int, Label, String]().getOr("no closed key"))
}`
	executable, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(executable).CombinedOutput()
	want := "int 3\nsome some int 4\nPoint\nint 5\nnone\nno closed key\n"
	if err != nil || string(output) != want {
		t.Fatalf("metadata family: %q, %v", output, err)
	}
}

func TestMetadataFamilyRejections(t *testing.T) {
	cases := []struct{ name, source, want string }{
		{"produces target", `metadata type Value[T] = { value: T }
fn main() {}`, "metadata family Value may use T only as a callback parameter type, found T"},
		{"returns target", `metadata type Make[T] = { make: () uses nothing => T }
fn main() {}`, "may use T only as a callback parameter type"},
		{"nested target parameter", `metadata type Each[T] = { each: (List[T]) uses nothing => String }
fn main() {}`, "may use T only as a callback parameter type"},
		{"two parameters", `metadata type Pair[A, B] = { a: (A) uses nothing => String }
fn main() {}`, "metadata family Pair must be a record or sealed type with one type parameter"},
		{"alias", `metadata type Name = String
fn main() {}`, "metadata family Name must be a record or sealed type with one type parameter"},
		{"declared for another type", metadataFamilyClass + `instance option[A]: Label[Option[A]] {
 metadata Show[A] = Show { show: value => "inner" }
 fn label(value: Option[A]): String { "option" }
}
fn main() {}`, "metadata family Show is indexed by the target: write Show[Option[A]], found Show[A]"},
		{"queried for another type", metadataFamilyClass + `fn info[A: Label](): Option[Show[Int]] { shape.metadata[A, Label, Show[Int]]() }
fn main() {}`, "metadata family Show is indexed by the target: write Show[A], found Show[Int]"},
		{"declared twice", metadataFamilyClass + `instance integer: Label[Int] {
 metadata Show[Int] = Show { show: value => "a" }
 metadata Show[Int] = Show { show: value => "b" }
 fn label(value: Int): String { "int" }
}
fn main() {}`, "metadata for Show[Int] is declared twice in instance integer"},
		{"refined argument", metadataFamilyClass + `pred small(n: Int) { n < 10 }
type Small = Int where small
instance integer: Label[Int] {
 metadata Show[Small] = Show { show: value => toString(value) }
 fn label(value: Int): String { "int" }
}
fn main() {}`, "not supported yet"},
		{"invalid family is an ordinary key", `import "bork/shape"
metadata type Pair[A, B] = { a: (A) uses nothing => String }
class Label[T] { fn label(value: T): String }
fn info[T: Label](): Option[Pair[Int, Int]] { shape.metadata[T, Label, Pair[Int, Int]]() }
fn main() {}`, "must be a record or sealed type with one type parameter"},
		{"lazy cycle", metadataFamilyClass + `instance integer: Label[Int] {
 metadata Show[Int] = Show { show: value => toString(Value) }
 fn label(value: Int): String { "int" }
}
fn info[A: Label](): Option[Show[A]] { shape.metadata[A, Label, Show[A]]() }
lazy Value: Int = { _ = info[Int](); 1 }
fn main() { println(Value) }`, "cycle"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			checkPreludeSource(t, test.source, test.want)
		})
	}
}
