package driver

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// The request precedes payload/receipts in our canonical envelope. A bounded
// prefix is sufficient for an eviction hint; it never certifies a cache hit.
// Unrecognized/large prefixes retain the entry until ordinary LRU pressure.
func cacheResultTarget(root *os.Root, entry cacheResultEntry) string {
	file, err := openCacheFile(root, entry.path, os.O_RDONLY, 0)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, 16<<10))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ""
	}
	token, err = decoder.Token()
	if err != nil || token != "schema" {
		return ""
	}
	var schema int
	if decoder.Decode(&schema) != nil || schema != cacheArtifactSchema {
		return ""
	}
	token, err = decoder.Token()
	if err != nil || token != "body" {
		return ""
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ""
	}
	var namespace, key [sha256.Size]byte
	for _, field := range []struct {
		name   string
		target any
	}{{"schema", &schema}, {"namespace", &namespace}, {"key", &key}} {
		token, err = decoder.Token()
		if err != nil || token != field.name || decoder.Decode(field.target) != nil {
			return ""
		}
	}
	if schema != cacheArtifactSchema || namespace != entry.namespace || key != entry.key {
		return ""
	}
	token, err = decoder.Token()
	if err != nil || token != "request" {
		return ""
	}
	var request cacheArtifactRequest
	if decoder.Decode(&request) != nil {
		return ""
	}
	actual, err := request.key()
	if err != nil || actual != key {
		return ""
	}
	if filepath.IsAbs(request.Path) {
		return request.Path
	}
	return request.Cwd + string(filepath.Separator) + request.Path
}
