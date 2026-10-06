package driver

import (
	"github.com/GiGurra/bork/internal/diag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageTagChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, declaration, want string }{
		{"unknown group", "type T={x:Int missing{name: \"x\"}}", "unknown tag package missing"},
		{"unknown entry", "type T={x:Int tags{typo: \"x\"}}", "has no field typo"},
		{"wrong value", "type T={x:Int tags{name: 2}}", "must be String"},
		{"repeated entry", "type T={x:Int tags{name: \"x\", name: \"y\"}}", "field name is given twice"},
		{"repeated group", "type T={x:Int tags{} tags{}}", "tag group tags is declared twice"},
		{"non-closed", "fn label():String{\"x\"}\ntype T={x:Int tags{name:label()}}", "tag values must be closed"},
		{"alias placement", "type T=Int tags{}", "only be written on record or sealed types"},
		{"constrained field", "type T={x:Int checked{count:-1}}", "checked.positive(-1) is false"},
		{"constrained alias metadata", "type T={x:Int alias{count:-1}}", "must be a non-generic record"},
		{"generic metadata", "type T={x:Int generic{}}", "must be a non-generic record"},
		{"wrong tag shape", "type T={} scalar{}", "must be a non-generic record"},
		{"missing variant support", "type T=sealed{A scalar{}}", "no exported VariantTags record"},
		{"missing required", "type T={x:Int required{}}", "is missing field(s): name"},
		{"defaults", "type T={x:Int tags{}} tags{}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sources := map[string]string{
				"bork.mod":           "module example.com/tagchecks\n",
				"tags/tags.bork":     "type FieldTags={name:String=\"default\"}\ntype TypeTags={}\n",
				"checked/tags.bork":  "pred positive(n:Int){n>0}\ntype FieldTags={count:Int where positive}\n",
				"alias/tags.bork":    "type Base={count:Int}\npred positive(x:Base){x.count>0}\ntype FieldTags=Base where positive\n",
				"generic/tags.bork":  "type FieldTags[T]={value:T}\n",
				"scalar/tags.bork":   "type TypeTags=sealed{A}\n",
				"required/tags.bork": "type FieldTags={name:String}\n",
				"main.bork":          "import \"example.com/tagchecks/tags\"\nimport \"example.com/tagchecks/generic\"\nimport \"example.com/tagchecks/scalar\"\nimport \"example.com/tagchecks/required\"\nimport \"example.com/tagchecks/alias\"\nimport \"example.com/tagchecks/checked\"\n" + tc.declaration + "\nfn main(){}\n",
			}
			// Avoid irrelevant unused imports in each focused diagnostic case.
			for _, pkg := range []string{"tags", "generic", "scalar", "required", "alias", "checked"} {
				if !strings.Contains(tc.declaration, pkg+"{") {
					sources["main.bork"] = strings.ReplaceAll(sources["main.bork"], "import \"example.com/tagchecks/"+pkg+"\"\n", "")
				}
			}
			for path, source := range sources {
				path = filepath.Join(dir, path)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(source), 0644); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := Check(dir)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestPackageTagEditorQueries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := "type FieldTags={name:String=\"x\", count:Int=1}\ntype TypeTags={}\ntype Row={value:Int main {name:\"chosen\"}}\nfn main(){}\n"
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	analysis, err := NewSession().Analyze(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	pos := diag.Pos{File: path, Line: 3, Col: strings.Index(strings.Split(source, "\n")[2], "\"chosen\"") + 1}
	result, err := analysis.Describe(pos)
	if err != nil || result.Type != "String" {
		t.Fatalf("tag value description: %+v %v", result, err)
	}
	line := strings.Split(source, "\n")[2]
	col := strings.Index(line, "main {") + len("main {") + 1
	at := diag.Pos{File: path, Line: 3, Col: col}
	completions, handled := analysis.EditorContextCompletions(path, source, at, at)
	found := false
	for _, completion := range completions {
		if completion.Name == "count" && completion.Detail == "Int" {
			found = true
		}
	}
	if !handled || !found {
		t.Fatalf("tag field completion: %+v, handled=%v", completions, handled)
	}
}
