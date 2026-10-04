"""Release asset contracts, version ordering, and generated tap metadata."""
import hashlib
from pathlib import Path
import tarfile
import tempfile
import unittest
import zipfile

import release


class ReleaseTests(unittest.TestCase):
    def test_patch_versions_ignore_other_tags_and_sort_numerically(self):
        self.assertEqual(release.next_version([]), "v0.0.1")
        self.assertEqual(release.next_version(["vscode-v0.1.0", "v0.9.50", "v0.10.9", "v0.11.0-rc.1"]), "v0.10.10")
        self.assertEqual(release.latest_version(["v1.0.0", "v0.99.99"]), "v1.0.0")
        for version in ("v0.4", "v01.4.0", "../v0.4.2", "v0.4.2\n", "v0.4.2-rc.1"):
            with self.assertRaises(ValueError):
                release.version_tuple(version)

    def test_archives_have_flat_platform_binary_and_license(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "binary"
            binary.write_bytes(b"fixture")
            binary.chmod(0o755)
            for system, arch in release.PLATFORMS:
                package = release.archive(binary, "v0.4.2", system, arch, root / "dist")
                if system == "windows":
                    with zipfile.ZipFile(package) as archive:
                        self.assertEqual(set(archive.namelist()), {"bork.exe", "LICENSE", "README.md"})
                        self.assertEqual(archive.read("bork.exe"), b"fixture")
                else:
                    with tarfile.open(package) as archive:
                        self.assertEqual(set(archive.getnames()), {"bork", "LICENSE", "README.md"})
                        self.assertEqual(archive.extractfile("bork").read(), b"fixture")
                        self.assertEqual(archive.getmember("bork").mode & 0o111, 0o111)

    def test_checksums_require_every_platform_and_one_vsix(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for system, arch in release.PLATFORMS:
                (root / release.archive_name("v0.4.2", system, arch)).write_bytes(b"archive")
            with self.assertRaises(ValueError):
                release.checksums(root, "v0.4.2")
            (root / "bork-0.1.0.vsix").write_bytes(b"extension")
            hashes = release.checksums(root, "v0.4.2")
            self.assertEqual(len(hashes.splitlines()), 7)
            self.assertIn(hashlib.sha256(b"extension").hexdigest() + "  bork-0.1.0.vsix", hashes)
            (root / "other.vsix").write_bytes(b"extra")
            with self.assertRaises(ValueError):
                release.checksums(root, "v0.4.2")

    def test_formula_includes_runtime_go_and_release_version(self):
        formula = release.formula("v0.4.2", "a" * 64)
        self.assertIn('depends_on "go"\n', formula)
        self.assertNotIn('=> :build', formula)
        self.assertIn('/tags/v0.4.2.tar.gz', formula)
        self.assertIn('main.releaseVersion=v#{version}', formula)
        self.assertIn('bork run hello.bork', formula)
        with self.assertRaises(ValueError):
            release.formula("v0.4.2", '" malicious')


if __name__ == "__main__":
    unittest.main()
