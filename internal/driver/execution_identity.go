package driver

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"slices"

	"github.com/GiGurra/bork/internal/check"
)

const (
	executionIdentityVersion    = 1
	executionIdentityMaxBytes   = 8 << 20
	executionPriorMaxCount      = 4096
	executionInvocationMaxCount = 4096
)

type executionDecline string

const executionClosureUnavailable executionDecline = "execution closure not certified"

// These owned primitives establish identity and invocation accounting only.
// No current() call qualifies until the execution closure collector is added.
type executionCandidate struct {
	input  []byte
	key    [sha256.Size]byte
	output executionOutputIdentity
}

type executionOutputIdentity struct {
	Site, Type    string
	Codec, Policy uint32
}

type executionReceipt struct {
	input    []byte
	inputKey [sha256.Size]byte
	output   executionOutputIdentity
	result   []byte
	valueKey [sha256.Size]byte
	key      [sha256.Size]byte
}

type executionPriorIdentity struct {
	Site, Type     string
	Codec, Policy  uint32
	Receipt, Value [sha256.Size]byte
}

// executionIdentityData must be derived by the future collector from resolved
// selections and captured inputs. Hashes supplied by a caller never authorize
// eligibility. Bounded identity construction does not certify a candidate.
type executionIdentityData struct {
	Namespace           [sha256.Size]byte
	Descriptor, Program []byte
	Output              executionOutputIdentity
	Prior               []executionPriorIdentity
}

func newExecutionCandidate(data executionIdentityData) (*executionCandidate, error) {
	if len(data.Descriptor) > executionIdentityMaxBytes || len(data.Program) > executionIdentityMaxBytes || len(data.Output.Site) > executionIdentityMaxBytes || len(data.Output.Type) > executionIdentityMaxBytes {
		return nil, errors.New("execution identity limit exceeded")
	}
	if len(data.Prior) > executionPriorMaxCount {
		return nil, errors.New("execution prior limit exceeded")
	}
	// Check the complete encoding size before cloning or appending any payload.
	size := 8 + sha256.Size + 8 + len(data.Descriptor) + 8 + len(data.Program) + 8 + len(data.Output.Site) + 8 + len(data.Output.Type) + 8 + 8
	if size > executionIdentityMaxBytes || len(data.Output.Site)+len(data.Output.Type) > executionIdentityMaxBytes-size {
		return nil, errors.New("execution identity limit exceeded")
	}
	for _, prior := range data.Prior {
		if len(prior.Site) > executionIdentityMaxBytes || len(prior.Type) > executionIdentityMaxBytes {
			return nil, errors.New("execution identity limit exceeded")
		}
		itemSize := 8 + len(prior.Site) + 8 + len(prior.Type) + 8 + 2*sha256.Size
		if itemSize > executionIdentityMaxBytes-size-len(data.Output.Site)-len(data.Output.Type) {
			return nil, errors.New("execution identity limit exceeded")
		}
		size += itemSize
	}
	if data.Output.Codec == 0 || data.Output.Policy == 0 || data.Output.Type == "" {
		return nil, errors.New("missing execution output identity")
	}
	input := make([]byte, 0, size)
	input = binary.BigEndian.AppendUint64(input, executionIdentityVersion)
	input = append(input, data.Namespace[:]...)
	appendBytes := func(value []byte) {
		input = binary.BigEndian.AppendUint64(input, uint64(len(value)))
		input = append(input, value...)
	}
	appendBytes(data.Descriptor)
	appendBytes(data.Program)
	appendBytes([]byte(data.Output.Site))
	appendBytes([]byte(data.Output.Type))
	input = binary.BigEndian.AppendUint32(input, data.Output.Codec)
	input = binary.BigEndian.AppendUint32(input, data.Output.Policy)
	input = binary.BigEndian.AppendUint64(input, uint64(len(data.Prior)))
	for _, prior := range data.Prior {
		appendBytes([]byte(prior.Site))
		appendBytes([]byte(prior.Type))
		input = binary.BigEndian.AppendUint32(input, prior.Codec)
		input = binary.BigEndian.AppendUint32(input, prior.Policy)
		input = append(input, prior.Receipt[:]...)
		input = append(input, prior.Value[:]...)
	}
	return &executionCandidate{input: input, key: executionInputIdentity(input, data.Output), output: data.Output}, nil
}

func (candidate *executionCandidate) identity() [sha256.Size]byte {
	if candidate == nil {
		return [sha256.Size]byte{}
	}
	return candidate.key
}
func (candidate *executionCandidate) current() bool { return false }
func (candidate *executionCandidate) certify(_ []byte) (*executionReceipt, error) {
	return nil, errors.New(string(executionClosureUnavailable))
}
func (receipt *executionReceipt) current() bool { return false }
func (receipt *executionReceipt) identity() [sha256.Size]byte {
	if receipt == nil {
		return [sha256.Size]byte{}
	}
	return receipt.key
}

func executionDigest(tag string, parts ...[]byte) [sha256.Size]byte {
	h := sha256.New()
	executionHashPart(h, []byte("bork-execution-identity-v1"))
	executionHashPart(h, []byte(tag))
	for _, part := range parts {
		executionHashPart(h, part)
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest
}
func executionHashPart(h hash.Hash, part []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(part)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(part)
}
func executionInputIdentity(input []byte, output executionOutputIdentity) [sha256.Size]byte {
	var versions [8]byte
	binary.BigEndian.PutUint32(versions[:4], output.Codec)
	binary.BigEndian.PutUint32(versions[4:], output.Policy)
	return executionDigest("input", input, []byte(output.Site), []byte(output.Type), versions[:])
}
func executionValueIdentity(output executionOutputIdentity, result []byte) [sha256.Size]byte {
	var versions [8]byte
	binary.BigEndian.PutUint32(versions[:4], output.Codec)
	binary.BigEndian.PutUint32(versions[4:], output.Policy)
	return executionDigest("typed-value", []byte(output.Site), []byte(output.Type), versions[:], result)
}
func (receipt *executionReceipt) validIdentity() bool {
	if receipt == nil || len(receipt.input) > executionIdentityMaxBytes || len(receipt.output.Site) > executionIdentityMaxBytes || len(receipt.output.Type) > executionIdentityMaxBytes {
		return false
	}
	metadata := len(receipt.output.Site) + len(receipt.output.Type)
	if metadata > executionIdentityMaxBytes-len(receipt.input) {
		return false
	}
	if len(receipt.result) > check.ComptimeResultLimit || receipt.output.Type == "" || receipt.output.Codec == 0 || receipt.output.Policy == 0 {
		return false
	}
	if receipt.inputKey != executionInputIdentity(receipt.input, receipt.output) || receipt.valueKey != executionValueIdentity(receipt.output, receipt.result) {
		return false
	}
	return receipt.key == executionDigest("receipt", receipt.inputKey[:], receipt.valueKey[:])
}
func (receipt *executionReceipt) clone() (*executionReceipt, error) {
	if !receipt.validIdentity() {
		return nil, errors.New("invalid execution receipt identity")
	}
	owned := *receipt
	owned.input = slices.Clone(receipt.input)
	owned.result = slices.Clone(receipt.result)
	return &owned, nil
}
func (receipt *executionReceipt) prior(site, typ string, codec, policy uint32, value [sha256.Size]byte) (executionPriorIdentity, error) {
	if !receipt.validIdentity() || receipt.output != (executionOutputIdentity{Site: site, Type: typ, Codec: codec, Policy: policy}) || value != receipt.valueKey {
		return executionPriorIdentity{}, errors.New("execution prior identity mismatch")
	}
	return executionPriorIdentity{Site: site, Type: typ, Codec: codec, Policy: policy, Receipt: receipt.key, Value: value}, nil
}
