package naming

import _ "embed"

// RuntimeSource is emitted when the codec word helpers are reachable.
//
//go:embed core.go
var RuntimeSource string

func Words(name string) []string                     { return _borkCodecWords(name) }
func JoinWords(words []string, policy string) string { return _borkCodecJoinWords(words, policy) }
