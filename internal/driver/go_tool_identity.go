package driver

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Matching metadata is trusted only after two seconds of monotonic observation
// of an unchanged tuple and matching bytes. This covers coarse timestamps without
// comparing the process clock with a remote filesystem clock. Timestamp granularity
// must be at most two seconds; FAT/exFAT lack independent ctime and always hash.
const goToolTimestampMargin = 2 * time.Second

type goToolIdentity struct {
	path                string
	device, inode       uint64
	size                int64
	mode                os.FileMode
	mtimeSec, mtimeNsec int64
	ctimeSec, ctimeNsec int64
}

type goToolEvidence struct {
	mu          sync.Mutex
	identity    *goToolIdentity
	mode        os.FileMode
	stableSince time.Time
}

func captureGoToolEvidence(path string) ([sha256.Size]byte, *goToolEvidence, error) {
	var empty [sha256.Size]byte
	stableSince := time.Now()
	file, err := os.Open(path)
	if err != nil {
		return empty, nil, err
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil {
		return empty, nil, err
	}
	if !before.Mode().IsRegular() {
		return empty, nil, fmt.Errorf("go launcher is not a regular file: %s", path)
	}
	identity := platformGoToolIdentity(path, before)
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return empty, nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return empty, nil, err
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		return empty, nil, err
	}
	if !os.SameFile(before, after) || !os.SameFile(after, pathInfo) || after.Mode() != pathInfo.Mode() {
		return empty, nil, fmt.Errorf("go launcher changed while hashing: %s", path)
	}
	if identity != nil {
		current := platformGoToolIdentity(path, after)
		last := platformGoToolIdentity(path, pathInfo)
		if current == nil || last == nil || *identity != *current || *current != *last {
			return empty, nil, fmt.Errorf("go launcher changed while hashing: %s", path)
		}
	}
	if err := file.Close(); err != nil {
		return empty, nil, err
	}
	copy(empty[:], hash.Sum(nil))
	return empty, &goToolEvidence{identity: identity, mode: before.Mode(), stableSince: stableSince}, nil
}

func (v *goContextValidation) toolCurrent() bool {
	if v.toolEvidence == nil {
		digest, err := goToolDigest(v.launcher)
		return err == nil && digest == v.toolDigest
	}
	evidence := v.toolEvidence
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	file, err := os.Stat(v.launcher)
	if err != nil || file.Mode() != evidence.mode {
		return false
	}
	if evidence.identity != nil {
		current := platformGoToolIdentity(v.launcher, file)
		if current == nil || current.mode != evidence.identity.mode {
			return false
		}
		if time.Since(evidence.stableSince) > goToolTimestampMargin && *current == *evidence.identity {
			return true
		}
	}
	// Re-record only a digest that still matches the captured configuration.
	digest, fresh, err := captureGoToolEvidence(v.launcher)
	if err != nil || digest != v.toolDigest {
		return false
	}
	if evidence.identity == nil || fresh.identity == nil || *evidence.identity != *fresh.identity {
		evidence.stableSince = fresh.stableSince
	}
	evidence.identity = fresh.identity
	return true
}
