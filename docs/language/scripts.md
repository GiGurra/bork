# Scripts

A bork script is one `.bork` file with statements at the top level. Put a shebang on the first line to make other compiler commands recognize it as a script:

```bork
#!/usr/bin/env -S bork script
import "bork/process"

args = process.Args()
name = if (notEmpty(args)) { args.first() } else { "world" }
println(s"Hello, $name!")
```

Save this as `hello.bork`, then run it:

```sh
bork script hello.bork -- Ada
chmod +x hello.bork
./hello.bork Ada
```

The executable form needs `bork` on your `PATH` and an `env` that supports `-S`. You can always use `bork script hello.bork`, which also accepts a file without a shebang. Arguments after `--` go to `process.Args()`, without the program name. The running script keeps the caller's working directory; relative file paths are relative to that directory.

## Bindings and declarations

Statements execute in source order in an implicit `main`. Top-level `x = expr` and `x: T = expr` are eager locals in that main, so they may do I/O and other effects. Types, helper functions, predicates, rules, instances and tests remain declarations. Helpers declare their effects with `uses`, as in ordinary programs. The script entrypoint has the same effect permissions as `fn main()`.

In a regular source file, top-level `x = expr` declares a pure, memoized package value. In a script it declares a main local. A helper function cannot capture a script local: use a parameter, or `lazy x = expr` for a shared pure package value.

```bork
#!/usr/bin/env -S bork script
lazy Greeting = "hello"

fn greet(name: String): String { s"$Greeting, $name!" }
name = "Ada"
println(greet(name))
```

A script cannot also declare `fn main()`, and scripts cannot be imported as packages. Compile each script as one root file. Explicit `lazy` package values follow the [package binding rules](basics.md): pure initializers, once on first read, checked facts, capitalization exports and cycle detection.

The other commands work on a script with a shebang too:

```sh
bork check hello.bork
bork fmt hello.bork
bork describe hello.bork:4:1
bork build hello.bork -o hello
bork test hello.bork
```

## Inline Go dependencies

A standalone script can declare pinned Go dependencies and allow its own unsafe Go bindings in header comments before imports or declarations:

```bork
#!/usr/bin/env -S bork script
// bork:require github.com/google/uuid v1.6.0
// bork:unsafe

fn valid(text: String): Ok | GoError unsafe go "github.com/google/uuid.Validate"
println(valid("00000000-0000-0000-0000-000000000001"))
```

Each `bork:require` names a Go module and a canonical pinned version, including pseudo-versions. Version queries such as `latest` are rejected. The first compile resolves the module graph through Go and records its manifest and verified checksums in the compiler cache. Later compiles reuse those files. Go's usual minimum version selection applies to transitive requirements. The script stays self-contained; no manifest or checksum file is written beside it. The first resolution may need network access, while later runs can use Go's populated module cache.

`bork:unsafe` allows unsafe Go only in this script. It does not grant effects: a helper doing I/O must still declare `uses io`.

Inline directives are errors anywhere under a `bork.mod`, including regular source files. In a project, manage dependencies with `bork deps` and grant unsafe access in `bork.mod`, as described in [calling Go](go-interop.md). There is no project allowlist for inline script directives.

## Startup and caching

Scripts use the normal [compile cache](../cli.md#the-compile-cache), enabled by default on Linux and macOS. A warm run reuses checked/generated results and the Go executable build. Changes to the script, module configuration or captured build inputs invalidate the applicable cached result. Program arguments and runtime I/O still run every time.
