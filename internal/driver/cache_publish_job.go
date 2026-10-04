package driver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"reflect"
	"slices"

	"github.com/GiGurra/bork/internal/diag"
)

const cachePublishJobSchema = 1
const cachePublishJobMaxBytes = 1 << 20
const cachePublisherSlots = 8

type cachePublishGo struct {
	ProcessEnv []string          `json:"process_env"`
	Values     map[string]string `json:"values"`
	Tool       string            `json:"tool"`
	Driver     string            `json:"driver"`
	Self       string            `json:"self"`
	ToolDigest [sha256.Size]byte `json:"tool_digest"`
	Namespace  [sha256.Size]byte `json:"namespace"`
}
type cachePublishName struct {
	Paths []string          `json:"paths"`
	Names map[string]string `json:"names"`
}
type cachePublishJob struct {
	Schema      int                    `json:"schema"`
	Root        string                 `json:"root"`
	Request     cacheArtifactRequest   `json:"request"`
	Source      *sourceReceipt         `json:"source"`
	Go          cachePublishGo         `json:"go"`
	Names       []cachePublishName     `json:"names"`
	Module      cacheArtifactModule    `json:"module"`
	SourcePaths []string               `json:"source_paths"`
	GoSource    []byte                 `json:"go_source"`
	Warnings    []cacheArtifactWarning `json:"warnings"`
}

func newCachePublishJob(root string, artifact *sessionArtifact) (*cachePublishJob, error) {
	// Bound owned clones before creating them, as well as the encoded handoff.
	budget := cacheEncodingBudget{remaining: cachePublishJobMaxBytes - 1024}
	for _, value := range []any{artifact.goSrc, artifact.module.mod, artifact.module.sum, artifact.warnings, artifact.context.processEnv, artifact.context.values, artifact.sourcePaths} {
		if err := budget.value(reflect.ValueOf(value), 0); err != nil {
			return nil, err
		}
	}
	artifact.inputs.mu.Lock()
	for key, read := range artifact.inputs.reads {
		for _, value := range []any{key.path, key.kind, read.data, read.entries} {
			if err := budget.value(reflect.ValueOf(value), 0); err != nil {
				artifact.inputs.mu.Unlock()
				return nil, err
			}
		}
	}
	artifact.inputs.mu.Unlock()
	for _, input := range artifact.names {
		for _, value := range []any{input.paths, input.names} {
			if err := budget.value(reflect.ValueOf(value), 0); err != nil {
				return nil, err
			}
		}
	}
	source, err := artifact.inputs.receipt()
	if err != nil {
		return nil, err
	}
	ctx := artifact.context
	job := &cachePublishJob{Schema: cachePublishJobSchema, Root: root, Request: cacheArtifactRequest{Path: artifact.path, Cwd: source.Cwd, Emit: artifact.emit}, Source: source, Go: cachePublishGo{ProcessEnv: slices.Clone(ctx.processEnv), Values: maps.Clone(ctx.values), Tool: ctx.tool, Driver: ctx.driver, Self: ctx.self, ToolDigest: ctx.toolDigest, Namespace: ctx.namespace}, Module: cacheArtifactModule{Mod: slices.Clone(artifact.module.mod), Sum: slices.Clone(artifact.module.sum)}, GoSource: slices.Clone(artifact.goSrc), SourcePaths: slices.Clone(artifact.sourcePaths)}
	for _, name := range artifact.names {
		if !name.standard {
			return nil, errUnsupportedGoReceipt
		}
		job.Names = append(job.Names, cachePublishName{Paths: slices.Clone(name.paths), Names: maps.Clone(name.names)})
	}
	for _, warning := range cloneSessionDiagnostics(artifact.warnings) {
		job.Warnings = append(job.Warnings, cacheArtifactWarning{Pos: warning.Pos, End: warning.End, Message: warning.Msg, Code: warning.Code, Severity: warning.Severity, Fixes: warning.Fixes})
	}
	return job, nil
}

func encodeCachePublishJob(job *cachePublishJob) ([]byte, error) {
	budget := cacheEncodingBudget{remaining: cachePublishJobMaxBytes - 1024}
	if err := budget.value(reflect.ValueOf(job), 0); err != nil {
		return nil, err
	}
	body, err := json.Marshal(job)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	encoded, err := json.Marshal(cacheArtifactEnvelope{Schema: cachePublishJobSchema, Body: body, Checksum: hex.EncodeToString(sum[:])})
	if err != nil || len(encoded) > cachePublishJobMaxBytes {
		return nil, errCacheArtifactBudget
	}
	return encoded, nil
}

func decodeCachePublishJob(reader io.Reader) (*cachePublishJob, error) {
	encoded, err := io.ReadAll(io.LimitReader(reader, cachePublishJobMaxBytes+1))
	if err != nil || len(encoded) > cachePublishJobMaxBytes {
		return nil, errInvalidCacheArtifact
	}
	if err := validateCacheJSON(encoded); err != nil {
		return nil, err
	}
	var envelope cacheArtifactEnvelope
	if err := decodeStrictCacheJSON(encoded, &envelope); err != nil || envelope.Schema != cachePublishJobSchema {
		return nil, errInvalidCacheArtifact
	}
	sum := sha256.Sum256(envelope.Body)
	if envelope.Checksum != hex.EncodeToString(sum[:]) {
		return nil, errInvalidCacheArtifact
	}
	var job cachePublishJob
	if err := decodeStrictCacheJSON(envelope.Body, &job); err != nil || job.Schema != cachePublishJobSchema || job.Source == nil || !validReceiptPath(job.Root) {
		return nil, errInvalidCacheArtifact
	}
	canonical, err := json.Marshal(job)
	if err != nil || !bytes.Equal(canonical, envelope.Body) {
		return nil, errInvalidCacheArtifact
	}
	return &job, nil
}

func (job *cachePublishJob) candidate() (*sessionArtifact, error) {
	if _, err := job.Request.key(); err != nil || job.Request.Cwd != job.Source.Cwd {
		return nil, errInvalidCacheArtifact
	}
	inputs, err := job.Source.snapshot()
	if err != nil || !inputs.current() {
		return nil, errInvalidCacheArtifact
	}
	resolved := resolveGoContext()
	if resolved.err != nil || resolved.driverErr != nil || resolved.tool != job.Go.Tool || resolved.driver != job.Go.Driver || resolved.self != job.Go.Self || !slices.Equal(resolved.processEnv, job.Go.ProcessEnv) {
		return nil, errUnsupportedGoReceipt
	}
	ctx := &goContext{processEnv: slices.Clone(job.Go.ProcessEnv), env: slices.Clone(job.Go.ProcessEnv), values: maps.Clone(job.Go.Values), tool: job.Go.Tool, driver: job.Go.Driver, self: job.Go.Self, toolDigest: job.Go.ToolDigest}
	ctx.pinSettings()
	if ctx.namespace != job.Go.Namespace || !supportedCacheMissSettings(ctx) {
		return nil, errUnsupportedGoReceipt
	}
	candidate := &sessionArtifact{path: job.Request.Path, emit: job.Request.Emit, inputs: inputs, module: &goModuleInputs{mod: slices.Clone(job.Module.Mod), sum: slices.Clone(job.Module.Sum)}, assets: newEmbedSnapshot(inputs), context: ctx, goSrc: slices.Clone(job.GoSource), sourcePaths: slices.Clone(job.SourcePaths)}
	for _, name := range job.Names {
		candidate.names = append(candidate.names, goNameInput{paths: slices.Clone(name.Paths), names: maps.Clone(name.Names), standard: true})
	}
	for _, warning := range job.Warnings {
		candidate.warnings = append(candidate.warnings, diag.Diagnostic{Pos: warning.Pos, End: warning.End, Msg: warning.Message, Code: warning.Code, Severity: warning.Severity, Fixes: warning.Fixes})
	}
	return candidate, nil
}
