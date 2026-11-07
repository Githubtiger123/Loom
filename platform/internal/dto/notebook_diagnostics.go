package dto

import (
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

// 日志固定为有界快照，不支持 follow、任意 sidecar 或任意 namespace。
type NotebookLogQuery struct {
	Pod          string `json:"pod,omitempty"`
	PodUID       string `json:"podUID,omitempty"`
	TailLines    int64  `json:"tailLines"`
	LimitBytes   int64  `json:"limitBytes"`
	SinceSeconds int64  `json:"sinceSeconds"`
	Previous     bool   `json:"previous"`
}

func (q *NotebookLogQuery) Validate() error {
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
		return apperr.New(apperr.CodeInvalid, "日志最多 1000 行、1MiB、最近 3600 秒")
	}
	if (q.Pod == "") != (q.PodUID == "") || (q.Pod != "" && (!validator.IsSecretName(q.Pod) || len(q.PodUID) > 64 || !validator.IsIdempotencyKey(q.PodUID))) {
		return apperr.New(apperr.CodeInvalid, "指定 Pod 时需同时提供合法的 Pod 名称和 UID")
	}
	return nil
}

type NotebookLogs struct {
	Pod        string    `json:"pod"`
	PodUID     string    `json:"podUID"`
	Container  string    `json:"container"`
	Text       string    `json:"text"`
	Truncated  bool      `json:"truncated"`
	ObservedAt time.Time `json:"observedAt"`
}

type NotebookEvent struct {
	Kind    string    `json:"kind"`
	Name    string    `json:"name"`
	UID     string    `json:"uid"`
	Type    string    `json:"type"`
	Reason  string    `json:"reason"`
	Message string    `json:"message"`
	Count   int32     `json:"count"`
	At      time.Time `json:"at"`
}
type NotebookEvents struct {
	Items      []NotebookEvent `json:"items"`
	Truncated  bool            `json:"truncated"`
	ObservedAt time.Time       `json:"observedAt"`
}
type NotebookPodDiagnostic struct {
	Name     string `json:"name"`
	UID      string `json:"uid"`
	Phase    string `json:"phase"`
	Ready    bool   `json:"ready"`
	Reason   string `json:"reason,omitempty"`
	Restarts int32  `json:"restarts"`
}
type NotebookDiagnostics struct {
	NotebookID               uint64                  `json:"notebookId"`
	OwnerUserID              *uint64                 `json:"ownerUserId"`
	Revision                 uint64                  `json:"revision"`
	UID                      string                  `json:"uid"`
	State                    string                  `json:"state"`
	Reason                   string                  `json:"reason,omitempty"`
	Pods                     []NotebookPodDiagnostic `json:"pods"`
	IdleReclamationAvailable bool                    `json:"idleReclamationAvailable"`
	IdleReclamationReason    string                  `json:"idleReclamationReason"`
	ObservedAt               time.Time               `json:"observedAt"`
}
type NotebookUsage struct {
	Available    bool       `json:"available"`
	Reason       string     `json:"reason,omitempty"`
	CPUMilli     *int64     `json:"cpuMilli,omitempty"`
	MemoryBytes  *int64     `json:"memoryBytes,omitempty"`
	GPUAvailable bool       `json:"gpuAvailable"`
	GPUReason    string     `json:"gpuReason"`
	Timestamp    *time.Time `json:"timestamp,omitempty"`
	Window       string     `json:"window,omitempty"`
	ObservedAt   time.Time  `json:"observedAt"`
}
