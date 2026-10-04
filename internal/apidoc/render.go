// Package apidoc renders checked package APIs without changing source files.
package apidoc

import (
	"crypto/sha256"
	"fmt"
	"html"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/doccomment"
)

type Package struct {
	API             *check.PackageAPI
	Module, Version string
}

func anchor(pkg string, d check.APIDeclaration) string {
	hash := sha256.Sum256([]byte(pkg + "\x00" + d.Kind + "\x00" + d.Receiver + "\x00" + d.Name))
	return fmt.Sprintf("api-%x", hash[:12])
}
func packageAnchor(path string) string { return anchor(path, check.APIDeclaration{}) }
func label(d check.APIDeclaration) string {
	if d.Receiver != "" {
		return d.Receiver + "." + d.Name
	}
	return d.Name
}

func Markdown(packages []Package) []byte {
	var out strings.Builder
	out.WriteString("# Bork API\n")
	for _, p := range packages {
		a := p.API
		fmt.Fprintf(&out, "\n## %s\n", a.Path)
		if p.Module != "" {
			fmt.Fprintf(&out, "\nModule: `%s`", p.Module)
			if p.Version != "" {
				fmt.Fprintf(&out, " at `%s`", p.Version)
			}
			out.WriteString(".\n")
		}
		if a.Unsafe {
			out.WriteString("\nThis package contains unsafe Go.\n")
		}
		if a.Documentation != "" {
			out.WriteString("\n" + doccomment.Markdown(a.Documentation) + "\n")
		}
		if len(a.Declarations) == 0 {
			out.WriteString("\nNo exported declarations.\n")
		}
		for _, d := range a.Declarations {
			fmt.Fprintf(&out, "\n<a id=%q></a>\n\n### %s\n\n%s\n", anchor(a.Path, d), label(d), doccomment.Fence("bork", d.Signature))
			if d.Documentation != "" {
				out.WriteString("\n" + doccomment.Markdown(d.Documentation) + "\n")
			}
			if d.Position.File != "" {
				fmt.Fprintf(&out, "\nSource: `%s:%d`.\n", d.Position.File, d.Position.Line)
			}
		}
	}
	return []byte(out.String())
}

func HTML(packages []Package) []byte {
	var out strings.Builder
	out.WriteString(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Bork API</title><style>body{max-width:72rem;margin:2rem auto;padding:0 1rem;font:16px/1.6 system-ui,sans-serif;color:#202126}pre{overflow:auto;background:#f3f4f5;padding:1rem;border-radius:.4rem}code{font-family:monospace}nav ul{list-style:none;padding-left:1rem}a{color:#215bc0}section{margin:3rem 0}.source{font-size:.9rem;color:#565b64}</style></head><body><h1>Bork API</h1><nav aria-label="Contents"><ul>
`)
	for _, p := range packages {
		fmt.Fprintf(&out, "<li><a href=\"#%s\">%s</a><ul>\n", packageAnchor(p.API.Path), html.EscapeString(p.API.Path))
		for _, d := range p.API.Declarations {
			fmt.Fprintf(&out, "<li><a href=\"#%s\">%s</a></li>\n", anchor(p.API.Path, d), html.EscapeString(label(d)))
		}
		out.WriteString("</ul></li>\n")
	}
	out.WriteString("</ul></nav>\n<main>\n")
	for _, p := range packages {
		a := p.API
		fmt.Fprintf(&out, "<section id=\"%s\"><h2>%s</h2>\n", packageAnchor(a.Path), html.EscapeString(a.Path))
		if p.Module != "" {
			text := "Module: " + p.Module
			if p.Version != "" {
				text += " at " + p.Version
			}
			out.WriteString("<p>" + html.EscapeString(text) + ".</p>\n")
		}
		if a.Unsafe {
			out.WriteString("<p>This package contains unsafe Go.</p>\n")
		}
		out.WriteString(doccomment.HTML(a.Documentation))
		if len(a.Declarations) == 0 {
			out.WriteString("<p>No exported declarations.</p>\n")
		}
		for _, d := range a.Declarations {
			fmt.Fprintf(&out, "<article id=\"%s\"><h3>%s</h3><pre><code>%s</code></pre>\n", anchor(a.Path, d), html.EscapeString(label(d)), html.EscapeString(d.Signature))
			out.WriteString(doccomment.HTML(d.Documentation))
			if d.Position.File != "" {
				fmt.Fprintf(&out, "<p class=\"source\">Source: %s:%d.</p>\n", html.EscapeString(d.Position.File), d.Position.Line)
			}
			out.WriteString("</article>\n")
		}
		out.WriteString("</section>\n")
	}
	out.WriteString("</main></body></html>\n")
	return []byte(out.String())
}
