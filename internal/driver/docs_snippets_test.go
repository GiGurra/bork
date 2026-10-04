package driver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/format"
)

// docSnippet is one fenced bork block of a reader page.
type docSnippet struct {
	line   int    // of the opening fence
	mode   string // "", "fails", or "fragment"
	source string
}

// readerPages lists the Markdown files written for readers of the
// language: the README, and the docs pages it leads to. Contributor
// references (the grammar, requirements, and design notes) are not
// checked.
func readerPages(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	pages := []string{filepath.Join(root, "README.md")}
	for _, pattern := range []string{"README.md", "tour.md", "cli.md", "examples.md", "contributing.md", filepath.Join("language", "*.md"), filepath.Join("std", "README.md")} {
		found, err := filepath.Glob(filepath.Join(root, "docs", pattern))
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, found...)
	}
	return pages
}

// docSnippets finds the bork blocks of a Markdown text. Every fence in
// a reader page must name its language, so that bork code cannot go
// unchecked by leaving it out.
func docSnippets(text string) ([]docSnippet, error) {
	var snippets []docSnippet
	var current *docSnippet
	var body []string
	open := false
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "```") {
			if current != nil {
				body = append(body, line)
			}
			continue
		}
		if open {
			if current != nil {
				current.source = strings.Join(body, "\n") + "\n"
				snippets = append(snippets, *current)
			}
			open, current, body = false, nil, nil
			continue
		}
		open = true
		info := strings.Fields(strings.TrimPrefix(trimmed, "```"))
		if len(info) == 0 {
			return nil, fmt.Errorf("line %d: the code block names no language (use bork, sh, text, ...)", i+1)
		}
		if info[0] != "bork" {
			continue
		}
		mode := strings.Join(info[1:], " ")
		if mode != "" && mode != "fails" && mode != "fragment" {
			return nil, fmt.Errorf("line %d: unknown bork block kind %q (use fails or fragment)", i+1, mode)
		}
		current = &docSnippet{line: i + 1, mode: mode}
	}
	if open {
		return nil, errors.New("the last code block is not closed")
	}
	return snippets, nil
}

// TestDocLinks checks that the relative links of the reader pages lead
// to files that exist.
func TestDocLinks(t *testing.T) {
	t.Parallel()
	link := regexp.MustCompile(`\]\(([^)\s]+)\)`)
	for _, page := range readerPages(t) {
		text, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range link.FindAllStringSubmatch(proseOf(string(text)), -1) {
			target, _, _ := strings.Cut(match[1], "#")
			if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(page), filepath.FromSlash(target))); err != nil {
				t.Errorf("%s links to %s, which does not exist", strings.TrimPrefix(filepath.ToSlash(page), "../../"), match[1])
			}
		}
	}
}

// proseOf drops the code of a Markdown text: fenced blocks and inline
// spans, where brackets and parentheses are not links.
func proseOf(text string) string {
	var prose []string
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			prose = append(prose, inlineCode.ReplaceAllString(line, ""))
		}
	}
	return strings.Join(prose, "\n")
}

var inlineCode = regexp.MustCompile("`[^`]*`")

// TestDocSnippets keeps the bork code in the README and the reader docs
// compiling. A block opened with
//
//   - ```bork is a complete source file: it must type-check and be
//     formatted as bork fmt would write it;
//   - ```bork fails shows a mistake: it must not compile;
//   - ```bork fragment is part of a file (a lone expression or
//     signature), and is not checked.
func TestDocSnippets(t *testing.T) {
	t.Parallel()
	for _, page := range readerPages(t) {
		text, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(filepath.ToSlash(page), "../../")
		snippets, err := docSnippets(string(text))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, snippet := range snippets {
			if snippet.mode == "fragment" {
				continue
			}
			t.Run(fmt.Sprintf("%s:%d", name, snippet.line), func(t *testing.T) {
				t.Parallel()
				file := filepath.Join(t.TempDir(), "main.bork")
				if err := os.WriteFile(file, []byte(snippet.source), 0o644); err != nil {
					t.Fatal(err)
				}
				_, _, err := Check(file)
				if snippet.mode == "fails" {
					var de *DiagError
					if !errors.As(err, &de) {
						t.Fatalf("the block is marked fails, but checking it gave: %v\n%s", err, snippet.source)
					}
					return
				}
				if err != nil {
					t.Fatalf("the block does not compile:\n%v\n%s", err, snippet.source)
				}
				formatted, err := format.Source(file, []byte(snippet.source))
				if err != nil {
					t.Fatal(err)
				}
				if string(formatted) != snippet.source {
					t.Errorf("the block is not formatted as bork fmt writes it:\n--- want ---\n%s--- got ---\n%s", formatted, snippet.source)
				}
			})
		}
	}
}
