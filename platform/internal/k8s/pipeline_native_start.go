package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

const AnnotationPipelineStartToken = "loom.ml/pipeline-start-token"

// Persist this immutable request before calling ReconcileNativePipelineStart.
// A changed resourceVersion is never automatically rebased: it could represent
// an intervening resume/suspend whose HTTP response was lost.
type PipelineWorkflowStartRequest struct {
	Token           string `json:"token"`
	ResourceVersion string `json:"resourceVersion"`
	Protocol        string `json:"protocol,omitempty"`
}

func nativeStartRequestValid(request PipelineWorkflowStartRequest) bool {
	return validator.IsCanonicalUUID(request.Token) && validator.IsKubernetesResourceVersion(request.ResourceVersion) && (request.Protocol == "" || pipelineir.IsPythonCompiler(request.Protocol))
}

// Preparing does not modify Kubernetes. This first-start protocol cannot be
// used to re-run a completed Workflow or resume a previously running instance.
func PrepareNativePipelineStart(obj *unstructured.Unstructured, ref PipelineWorkflowRef, specHash string) (PipelineWorkflowStartRequest, error) {
	if ref.UID == "" || !validator.IsCatalogID(ref.UID) || !validator.IsSHA256(specHash) {
		return PipelineWorkflowStartRequest{}, &Permanent{Reason: "native start requires a bound Workflow UID and frozen spec hash"}
	}
	if err := ValidateStagedPipelineWorkflow(obj, ref, specHash, true); err != nil {
		return PipelineWorkflowStartRequest{}, err
	}
	if _, exists := obj.GetAnnotations()[AnnotationPipelineStartToken]; exists {
		return PipelineWorkflowStartRequest{}, &Permanent{Reason: "native Workflow already carries a start token"}
	}
	obs, err := ObservePipelineWorkflow(obj)
	if err != nil {
		return PipelineWorkflowStartRequest{}, err
	}
	python := pipelineir.IsPythonCompiler(obj.GetAnnotations()["loom.ml/pipeline-compiler"])
	if !obs.NodesComplete || (!python && (len(obs.Nodes) != 0 || (obs.RawPhase != "" && obs.RawPhase != "Pending"))) || (python && !nativePythonUnstarted(obs, ref)) || obs.FinishedAt != nil {
		return PipelineWorkflowStartRequest{}, &Permanent{Reason: "native Workflow is not an unstarted suspended instance"}
	}
	request := PipelineWorkflowStartRequest{Token: uuid.NewString(), ResourceVersion: obj.GetResourceVersion()}
	if python {
		request.Protocol = obj.GetAnnotations()["loom.ml/pipeline-compiler"]
	}
	if !nativeStartRequestValid(request) {
		return PipelineWorkflowStartRequest{}, &Permanent{Reason: "native start resource version is invalid"}
	}
	return request, nil
}

func nativePipelineStartAccepted(obj *unstructured.Unstructured, ref PipelineWorkflowRef, specHash string, request PipelineWorkflowStartRequest) (bool, error) {
	if err := ValidateNativePipelineObservedSpec(obj, ref, specHash); err != nil {
		return false, err
	}
	if obj.GetDeletionTimestamp() != nil {
		return false, &Permanent{Reason: "native start cannot target a deleting Workflow"}
	}
	token, exists := obj.GetAnnotations()[AnnotationPipelineStartToken]
	if !exists {
		return false, nil
	}
	if token != request.Token {
		return false, &Permanent{Reason: "native Workflow start token conflict"}
	}
	suspended, _, err := unstructured.NestedBool(obj.Object, "spec", "suspend")
	if err != nil {
		return false, err
	}
	if suspended {
		return false, &Permanent{Reason: "native Workflow was suspended after start; automatic resume is forbidden"}
	}
	return true, nil
}

// Only one pinned JSON Patch is allowed. Token and suspend=false are written
// atomically, with UID/RV/full-spec/annotations tests; this method never creates,
// replaces, deletes, or unsuspends an object already carrying the token.
// Read-only confirmation is allowed when allowPatch=false.
func (c *Client) ReconcileNativePipelineStart(ctx context.Context, ref PipelineWorkflowRef, specHash string,
	request PipelineWorkflowStartRequest, allowPatch bool, guard func() error) (*unstructured.Unstructured, error) {
	if guard == nil || ref.UID == "" || !validator.IsSHA256(specHash) || !nativeStartRequestValid(request) {
		return nil, fmt.Errorf("native start requires a frozen request, bound UID and live execution guard")
	}
	live, err := c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
	if err != nil {
		return nil, err // In particular, never recreate a missing bound UID.
	}
	accepted, err := nativePipelineStartAccepted(live, ref, specHash, request)
	if err != nil {
		return nil, err
	}
	if accepted {
		return live, nil
	}
	python := pipelineir.IsPythonCompiler(request.Protocol) && live.GetAnnotations()["loom.ml/pipeline-compiler"] == request.Protocol
	if !allowPatch || (!python && live.GetResourceVersion() != request.ResourceVersion) {
		return nil, &Permanent{Reason: "native start result is uncertain; automatic request rebasing is forbidden"}
	}
	if err := ValidateStagedPipelineWorkflow(live, ref, specHash, true); err != nil {
		return nil, err
	}
	if python {
		obs, err := ObservePipelineWorkflow(live)
		if err != nil || !nativePythonUnstarted(obs, ref) {
			return nil, &Permanent{Reason: "native Python start requires an unstarted suspended graph"}
		}
	}
	if err := guard(); err != nil {
		return nil, err
	}
	patch, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": ref.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": live.GetResourceVersion()},
		{"op": "test", "path": "/metadata/annotations", "value": live.GetAnnotations()},
		{"op": "test", "path": "/spec", "value": live.Object["spec"]},
		{"op": "test", "path": "/spec/suspend", "value": true},
		{"op": "add", "path": "/metadata/annotations/loom.ml~1pipeline-start-token", "value": request.Token},
		{"op": "replace", "path": "/spec/suspend", "value": false},
	})
	if err != nil {
		return nil, err
	}
	_, patchErr := c.dyn.Resource(WorkflowGVR).Namespace(ref.Namespace).Patch(ctx, ref.Name, types.JSONPatchType, patch,
		metav1.PatchOptions{FieldValidation: metav1.FieldValidationStrict})
	// Both success and unknown outcomes require a live GET. Do not infer that
	// tasks ran (or completed) merely from a successful Patch response.
	live, err = c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
	if err != nil {
		return nil, err
	}
	accepted, err = nativePipelineStartAccepted(live, ref, specHash, request)
	if err != nil {
		return nil, err
	}
	if accepted {
		return live, nil
	}
	if !python && live.GetResourceVersion() != request.ResourceVersion {
		return nil, &Permanent{Reason: "native start result is uncertain; automatic request rebasing is forbidden"}
	}
	if patchErr != nil {
		return nil, patchErr // Same RV + no token: a retry can only replay this CAS.
	}
	return nil, fmt.Errorf("native start acceptance has not been observed")
}

// Argo may publish the structural root while suspended. Python-v1 pins the
// token, UID, whole spec and annotations, but permits a fresh status-only RV
// CAS while still suspended and with no Pod/task ever observed. A confirmed
// token always takes the read-only path above; it can never be re-armed.
func nativePythonUnstarted(obs *PipelineWorkflowObservation, ref PipelineWorkflowRef) bool {
	if obs == nil || !obs.NodesComplete || obs.FinishedAt != nil || obs.Phase == "SUCCEEDED" || obs.Phase == "FAILED" {
		return false
	}
	for _, node := range obs.Nodes {
		if node.Type != "DAG" || node.Name != ref.Name || node.FinishedAt != nil {
			return false
		}
	}
	return true
}
