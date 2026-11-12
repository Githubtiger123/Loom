package dto

import (
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

// Only public runtime capabilities are projected; never return its PodSpec/env.
type KServeModelFormat struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	AutoSelect bool   `json:"autoSelect"`
}

type KServeRuntime struct {
	Name       string              `json:"name"`
	Scope      string              `json:"scope"`
	Disabled   bool                `json:"disabled"`
	MultiModel bool                `json:"multiModel"`
	MultiNode  bool                `json:"multiNode"`
	Protocols  []string            `json:"protocols"`
	Formats    []KServeModelFormat `json:"formats"`
}

type KServeRuntimeList struct {
	Namespace  string          `json:"namespace"`
	Items      []KServeRuntime `json:"items"`
	ObservedAt time.Time       `json:"observedAt"`
}

type KServeCheck struct {
	Code    string `json:"code"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

type KServePreflight struct {
	Ready              bool            `json:"ready"`
	Checks             []KServeCheck   `json:"checks"`
	Runtimes           []KServeRuntime `json:"runtimes"`
	StorageAccess      string          `json:"storageAccess"`
	InferenceValidated bool            `json:"inferenceValidated"`
	ObservedAt         time.Time       `json:"observedAt"`
}

// KServe logs are bounded snapshots. Pod UID + container are required so a
// replica switch cannot silently turn a retry into a different log target.
type KServeLogQuery struct {
	InstanceID   string `json:"instanceId"`
	Revision     uint64 `json:"revision"`
	Pod          string `json:"pod"`
	PodUID       string `json:"podUID"`
	Container    string `json:"container"`
	TailLines    int64  `json:"tailLines"`
	LimitBytes   int64  `json:"limitBytes"`
	SinceSeconds int64  `json:"sinceSeconds"`
	Previous     bool   `json:"previous"`
}

func (q *KServeLogQuery) Validate() error {
	if !validator.IsCanonicalUUID(q.InstanceID) || q.Revision == 0 || !validator.IsDNS1123Subdomain(q.Pod) ||
		!validator.IsCanonicalUUID(q.PodUID) || !validator.IsDNS1123Label(q.Container) {
		return apperr.New(apperr.CodeInvalid, "日志需要服务 instanceId/revision、Pod 名称/UID 和容器名称")
	}
	if q.TailLines == 0 {
		q.TailLines = 200
	}
	if q.LimitBytes == 0 {
		q.LimitBytes = 256 << 10
	}
	if q.SinceSeconds == 0 {
		q.SinceSeconds = 3600
	}
	if q.TailLines < 1 || q.TailLines > 1000 || q.LimitBytes < 1 || q.LimitBytes > 1<<20 || q.SinceSeconds < 1 || q.SinceSeconds > 3600 {
		return apperr.New(apperr.CodeInvalid, "日志最多 1000 行、1 MiB、最近 3600 秒")
	}
	return nil
}

type KServeContainerDiagnostic struct {
	Name     string `json:"name"`
	Init     bool   `json:"init"`
	Ready    bool   `json:"ready"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	Restarts int32  `json:"restarts"`
	ExitCode *int32 `json:"exitCode,omitempty"`
}

type KServePodDiagnostic struct {
	Name       string                      `json:"name"`
	UID        string                      `json:"uid"`
	Phase      string                      `json:"phase"`
	Ready      bool                        `json:"ready"`
	Reason     string                      `json:"reason,omitempty"`
	Containers []KServeContainerDiagnostic `json:"containers"`
}

type KServeEvent struct {
	Kind    string    `json:"kind"`
	Name    string    `json:"name"`
	UID     string    `json:"uid"`
	Type    string    `json:"type"`
	Reason  string    `json:"reason"`
	Message string    `json:"message"`
	Count   int32     `json:"count"`
	At      time.Time `json:"at"`
}

type KServeDiagnostics struct {
	ServiceID       uint64                `json:"serviceId"`
	InstanceID      string                `json:"instanceId"`
	Revision        uint64                `json:"revision"`
	AppliedRevision uint64                `json:"appliedRevision"`
	UID             string                `json:"uid"`
	State           string                `json:"state"`
	Ready           bool                  `json:"ready"`
	Reason          string                `json:"reason,omitempty"`
	Pods            []KServePodDiagnostic `json:"pods"`
	Events          []KServeEvent         `json:"events"`
	Truncated       bool                  `json:"truncated"`
	ObservedAt      time.Time             `json:"observedAt"`
}
