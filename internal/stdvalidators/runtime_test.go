package stdvalidators

import (
	"bytes"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"
)

func sqlCall(t *testing.T, parts []string) Call {
	t.Helper()
	binding, ok := Lookup("bork/sql", "sqlInterpolationValidator", "Builder", "(StaticParts,List[InterpolationHole])->List[InterpolationIssue]")
	if !ok {
		t.Fatal("missing standard artifact")
	}
	return Call{Binding: binding, Request: Request{Parts: parts, Holes: [][]Kind{{{Tag: "Builtin", Name: "Int"}}}}}
}

func TestRunFreshAndConcurrent(t *testing.T) {
	good := sqlCall(t, []string{"SELECT ", ""})
	bad := sqlCall(t, []string{"SELECT '", "'"})
	goodOutput, err := Run([]Call{good}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	badOutput, err := Run([]Call{bad}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(goodOutput, badOutput) || !bytes.Contains(badOutput, []byte(base64.StdEncoding.EncodeToString([]byte("SQL hole is inside quoted text or an identifier")))) {
		t.Fatal("artifact failed structural validation")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := Run([]Call{good}, time.Second)
			if err != nil || !bytes.Equal(out, goodOutput) {
				t.Errorf("concurrent artifact: %s %v", out, err)
			}
		}()
	}
	wg.Wait()
}

func TestRunLimitsAndOwnership(t *testing.T) {
	call := sqlCall(t, []string{"SELECT ", ""})
	descriptor := call.Binding.Descriptor()
	descriptor.Sources[0].Digest = "modified"
	if call.Binding.Descriptor().Sources[0].Digest == "modified" {
		t.Fatal("mutable registry exposed")
	}
	if _, err := Run([]Call{call}, 0); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("zero deadline: %v", err)
	}
	call.Request.Parts[0] = strings.Repeat("x", InputLimit+1)
	if _, err := Run([]Call{call}, time.Second); err == nil || !strings.Contains(err.Error(), "input limit") {
		t.Fatalf("input limit: %v", err)
	}
	call = sqlCall(t, []string{"SELECT ", ""})
	call.Request.Holes[0][0].Tag = "invalid"
	if _, err := Run([]Call{call}, time.Second); err == nil || !strings.Contains(err.Error(), "invalid standard validator kind") {
		t.Fatalf("panic not contained: %v", err)
	}
	call = sqlCall(t, []string{"SELECT ", ""})
	call.Binding.descriptor.Identity = "invalid"
	if _, err := Run([]Call{call}, time.Second); err == nil || !strings.Contains(err.Error(), "binding") {
		t.Fatalf("invalid binding: %v", err)
	}
}
