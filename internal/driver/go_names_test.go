package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// External drivers can omit Module even for user packages. Their names must
// remain specific to each load rather than entering the standard-name cache.
func TestGoNamesExternalDriver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell package driver")
	}
	dir := t.TempDir()
	driver := filepath.Join(dir, "driver")
	t.Setenv("GOPACKAGESDRIVER", driver)
	write := func(name string) {
		t.Helper()
		response := fmt.Sprintf(`{"Compiler":"gc","Arch":%q,"Roots":["example.com/drivername"],"Packages":[{"ID":"example.com/drivername","PkgPath":"example.com/drivername","Name":%q}]}`, runtime.GOARCH, name)
		source := "#!/bin/sh\ncat <<'RESPONSE'\n" + response + "\nRESPONSE\n"
		if err := os.WriteFile(driver, []byte(source), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"firstName", "secondName"} {
		write(name)
		names := (goPackages{}).Names([]string{"example.com/drivername"})
		if got := names["example.com/drivername"]; got != name {
			t.Fatalf("name = %q, want %q", got, name)
		}
	}
	// Unset selects the driver on PATH; it has the same incomplete metadata.
	automatic := filepath.Join(dir, "gopackagesdriver")
	if err := os.Rename(driver, automatic); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPACKAGESDRIVER", "")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, name := range []string{"thirdName", "fourthName"} {
		driver = automatic
		write(name)
		names := (goPackages{}).Names([]string{"example.com/drivername"})
		if got := names["example.com/drivername"]; got != name {
			t.Fatalf("automatic driver name = %q, want %q", got, name)
		}
	}
}

func TestGoNamesGOPATH(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprintf("saved=%v", saved), func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("GO111MODULE", "off")
			if saved {
				settings := filepath.Join(root, "goenv")
				if err := os.WriteFile(settings, []byte("GO111MODULE=off\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GOENV", settings)
				t.Setenv("GO111MODULE", "")
			}
			t.Setenv("GOPACKAGESDRIVER", "off")
			t.Setenv("GOPATH", root)
			dir := filepath.Join(root, "src", "example.com", "gopathname")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"firstName", "secondName"} {
				if err := os.WriteFile(filepath.Join(dir, "name.go"), []byte("package "+name+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				names := (goPackages{}).Names([]string{"example.com/gopathname"})
				if got := names["example.com/gopathname"]; got != name {
					t.Fatalf("GOPATH name = %q, want %q", got, name)
				}
			}
		})
	}
}
