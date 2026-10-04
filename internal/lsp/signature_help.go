package lsp

import "strings"

type signatureMarkup struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type signatureParameter struct {
	Label         string           `json:"label"`
	Documentation *signatureMarkup `json:"documentation,omitempty"`
}
type signatureInformation struct {
	Label           string               `json:"label"`
	Documentation   *signatureMarkup     `json:"documentation,omitempty"`
	Parameters      []signatureParameter `json:"parameters"`
	ActiveParameter *int                 `json:"activeParameter,omitempty"`
}
type signatureHelpResult struct {
	Signatures      []signatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature"`
	ActiveParameter *int                   `json:"activeParameter,omitempty"`
}

func (s *server) signatureHelp(path, source string, at position) (*signatureHelpResult, error) {
	pos, err := compilerPosition(path, source, at)
	if err != nil {
		return nil, err
	}
	pkg := s.state(path)
	if pkg == nil || pkg.analysis == nil {
		return nil, nil
	}
	help := pkg.analysis.SignatureHelp(path, source, pos)
	if help == nil {
		return nil, nil
	}
	signature := signatureInformation{Parameters: []signatureParameter{}, ActiveParameter: help.ActiveParameter}
	var labels []string
	for _, parameter := range help.Callable.Parameters {
		label := parameter.Type
		if parameter.Name != "" {
			label = parameter.Name + ": " + label
		}
		if parameter.Default != "" {
			label += " = " + parameter.Default
		}
		item := signatureParameter{Label: label}
		if parameter.Doc != "" {
			item.Documentation = &signatureMarkup{Kind: "markdown", Value: parameter.Doc}
		}
		signature.Parameters = append(signature.Parameters, item)
		labels = append(labels, label)
	}
	signature.Label = help.Name + "(" + strings.Join(labels, ", ") + "): " + help.Result
	if help.Effects != "nothing" {
		signature.Label += " uses " + help.Effects
	}
	docs := help.Documentation
	if pkg.stale {
		docs = "**Stale:** signature from the last successful check.\n\n" + docs
	}
	if len(help.Callable.Requires) > 0 {
		docs += "\n\nRequires: " + strings.Join(help.Callable.Requires, "; ")
	}
	if len(help.Callable.Needs) > 0 {
		docs += "\n\nNeeds: " + strings.Join(help.Callable.Needs, " + ")
	}
	if strings.TrimSpace(docs) != "" {
		signature.Documentation = &signatureMarkup{Kind: "markdown", Value: strings.TrimSpace(docs)}
	}
	return &signatureHelpResult{Signatures: []signatureInformation{signature}, ActiveParameter: help.ActiveParameter}, nil
}
