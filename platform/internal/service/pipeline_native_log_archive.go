package service

import (
	"context"
	"encoding/json"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/logarchive"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func sameNativeLogStorage(a, b *k8s.NativeLogArchivePolicy) bool {
	return a != nil && b != nil && a.Endpoint == b.Endpoint && a.Bucket == b.Bucket && a.Prefix == b.Prefix && a.Cluster == b.Cluster && a.Region == b.Region && a.Insecure == b.Insecure
}

// Argo uploads logs before completing each Pod. Confirm every bound attempt is
// readable before permitting final receipts/cleanup; listing is outside SQL.
func (s *Service) verifyNativeLogArchive(ctx context.Context, a *model.PipelineRunAttempt) error {
	var snapshot nativeStagingSnapshot
	if json.Unmarshal(a.Snapshot, &snapshot) != nil {
		return nativeFinalConflict()
	}
	if snapshot.Policy.LogArchive == nil {
		return nil
	}
	if s.PipelineLogArchivesReader == nil {
		return apperr.New(apperr.CodeUnavailable, "自动日志归档读取未配置")
	}
	p := snapshot.Policy.LogArchive
	if !sameNativeLogStorage(p, s.NativeLogArchivePolicy) {
		return apperr.New(apperr.CodeConflict, "日志读取配置与本次执行冻结的归档位置不一致")
	}
	m, err := s.PipelineLogArchivesReader.Manifest(ctx, logarchive.Execution{Cluster: p.Cluster, Namespace: snapshot.Input.Command.Namespace, InstanceID: a.InstanceID})
	if err != nil {
		return err
	}
	if m.Source != "ARGO" {
		return apperr.New(apperr.CodeConflict, "自动日志归档读取格式不匹配")
	}
	available := map[string]bool{}
	for _, entry := range m.Entries {
		available[entry.Pod] = true
	}
	rows := []model.PipelineTaskExecution{}
	if err = s.DB.WithContext(ctx).Where("group_id = ? AND run_id = ? AND instance_id = ?", a.GroupID, a.RunID, a.InstanceID).Limit(4001).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) > 4000 {
		return nativeFinalConflict()
	}
	for _, row := range rows {
		var state struct {
			MainStarted          bool   `json:"mainStarted"`
			LogUnavailableReason string `json:"logUnavailableReason"`
		}
		if json.Unmarshal(row.RuntimeState, &state) != nil {
			return nativeFinalConflict()
		}
		neverStarted := state.LogUnavailableReason == "MAIN_NEVER_STARTED" && !state.MainStarted && row.Phase == "FAILED" && row.PodUID != nil
		if row.PodName != nil && !available[*row.PodName] && !neverStarted {
			return apperr.New(apperr.CodeUnavailable, "任务日志尚未完成自动归档")
		}
		attempts, err := nativeRetryAttempts(row.RuntimeState)
		if err != nil {
			return err
		}
		for _, attempt := range attempts {
			if attempt.PodName != "" && !available[attempt.PodName] {
				return apperr.New(apperr.CodeUnavailable, "重试日志尚未完成自动归档")
			}
		}
	}
	return nil
}
