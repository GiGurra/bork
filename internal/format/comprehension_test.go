package format

import "testing"

func TestFormatComprehensions(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"xs = for {x   in [1,2] ;if x>1} yield x*2", "xs = for { x in [1, 2]; if x > 1 } yield x * 2"},
		{"xs = for {\n    (a,b)  in pairs\n    // keep\n    c=a+b\n  }   yield   c", "xs = for {\n    (a, b) in pairs\n    // keep\n    c = a + b\n  } yield c"},
		{"xs = for { x in xs; if (x > 1) } yield x", "xs = for { x in xs; if (x > 1) } yield x"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			for _, simplify := range []bool{false, true} {
				source := []byte("fn f() {\n  " + tc.input + "\n}\n")
				got, err := SourceWithOptions("comprehension.bork", source, Options{Simplify: simplify})
				if err != nil {
					t.Fatal(err)
				}
				if want := "fn f() {\n  " + tc.want + "\n}\n"; string(got) != want {
					t.Fatalf("simplify=%v: got %q; want %q", simplify, got, want)
				}
				again, err := SourceWithOptions("comprehension.bork", got, Options{Simplify: simplify})
				if err != nil || string(again) != string(got) {
					t.Fatalf("simplify=%v: not idempotent: %q, %v", simplify, again, err)
				}
			}
		})
	}
}
