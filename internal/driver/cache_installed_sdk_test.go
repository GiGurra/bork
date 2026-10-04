package driver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheInstalledSDKReplacementInvalidates(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	tool := filepath.Join(root, "go")
	versionFile := filepath.Join(root, "VERSION")
	for _, path := range []string{tool, versionFile} {
		if err := os.WriteFile(path, []byte("go1.27.1\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	identity := captureInstalledSDK(tool, root, "go1.27.1")
	if identity == nil {
		t.Skip("platform has no persistent stat identity")
	}
	if !identity.current(tool, root, "go1.27.1") {
		t.Fatal("fresh identity missed")
	}
	if identity.current(tool, root, "go1.27.2") || identity.current(tool, root+"/different", "go1.27.1") {
		t.Fatal("changed root/version accepted")
	}
	for _, path := range []string{tool, versionFile} {
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		replacement := path + ".next"
		if err := os.WriteFile(replacement, []byte("go1.27.1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(replacement, before.ModTime(), before.ModTime()); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Fatal(err)
		}
		if identity.current(tool, root, "go1.27.1") {
			t.Fatal("equal-byte/equal-mtime replacement accepted", path)
		}
		identity = captureInstalledSDK(tool, root, "go1.27.1")
	}
	if err := os.Chtimes(versionFile, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if identity.current(tool, root, "go1.27.1") {
		t.Fatal("version timestamp change accepted")
	}
	if err := os.Remove(versionFile); err != nil {
		t.Fatal(err)
	}
	if identity.current(tool, root, "go1.27.1") {
		t.Fatal("removed version file accepted")
	}
}

func TestCacheInstalledSDKHitStillValidatesSources(t *testing.T) {
	t.Parallel()
	body, _ := cacheArtifactFixture(t)
	restore := func(receipt *goContextReceipt) (*goContext, error) {
		return receipt.restoreInstalledSDK(resolveOwnedReceiptGoContext())
	}
	restored, err := body.validateWithContext(body.Request, body.Namespace, restore)
	if err != nil {
		t.Fatal(err)
	}
	// Restored contexts own their policy evidence, independently of the receipt.
	restored.context.validation.installedSDK.Version = 0
	if body.Go.InstalledSDK.Version != 1 {
		t.Fatal("restored policy aliases receipt")
	}
	original, err := os.Stat(body.Request.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(body.Request.Path, []byte("fn main(){ let changed=1 }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(body.Request.Path, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := body.validateWithContext(body.Request, body.Namespace, restore); err == nil {
		t.Fatal("SDK policy weakened source content validation")
	}
}
