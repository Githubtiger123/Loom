// Package kfpengine embeds the upstream compiler. It does not implement an IR
// lowering pass or replace the upstream driver/launcher execution protocol.
package kfpengine

import (
	"context"
	"encoding/json"
	"errors"

	wfapi "github.com/argoproj/argo-workflows/v4/pkg/apis/workflow/v1alpha1"
)

const (
	UpstreamRevision = "555bfa60a70da88c594960bdacfd78fa7c061043"
	CompilerVersion  = "loom-kfp-official-v1"
	MaxIRBytes       = 512 << 10
	MaxTasks         = 128
	MaxDepth         = 16
)

var ErrRuntimeNotConnected = errors.New("official driver/launcher Runtime API adapters are not connected")

type Issue struct {
	Code string `json:"code"`
	Path string `json:"path"`
}

// Report preserves the public compatibility response without exposing argv,
// generated manifests, credentials, or operator-configured runtime endpoints.
type Report struct {
	CompilerVersion   string   `json:"compilerVersion"`
	SchemaVersion     string   `json:"schemaVersion"`
	ValidationMode    string   `json:"validationMode"`
	Compilable        bool     `json:"compilable"`
	Runnable          bool     `json:"runnable"`
	ExecutionReason   string   `json:"executionReason"`
	SupportedFeatures []string `json:"supportedFeatures"`
	Issues            []Issue  `json:"issues"`
	IssuesTruncated   bool     `json:"issuesTruncated"`
	IRDigest          string   `json:"irDigest,omitempty"`
	PlanDigest        string   `json:"planDigest,omitempty"`
	TaskCount         int      `json:"taskCount"`
	DependencyCount   int      `json:"dependencyCount"`
}

type Identity struct {
	GroupID      uint64
	RunID        uint64
	InstanceID   string
	Namespace    string
	NamespaceUID string
}

type Options struct {
	DriverImage    string
	LauncherImage  string
	ServiceAccount string
	PipelineRoot   string
	Parallelism    int64
	CacheDisabled  bool
}

type Request struct {
	PipelineIR json.RawMessage
	Parameters map[string]any
	Identity   Identity
	Options    Options
}

type Result struct {
	Workflow       *wfapi.Workflow
	IRDigest       string
	WorkflowDigest string
	EngineRevision string
}

// Backend is the execution boundary, not an implementation of KFP semantics.
// No backend is installed until workload authentication, metadata, and cache
// persistence are wired. NativeEnabled alone cannot satisfy this boundary.
type Backend interface {
	Ready(context.Context) error
	Execute(context.Context, Operation) error
}

type Operation struct {
	ID         string
	GroupID    uint64
	RunID      uint64
	InstanceID string
	Action     string
	Fence      string
	LeaseOwner string
}
