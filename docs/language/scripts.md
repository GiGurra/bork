# Scripts

A bork script is one `.bork` file with statements at the top level or an explicit `fn main()`. Put a shebang on the first line to make other compiler commands recognize it as a script:

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

The executable form needs `bork` on your `PATH` and an `env` that supports `-S`. You can always use `bork script hello.bork`, which also accepts a file without a shebang. Arguments after the file go to `process.Args()`, without the program name. An optional `--` after the file is accepted. Put compiler flags such as `--fast` before the file; flags after it belong to the script. The running script keeps the caller's working directory; relative file paths are relative to that directory.

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

A script may instead declare `fn main()`. Other declarations, including helpers, types, tests and pure `lazy` package values, still work. Combining `fn main()` with top-level statements is an error that names the main declaration and the first statement; move those statements into main or remove main. Scripts cannot be imported as packages. Compile each script as one root file. Explicit `lazy` package values follow the [package binding rules](basics.md): pure initializers, once on first read, checked facts, capitalization exports and cycle detection.

The other commands work on a script with a shebang too:

```sh
bork check hello.bork
bork fmt hello.bork
bork describe hello.bork:4:1
bork build hello.bork -o hello
bork test hello.bork
```

## Command-line tools

Scripts can use [bork/cli](../std/cli.md) in either entrypoint form, including typed flags, config files, subcommands and persistent root flags (`RunRoot`). The CLI APIs receive script arguments without the program name. Flags such as `--help` belong to the script, so they work through shebang execution too.

```bork
#!/usr/bin/env -S bork script
import "bork/cli"
import "bork/codec"
use codec.Defaults

type Options = { name: String = "world" } derive (codec.Decode)
fn greeting(name: String): String { s"Hello, $name!" }
fn main() {
  println(cli.Run[Options]("greet", "A greeting script", (options, s) => {
    println(greeting(options.name))
  }))
}
```

Run it with `bork script greet.bork --name Ada` or `./greet.bork --name Ada`. See the checked [top-level CLI script](../../examples/script_cli/main.bork) and [fn-main CLI script](../../examples/script_cli_main/main.bork).

## Inline dependencies

A standalone script can declare pinned Go or Bork dependencies and allow its own unsafe Go bindings in header comments before imports or declarations:

```bork
#!/usr/bin/env -S bork script
// bork:require github.com/google/uuid v1.6.0
// bork:unsafe

fn valid(text: String): Ok | GoError unsafe go "github.com/google/uuid.Validate"
println(valid("00000000-0000-0000-0000-000000000001"))
```

Each `bork:require` names a Go module, including a published Bork library, and a canonical pinned version, including pseudo-versions. Version queries such as `latest` are rejected. The first compile resolves the module graph through Go and records its manifest and verified checksums in the compiler cache. Later compiles reuse those files. `bork clean --all` removes resolved script dependency graphs too; ordinary namespace cleaning preserves them. Go's usual minimum version selection applies to transitive requirements. The script stays self-contained; no manifest or checksum file is written beside it. The first resolution may need network access, while later runs can use Go's populated module cache.

Bork libraries use ordinary module-path imports, with an alias when the final path component is not a Bork identifier:

```bork fragment
#!/usr/bin/env -S bork script
// bork:require example.com/my-library v1.0.0
import lib "example.com/my-library"
println(lib.Value())
```

The resolver pins the complete graph before loading library sources. Downloaded libraries use their own unsafe grants; `bork:unsafe` authorizes only the root script. Editor checks use an already resolved script graph and diagnose missing downloads; run the script once to populate it. Projects provide committed checksum pins when portability and reproducibility matter.

`bork:unsafe` allows unsafe Go only in this script. It does not grant effects: a helper doing I/O must still declare `uses io`.

Inline directives are errors anywhere under a `bork.mod`, including regular source files. In a project, manage dependencies with `bork deps` and grant unsafe access in `bork.mod`, as described in [calling Go](go-interop.md). There is no project allowlist for inline script directives.

## Startup and caching

Scripts use the normal [compile cache](../cli.md#the-compile-cache), enabled by default on Linux and macOS. A warm run reuses checked/generated results and the Go executable build. Changes to the script, module configuration or captured build inputs invalidate the applicable cached result. Program arguments and runtime I/O still run every time.

---

Next: [Basics](basics.md) · [All pages](../README.md#the-language)
