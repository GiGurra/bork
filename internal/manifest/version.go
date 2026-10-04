// Package manifest reads compiler requirements without loading program sources.
package manifest

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

type Version struct {
	Query   string // Go module query; a minor version selects its latest patch.
	Minimum string // Full semantic version used to compare compiler requirements.
}

func ParseVersion(text string) (Version, error) {
	query := "v" + strings.TrimPrefix(text, "v")
	if !semver.IsValid(query) || strings.Count(strings.SplitN(query, "-", 2)[0], ".") < 1 || strings.Contains(query, "+") {
		return Version{}, fmt.Errorf("invalid bork version %q; expected 0.4 or 0.4.2", text)
	}
	return Version{Query: query, Minimum: semver.Canonical(query)}, nil
}

// CompilerVersion ignores unrelated directives so startup dispatch can select
// a newer compiler before that compiler parses newer manifest syntax.
func CompilerVersion(data []byte) (Version, error) {
	var version Version
	for index, line := range strings.Split(string(data), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "bork" {
			continue
		}
		if len(fields) != 2 || version.Query != "" {
			return Version{}, fmt.Errorf("line %d: expected a single bork <version> directive", index+1)
		}
		var err error
		version, err = ParseVersion(fields[1])
		if err != nil {
			return Version{}, fmt.Errorf("line %d: %w", index+1, err)
		}
	}
	return version, nil
}
