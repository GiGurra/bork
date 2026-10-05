# Executable reuse

`bork run`, `bork build`, and `bork script` default to reusing an executable when
its recorded inputs have not changed. This applies to programs with foreign Go
dependencies, embedded files, assembly, cgo, and compile-time computation. A warm
hit starts neither Go nor the bork checker/evaluator and never enumerates a
directory. Run and script hand the stable executable to `exec` on Unix.

`--rebuild` or `BORKREBUILD=1` selects the exhaustive path: reload, check and
reevaluate the program, then ask Go to rebuild all packages with `-a`. This is the
way to observe changes outside the default recipe. It wins over `--fast`.

## Recorded evidence

The dependency closure is captured before building and captured again afterward;
any observed changes decline publication. The recipe is written only after a successful build. It records the request
(including working directory, script/program mode, and requested output), compiler
image identity, executable bytes and executable permissions, and a bounded set
of concrete input paths. The output identity comes from the private artifact
before atomic publication, so concurrent programs sharing `-o` cannot certify one
another's bytes. A checksum envelope detects truncated or edited recipes; these
files are disposable cache entries, not a security boundary against an actor who
can rewrite both the recipe and its checksum.

The concrete input set includes:

- Loaded bork source files, module manifests, local Go manifests/checksums, and
  failed module lookups whose later appearance would change module selection.
- The Go package graph returned by `go list -deps -json`, including pinned module
  versions and replacement manifests. Nonstandard packages record their selected
  Go, assembly, C, C++, Objective-C, Fortran, header, object and embedded files.
  Cold discovery records embed subtree directory membership, assembly includes,
  and project headers reported by the selected C frontend using Go’s effective
  compiler flags, plus explicit project include search directories.
- Original files and directories observed by bork embed requests, including empty
  directories; the staged copies are derived output.
- Compiler-supplied `bork/build` file inputs observed during compile-time code.
- The installed Go SDK identity, Go launcher selection, saved Go environment
  file, and process Go settings, including GOOS, GOARCH, GOFLAGS/tags and CGO_*.
- For cgo, CC/CXX/FC/pkg-config command selection and available compiler executable
  bytes, as well as PATH, compiler include/search environment and the cgo configuration used for the build.

Hits hash the recorded mutable files and compare directory device, inode, mode,
modification time and change time. Directory metadata invalidates package/file
selection after additions, removals or replacements; the hit does not walk the
contents again. Missing paths are checked individually. Recipes are bounded by
the cache's size and inventory limits. A miss performs normal input discovery;
unsupported or failed capture cannot authorize reuse.

Installed Go SDKs and downloaded versioned modules are treated as immutable,
as Go's own cache expects. Replacing an SDK or changing a module requirement
invalidates the recipe. Editing a file inside an installed SDK in place is outside
this contract; use `--rebuild` after such edits. Concrete nonstandard Go files are
also hashed, so ordinary local replacement-module edits invalidate even when
file size and modification time are preserved.

## External inputs and explicit acceptance

The default recipe does not track system C headers and libraries, nor external
state read secretly by foreign compile-time code (environment, clock, or files
outside the compiler's known project inputs). Those changes require `--rebuild`.
Checked pure compile-time code and `bork/build` reads use the recorded input
contract. Foreign helpers reached by compile-time recipes or predicate evaluation
carry the external-state warning conservatively; bork does not infer that an
arbitrary unsafe Go body is deterministic.

A warm reuse with such untracked inputs prints one line to stderr naming them
and offering `--rebuild` or explicit acceptance. Cold builds and forced rebuilds
do not print this reuse warning. Accept it with `--fast`, `BORKFAST=1`, or a
`fast` directive after the `module` line in `bork.mod`. A standalone script can
put `--fast` in its shebang, for example `#!/usr/bin/env -S bork script --fast`.
Programs with no untracked inputs do not warn. Acceptance changes warning policy,
not which concrete inputs are validated.

Compiler-result caching remains a separate, stricter mechanism for commands such
as `check` and `emit`; this executable contract does not relax their evaluator
bypass or semantic validation rules.
