#!/usr/bin/env python3
"""Build browser assets and a matching versioned Go Wasm/runtime bundle."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
WEB = ROOT / "web" / "playground"


def build(site):
    target = site / "try"
    target.mkdir(parents=True, exist_ok=True)
    for name in ("index.html", "style.css", "app.mjs", "client.mjs"):
        shutil.copyfile(WEB / name, target / name)
    with tempfile.TemporaryDirectory(prefix="bork-playground-") as temp:
        wasm = Path(temp) / "checker.wasm"
        env = dict(os.environ, GOOS="js", GOARCH="wasm")
        subprocess.run(["go", "build", "-trimpath", "-ldflags=-s -w", "-o", str(wasm),
                        "./cmd/bork-playground"], cwd=ROOT, env=env, check=True)
        # Use exactly the SDK that go build selected, including toolchain selection.
        goroot = subprocess.check_output(["go", "env", "GOROOT"], cwd=ROOT, env=env, text=True).strip()
        runtime = Path(goroot) / "lib" / "wasm" / "wasm_exec.js"
        parts = [wasm.read_bytes(), runtime.read_bytes(), (WEB / "worker.js").read_bytes()]
        digest = hashlib.sha256(b"".join(parts)).hexdigest()[:20]
        bundle = target / "build" / digest
        bundle.mkdir(parents=True, exist_ok=True)
        for name, data in zip(("checker.wasm", "wasm_exec.js", "worker.js"), parts):
            (bundle / name).write_bytes(data)
        compressed = gzip.compress(parts[0], compresslevel=9, mtime=0)
        (bundle / "checker.wasm.gz").write_bytes(compressed)
        revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
        (target / "build" / "manifest.json").write_text(json.dumps({
            "worker": f"build/{digest}/worker.js", "revision": revision,
            "wasm_bytes": len(parts[0]), "gzip_bytes": len(compressed),
        }) + "\n")
        print(f"Playground Wasm: {len(parts[0]):,} bytes raw; {len(compressed):,} bytes gzip-9")
        print(f"Versioned compiler bundle: {digest}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--site-dir", type=Path, default=ROOT / "site")
    build(parser.parse_args().site_dir)
