#!/usr/bin/env python3
"""Version, package and describe compiler releases without publishing them."""
import argparse
import hashlib
from pathlib import Path
import re
import subprocess
import tarfile
import zipfile


VERSION = re.compile(r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)\Z")
PLATFORMS = [(system, arch) for system in ("linux", "darwin", "windows")
             for arch in ("amd64", "arm64")]


def version_tuple(version):
    match = VERSION.fullmatch(version)
    if not match:
        raise ValueError(f"expected a stable vX.Y.Z release version, got {version!r}")
    return tuple(map(int, match.groups()))


def latest_version(tags):
    stable = [tag for tag in tags if VERSION.fullmatch(tag)]
    return max(stable, key=version_tuple) if stable else "v0.0.0"


def next_version(tags):
    latest = version_tuple(latest_version(tags))
    return f"v{latest[0]}.{latest[1]}.{latest[2] + 1}"


def version_for_source(history, tags, first_release):
    """Reserve patch numbers in main ancestry order, regardless of CI order."""
    indices = {commit: index for index, commit in enumerate(history)}
    source = history[-1]
    existing = [tag for tag, commit in tags.items()
                if commit == source and VERSION.fullmatch(tag)]
    if existing:
        return latest_version(existing)
    ancestors = [tag for tag, commit in tags.items()
                 if commit in indices and VERSION.fullmatch(tag)]
    baseline = latest_version(ancestors)
    start = indices[first_release]
    if ancestors:
        anchor = indices[tags[baseline]]
        distance = len(history) - 1 - anchor if anchor >= start else len(history) - start
    else:
        distance = len(history) - start
    major, minor, patch = version_tuple(baseline)
    return f"v{major}.{minor}.{patch + distance}"


def commit_version(source, cwd=None):
    def git(*args):
        return subprocess.check_output(["git", *args], cwd=cwd, text=True).splitlines()
    history = git("log", "--first-parent", "--reverse", "--format=%H", source)
    introductions = git("log", "--first-parent", "--reverse", "--diff-filter=A", "--format=%H", source, "--", "scripts/release.py")
    if not introductions:
        raise ValueError("commit predates compiler release automation")
    tags = {}
    for line in git("for-each-ref", "--format=%(refname:strip=2) %(objectname) %(*objectname)", "refs/tags"):
        fields = line.split()
        tags[fields[0]] = fields[-1]
    return version_for_source(history, tags, introductions[0])


def archive_name(version, system, arch):
    version_tuple(version)
    if (system, arch) not in PLATFORMS:
        raise ValueError(f"unsupported platform {system}/{arch}")
    extension = "zip" if system == "windows" else "tar.gz"
    return f"bork_{version}_{system}_{arch}.{extension}"


def archive(binary, version, system, arch, output):
    output.mkdir(parents=True, exist_ok=True)
    destination = output / archive_name(version, system, arch)
    name = "bork.exe" if system == "windows" else "bork"
    files = [(binary, name), (Path("LICENSE"), "LICENSE"), (Path("README.md"), "README.md")]
    if system == "windows":
        with zipfile.ZipFile(destination, "w", zipfile.ZIP_DEFLATED) as package:
            for source, target in files:
                package.write(source, target)
    else:
        with tarfile.open(destination, "w:gz") as package:
            for source, target in files:
                package.add(source, arcname=target)
    return destination


def checksums(directory, version):
    expected = {archive_name(version, system, arch) for system, arch in PLATFORMS}
    assets = [path for path in directory.iterdir() if path.is_file()
              and (path.name in expected or path.suffix == ".vsix")]
    actual = {path.name for path in assets}
    if not expected.issubset(actual) or len([p for p in assets if p.suffix == ".vsix"]) != 1:
        raise ValueError("release must contain all six compiler archives and exactly one VSIX")
    contents = "".join(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n"
                       for path in sorted(assets))
    (directory / "checksums.txt").write_text(contents)
    return contents


def formula(version, checksum):
    version_tuple(version)
    if not re.fullmatch(r"[a-f0-9]{64}", checksum):
        raise ValueError("expected a SHA-256 source archive checksum")
    return f'''class Bork < Formula
  desc "A pragmatic backend language of guarantees, compiled to Go"
  homepage "https://gigurra.github.io/bork/"
  url "https://github.com/GiGurra/bork/archive/refs/tags/{version}.tar.gz"
  sha256 "{checksum}"
  license "MIT"

  # The installed compiler invokes Go when building programs.
  depends_on "go"

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.releaseVersion=v#{{version}}"), "./cmd/bork"
  end

  test do
    assert_match "bork v#{{version}}", shell_output("#{{bin}}/bork version")
    (testpath/"hello.bork").write('fn main() {{ println("hello") }}')
    assert_equal "hello\\n", shell_output("#{{bin}}/bork run hello.bork")
  end
end
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    version = commands.add_parser("version")
    version.add_argument("tags", nargs="*")
    latest = commands.add_parser("latest")
    latest.add_argument("tags", nargs="*")
    commit = commands.add_parser("commit-version")
    commit.add_argument("--sha", required=True)
    package = commands.add_parser("archive")
    package.add_argument("--binary", type=Path, required=True)
    package.add_argument("--version", required=True)
    package.add_argument("--os", required=True)
    package.add_argument("--arch", required=True)
    package.add_argument("--output", type=Path, default=Path("dist"))
    hashes = commands.add_parser("checksums")
    hashes.add_argument("--version", required=True)
    hashes.add_argument("directory", type=Path)
    brew = commands.add_parser("formula")
    brew.add_argument("--version", required=True)
    brew.add_argument("--sha256", required=True)
    args = parser.parse_args()
    if args.command == "version":
        print(next_version(args.tags))
    elif args.command == "latest":
        print(latest_version(args.tags))
    elif args.command == "commit-version":
        print(commit_version(args.sha))
    elif args.command == "archive":
        print(archive(args.binary, args.version, args.os, args.arch, args.output))
    elif args.command == "checksums":
        print(checksums(args.directory, args.version), end="")
    else:
        print(formula(args.version, args.sha256), end="")


if __name__ == "__main__":
    main()
