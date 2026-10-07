package format

import "testing"

func TestFormatComprehensions(t *testing.T) {
	for _, tc := range []struct{ input, want, simplified string }{
		{"xs = for {x   in [1,2] ;if x>1} yield x*2", "xs = for { x in [1, 2]; if x > 1 } yield x * 2", ""},
		{"xs = for {\n    (a,b)  in pairs\n    // keep\n    c=a+b\n  }   yield   c", "xs = for {\n    (a, b) in pairs\n    // keep\n    c = a + b\n  } yield c", ""},
		{"xs = for { x in xs; if (x > 1) } yield x", "xs = for { x in xs; if (x > 1) } yield x", "xs = for { x in xs; if x > 1 } yield x"},
		{"xs = for {\n    x in xs\n    if ((x > 1))\n  } yield x", "xs = for {\n    x in xs\n    if ((x > 1))\n  } yield x", "xs = for {\n    x in xs\n    if x > 1\n  } yield x"},
		{"xs = for { p in ps; if (p == P { x: 1 }) } yield p", "xs = for { p in ps; if (p == P { x: 1 }) } yield p", "xs = for { p in ps; if (p == P { x: 1 }) } yield p"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			for _, simplify := range []bool{false, true} {
				source := []byte("fn f() {\n  " + tc.input + "\n}\n")
				got, err := SourceWithOptions("comprehension.bork", source, Options{Simplify: simplify})
				if err != nil {
					t.Fatal(err)
				}
				want := tc.want
				if simplify && tc.simplified != "" {
					want = tc.simplified
				}
				if want := "fn f() {\n  " + want + "\n}\n"; string(got) != want {
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
