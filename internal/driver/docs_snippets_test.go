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

// docBlock is one fenced code block of a Markdown page.
type docBlock struct {
	line   int    // of the opening fence
	lang   string // "bork", "sh", "text", ...
	mode   string // of a bork block: "", "fails", or "fragment"
	source string
}

// docPage is a Markdown page split into its prose and its code blocks.
type docPage struct {
	prose  []string // the lines outside code blocks, without inline code
	blocks []docBlock
}

// docLanguages are the languages a code block of a reader page may name.
var docLanguages = map[string]bool{"bork": true, "sh": true, "text": true, "json": true}

var (
	inlineCode  = regexp.MustCompile("<code>.*?</code>|`+[^`]*`+")
	inlineLink  = regexp.MustCompile(`\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	otherLink   = regexp.MustCompile(`\]\[|^\s*\[[^\]]+\]:\s|<a\s`)
	docHeading  = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	anchorDrops = regexp.MustCompile(`[^\p{L}\p{N} _-]`)
)

// readerPages lists the Markdown files written for readers of the
// language: the README, and the docs pages it leads to. Their bork code
// is compiled. Contributor references (the grammar, requirements, and
// design notes) hold fragments by design, and are not.
func readerPages(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	pages := []string{filepath.Join(root, "README.md")}
	for _, name := range []string{"README.md", "tour.md", "cli.md", "playground.md", "examples.md", "contributing.md"} {
		pages = append(pages, filepath.Join(root, "docs", name))
	}
	for _, dir := range []string{"language", "std"} {
		found, err := filepath.Glob(filepath.Join(root, "docs", dir, "*.md"))
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, found...)
	}
	return pages
}

// parseDocPage splits a Markdown text. With strict set, it accepts only
// the code block forms the snippet test understands, so that bork code
// cannot go unchecked by being written another way: every block is
// fenced with three backticks at the start of a line and names one of
// docLanguages.
func parseDocPage(text string, strict bool) (docPage, error) {
	var page docPage
	var current *docBlock
	var body []string
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		fence := strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
		switch {
		case current != nil && trimmed == "```":
			current.source = strings.Join(body, "\n") + "\n"
			page.blocks = append(page.blocks, *current)
			current, body = nil, nil
		case current != nil:
			body = append(body, line)
		case fence && !strict:
			// A block of a page that is only checked for links: skip to
			// the fence that closes it.
			current = &docBlock{line: i + 1}
		case fence:
			info := strings.Fields(strings.TrimPrefix(line, "```"))
			if !strings.HasPrefix(line, "```") || strings.HasPrefix(line, "````") || len(info) == 0 || !docLanguages[info[0]] {
				return page, fmt.Errorf("line %d: a code block must open with three backticks at the start of the line and one of the languages bork, sh, text, json", i+1)
			}
			mode := strings.Join(info[1:], " ")
			if info[0] != "bork" && mode != "" || mode != "" && mode != "fails" && mode != "fragment" {
				return page, fmt.Errorf("line %d: unknown code block kind %q (a bork block may be marked fails or fragment)", i+1, mode)
			}
			current = &docBlock{line: i + 1, lang: info[0], mode: mode}
		default:
			if strict && strings.HasPrefix(line, "    ") && len(page.prose) > 0 && strings.TrimSpace(page.prose[len(page.prose)-1]) == "" {
				return page, fmt.Errorf("line %d: indented code blocks are not checked; use a fenced block", i+1)
			}
			page.prose = append(page.prose, inlineCode.ReplaceAllString(line, ""))
		}
	}
	if current != nil {
		return page, errors.New("the last code block is not closed")
	}
	return page, nil
}

// docAnchors gives the anchors GitHub makes for a page's headings.
func docAnchors(page docPage) map[string]bool {
	anchors := map[string]bool{}
	for _, line := range page.prose {
		if match := docHeading.FindStringSubmatch(line); match != nil {
			// Inline code was dropped from the prose; anchors keep its text.
			anchors[docAnchor(match[1])] = true
		}
	}
	return anchors
}

func docAnchor(heading string) string {
	text := strings.ToLower(strings.TrimSpace(heading))
	return strings.ReplaceAll(anchorDrops.ReplaceAllString(text, ""), " ", "-")
}

// TestDocLinks checks the relative links of every Markdown page of the
// README, docs, and examples: the file must exist and, for a link to a
// heading of a Markdown page, so must the heading.
func TestDocLinks(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	pages := []string{filepath.Join(root, "README.md")}
	for _, dir := range []string{"docs", "examples"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && filepath.Ext(path) == ".md" {
				pages = append(pages, path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Headings keep the text of their inline code in anchors, so the
	// anchors are read from the page with code spans unwrapped.
	anchorsOf := func(path string) (map[string]bool, error) {
		text, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		page, err := parseDocPage(strings.ReplaceAll(string(text), "`", ""), false)
		return docAnchors(page), err
	}
	checked := 0
	for _, path := range pages {
		name := strings.TrimPrefix(filepath.ToSlash(path), "../../")
		text, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		page, err := parseDocPage(string(text), false)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, line := range page.prose {
			if otherLink.MatchString(line) {
				t.Errorf("%s: reference-style and HTML links are not checked; write [text](target): %s", name, strings.TrimSpace(line))
			}
			for _, match := range inlineLink.FindAllStringSubmatch(line, -1) {
				target, anchor, _ := strings.Cut(match[1], "#")
				if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				checked++
				file := path
				if target != "" {
					file = filepath.Join(filepath.Dir(path), filepath.FromSlash(target))
				}
				info, err := os.Stat(file)
				if err != nil {
					t.Errorf("%s links to %s, which does not exist", name, match[1])
					continue
				}
				if anchor == "" || info.IsDir() || filepath.Ext(file) != ".md" {
					continue
				}
				anchors, err := anchorsOf(file)
				if err != nil {
					t.Fatal(err)
				}
				if !anchors[anchor] {
					t.Errorf("%s links to %s, but that page has no heading with the anchor #%s", name, match[1], anchor)
				}
			}
		}
	}
	if checked < 100 {
		t.Errorf("only %d links were found; the link pattern no longer matches the pages", checked)
	}
}

// TestDocSnippets keeps the bork code in the README and the reader docs
// compiling. A block opened with
//
//   - ```bork is a complete source file: it must type-check and be
//     formatted as bork fmt would write it;
//   - ```bork fails shows a mistake: it must not compile. If a ```text
//     block follows it, that block quotes the compiler: each of its
//     lines must appear in the errors;
//   - ```bork fragment is part of a file (a lone expression, or one
//     file of several packages), and is not checked.
func TestDocSnippets(t *testing.T) {
	t.Parallel()
	total := 0
	for _, path := range readerPages(t) {
		name := strings.TrimPrefix(filepath.ToSlash(path), "../../")
		text, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		page, err := parseDocPage(string(text), true)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for i, block := range page.blocks {
			if block.lang != "bork" || block.mode == "fragment" {
				continue
			}
			total++
			var quoted []string
			if block.mode == "fails" && i+1 < len(page.blocks) && page.blocks[i+1].lang == "text" {
				quoted = strings.Split(strings.TrimSpace(page.blocks[i+1].source), "\n")
			}
			t.Run(fmt.Sprintf("%s:%d", name, block.line), func(t *testing.T) {
				t.Parallel()
				file := filepath.Join(t.TempDir(), "main.bork")
				if err := os.WriteFile(file, []byte(block.source), 0o644); err != nil {
					t.Fatal(err)
				}
				_, _, err := Check(file)
				if block.mode == "fails" {
					var de *DiagError
					if !errors.As(err, &de) {
						t.Fatalf("the block is marked fails, but checking it gave: %v\n%s", err, block.source)
					}
					for _, line := range quoted {
						if !strings.Contains(de.Error(), strings.TrimSpace(line)) {
							t.Errorf("the page quotes %q, but the compiler says:\n%v", strings.TrimSpace(line), de)
						}
					}
					return
				}
				if err != nil {
					t.Fatalf("the block does not compile:\n%v\n%s", err, block.source)
				}
				formatted, err := format.Source(file, []byte(block.source))
				if err != nil {
					t.Fatal(err)
				}
				if string(formatted) != block.source {
					t.Errorf("the block is not formatted as bork fmt writes it:\n--- want ---\n%s--- got ---\n%s", formatted, block.source)
				}
			})
		}
	}
	if total < 10 {
		t.Errorf("only %d bork blocks were found; the reader pages or the block pattern changed", total)
	}
}

func TestParseDocPage(t *testing.T) {
	t.Parallel()
	page, err := parseDocPage("# A `b` C!\n\nsee [x](y.md) and `[no](link.md)`\n\n```bork fails\nfn\n```\n\n```text\nwhy\n```\n", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.blocks) != 2 || page.blocks[0].mode != "fails" || page.blocks[0].source != "fn\n" || page.blocks[1].lang != "text" {
		t.Errorf("blocks: %+v", page.blocks)
	}
	if links := inlineLink.FindAllStringSubmatch(strings.Join(page.prose, "\n"), -1); len(links) != 1 || links[0][1] != "y.md" {
		t.Errorf("links: %v", links)
	}
	if got := docAnchor("Run, build, and install"); got != "run-build-and-install" {
		t.Errorf("anchor: %s", got)
	}
	for _, unchecked := range []string{
		"```\nfn main() {}\n```\n",
		"~~~bork\nfn main() {}\n~~~\n",
		"````bork\nfn main() {}\n````\n",
		"```Bork\nfn main() {}\n```\n",
		"```bork,fails\nfn main() {}\n```\n",
		"  ```bork\n  fn main() {}\n  ```\n",
		"text\n\n    fn main() {}\n",
		"```bork broken\nfn main() {}\n```\n",
		"```bork\nfn main() {}\n",
	} {
		if _, err := parseDocPage(unchecked, true); err == nil {
			t.Errorf("accepted a code block that would not be checked:\n%s", unchecked)
		}
	}
	for _, link := range []string{`[a](m.md "title")`, `[b](<m.md>)`, `[c]( m.md )`} {
		if match := inlineLink.FindStringSubmatch(link); match == nil || match[1] != "m.md" {
			t.Errorf("%s: %v", link, match)
		}
	}
	for _, link := range []string{"[c][ref]", "[ref]: m.md", `<a href="m.md">`} {
		if !otherLink.MatchString(link) {
			t.Errorf("%s is not reported as unchecked", link)
		}
	}
}
