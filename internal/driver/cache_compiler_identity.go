package driver

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
)

var errCompilerImageUnavailable = errors.New("running compiler image cannot be identified safely")

// The running image is immutable for the process lifetime. Never derive its
// identity by reopening os.Executable after an installer replaces that path.
var compilerImageDigest = sync.OnceValues(hashCompilerImage)

func compilerArtifactNamespace(schema, layout string) ([sha256.Size]byte, error) {
	digest, err := compilerImageDigest()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return compilerNamespaceDigest(digest, schema, layout), nil
}

func compilerNamespaceDigest(compiler [sha256.Size]byte, schema, layout string) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = hash.Write(compiler[:])
	add := func(value string) {
		// Schema/layout identifiers are private and bounded, but length encoding
		// avoids relying on forbidden separator bytes in future identifiers.
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	add(schema)
	add(layout)
	var out [sha256.Size]byte
	copy(out[:], hash.Sum(nil))
	return out
}
