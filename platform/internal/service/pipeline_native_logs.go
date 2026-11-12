package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func (s *Service) nativePipelineTaskPodRef(ctx context.Context, n *model.PipelineRun, taskID, container string) (k8s.PipelinePodRef, error) {
	var empty k8s.PipelinePodRef
	if !strings.HasPrefix(taskID, "execution-") {
		return empty, apperr.New(apperr.CodeInvalid, "请使用原生 runtime/DAG 返回的 taskId")
	}
	parts := strings.Split(strings.TrimPrefix(taskID, "execution-"), "-attempt-")
	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || id == 0 || parts[0] != fmt.Sprint(id) || len(parts) > 2 {
		return empty, apperr.New(apperr.CodeInvalid, "taskId 无效")
	}
	a, err := s.Store.PipelineRunAttempt(ctx, n.GroupID, n.ID, n.InstanceID, false)
	if err != nil {
		return empty, err
	}
	j := &model.PipelineSubmission{}
	if err := s.DB.WithContext(ctx).Where("instance_id = ?", n.InstanceID).First(j).Error; err != nil {
		return empty, err
	}
	frozen, err := restoreNativeStaticNodes(a, j)
	if err != nil {
		return empty, err
	}
	ref, ok := nativePipelineRef(a)
	if !ok || ref.UID == "" || n.ExternalID == nil || *n.ExternalID != ref.UID {
		return empty, nativeFinalConflict()
	}
	row := &model.PipelineTaskExecution{}
	if err := s.DB.WithContext(ctx).Where("id = ? AND group_id = ? AND run_id = ? AND instance_id = ?", id, n.GroupID, n.ID, n.InstanceID).First(row).Error; err != nil {
		return empty, domainError(err)
	}
	attempts, err := nativeRetryAttempts(row.RuntimeState)
	if err != nil {
		return empty, err
	}
	var attempt *nativeRetryAttempt
	if len(parts) == 2 {
		index, parseErr := strconv.Atoi(parts[1])
		if parseErr != nil || index < 0 || index >= len(attempts) || parts[1] != strconv.Itoa(index) {
			return empty, apperr.New(apperr.CodeInvalid, "重试序号无效")
		}
		attempt = &attempts[index]
	} else if len(attempts) > 0 {
		attempt = &attempts[len(attempts)-1]
	}
	if attempt != nil {
		if attempt.PodUID == "" {
			return empty, apperr.New(apperr.CodeConflict, "本次尝试尚未绑定 Pod")
		}
		row.PodName, row.PodUID, row.ArgoNodeID = &attempt.PodName, &attempt.PodUID, &attempt.NodeID
	}
	if row.PodName == nil || row.PodUID == nil || row.ArgoNodeID == nil {
		return empty, apperr.New(apperr.CodeConflict, "该节点尚未分配 Pod 或不是容器节点")
	}
	name := ""
	if frozen.Plan.Compiler() == pipelineir.AdvancedCompilerVersion {
		var saved struct {
			NodeName string `json:"nodeName"`
		}
		if json.Unmarshal(row.RuntimeState, &saved) != nil {
			return empty, nativeFinalConflict()
		}
		name = saved.NodeName
	}
	for _, task := range frozen.Plan.Tasks() {
		if task.NodeKey == row.NodeKey {
			name = ref.Name + "." + task.TaskName
			break
		}
	}
	if name == "" {
		return empty, nativeFinalConflict()
	}
	if attempt != nil {
		name = attempt.NodeName
	}
	return k8s.PipelinePodRef{Namespace: n.Namespace, RunID: n.InstanceID, Pod: *row.PodName, Container: container, NativeWorkflow: &ref, NativeSpecHash: j.RequestHash, NativePodUID: *row.PodUID, NativeNodeID: *row.ArgoNodeID, NativeNodeName: name}, nil
}
