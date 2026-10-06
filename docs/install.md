# Installing bork

bork compiles through Go, so every installation needs Go to build programs.

## Go

Install [Go](https://go.dev/dl/) 1.21 or later and keep automatic toolchain switching on (the Go default). bork needs Go 1.26 or newer, and asks Go to download a suitable toolchain when your installed Go is older. The first install or build may then need network access; cached toolchains work offline.

With `GOTOOLCHAIN=local`, install Go 1.26 or newer yourself. See [Go toolchains](cli.md#go-toolchains) for the details.

## With Go

```sh
go install github.com/GiGurra/bork/cmd/bork@latest
bork version
```

## With Homebrew

On macOS or Linux:

```sh
brew install gigurra/tap/bork
```

The formula installs Go as a dependency. Update with `brew upgrade bork`.

## From a release archive

[GitHub Releases](https://github.com/GiGurra/bork/releases) has prebuilt archives for Linux, macOS and Windows (amd64 and arm64), with `checksums.txt` and the VS Code `.vsix`. Extract the compiler archive and put `bork` (`bork.exe` on Windows) on `PATH`. Go is still needed to compile programs.

## Upgrading

```sh
bork upgrade            # install the latest release
bork upgrade v0.4.0     # install a specific release
```

`bork upgrade` downloads a release binary, verifies its SHA-256 checksum, and installs it into `BORKBIN` (shown by `bork env BORKBIN`). Add that directory to `PATH`. It reports progress and the old and new versions; `--from-source` builds with Go instead. A Homebrew installation is detected, and the command points you to `brew upgrade bork`. See [upgrades](cli.md#upgrades).

Interactive commands can print a quiet notice, at most once a day, when a newer release exists. Turn it off with `bork env -w BORKUPDATECHECK=off`. See [update notices](cli.md#update-notices).

## Project compiler versions

A project can require a minimum compiler with a `bork 0.4` line in `bork.mod`. An older compiler then installs and runs a suitable release through Go's module proxy. Use `BORKTOOLCHAIN=local` to turn switching off, or `BORKTOOLCHAIN=v0.4.2` to pin an exact compiler. `bork version` and `bork env BORKVERSION` explain which compiler was chosen. See [compiler versions](cli.md#compiler-versions).

## Next

- [A tour of bork](tour.md) builds a first program.
- [Editors](editors.md) sets up VS Code and other editors.
