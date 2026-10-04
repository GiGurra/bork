package lsp

import (
	"encoding/json"
	"fmt"

	"github.com/GiGurra/bork/internal/check"
)

type inlayHintSettings struct {
	Types      *bool `json:"types"`
	Parameters *bool `json:"parameters"`
	Facts      *bool `json:"facts"`
}

func (s *server) configureInlays(raw json.RawMessage) error {
	var p struct {
		Settings struct {
			Bork struct {
				InlayHints inlayHintSettings `json:"inlayHints"`
			} `json:"bork"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	before := s.inlayOptions()
	changes := p.Settings.Bork.InlayHints
	if changes.Types != nil {
		s.inlaySettings.Types = changes.Types
	}
	if changes.Parameters != nil {
		s.inlaySettings.Parameters = changes.Parameters
	}
	if changes.Facts != nil {
		s.inlaySettings.Facts = changes.Facts
	}
	if before != s.inlayOptions() && s.inlayRefreshSupport {
		s.inlayRefreshID++
		return writeMessage(s.out, map[string]any{"jsonrpc": "2.0", "id": fmt.Sprintf("bork/inlayHint/refresh/%d", s.inlayRefreshID), "method": "workspace/inlayHint/refresh"})
	}
	return nil
}

func (s *server) inlayHints(path string, p documentParams) (any, error) {
	out := []any{}
	pkg := s.state(path)
	if pkg == nil || pkg.analysis == nil {
		return out, nil
	}
	source := s.source(path)
	if pkg.analysis.Sources()[path] != source {
		return out, nil
	}
	options := s.inlayOptions()
	if !options.Types && !options.Parameters && !options.Facts {
		return out, nil
	}
	hints, err := pkg.analysis.EditorInlays(path, options)
	if err != nil {
		return out, nil
	}
	for _, hint := range hints {
		pos := lspPosition(source, hint.Pos)
		less := func(a, b position) bool { return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character }
		if less(pos, p.Range.Start) || !less(pos, p.Range.End) {
			continue
		}
		kind := 1
		value := map[string]any{"position": pos, "label": hint.Label, "kind": kind}
		if hint.Parameter {
			value["kind"] = 2
			value["paddingRight"] = true
		}
		out = append(out, value)
	}
	return out, nil
}

func (s *server) inlayOptions() check.EditorInlayOptions {
	enabled := func(value *bool, fallback bool) bool {
		if value != nil {
			return *value
		}
		return fallback
	}
	return check.EditorInlayOptions{Types: enabled(s.inlaySettings.Types, true), Parameters: enabled(s.inlaySettings.Parameters, false), Facts: enabled(s.inlaySettings.Facts, false)}
}
