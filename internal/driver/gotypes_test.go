package driver

import (
	"strings"
	"testing"
)

func TestGoBindingsReject32BitTarget(t *testing.T) {
	t.Setenv("GOARCH", "386")
	pkgs, errs := (goPackages{}).Load([]string{"strings"})
	if len(pkgs) != 0 || errs["strings"] == nil || !strings.Contains(errs["strings"].Error(), "64-bit Go target") {
		t.Fatalf("32-bit target must not use 64-bit integer conversions: packages=%v, errors=%v", pkgs, errs)
	}
}
