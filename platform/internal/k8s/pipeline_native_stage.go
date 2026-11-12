package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ValidateStagedPipelineWorkflow fails closed on admission/controller changes
// to the frozen spec. No equivalence rule silently removes security fields.
// A staged Workflow must remain suspended and cannot own automatic GC policy.
func ValidateStagedPipelineWorkflow(obj *unstructured.Unstructured, ref PipelineWorkflowRef, specHash string, requireUID bool) error {
	if !pipelineWorkflowMetadataMatches(obj, ref) || obj.GetDeletionTimestamp() != nil ||
		(requireUID && !PipelineWorkflowMatches(obj, ref)) {
		return &Permanent{Reason: "native staged Workflow identity conflict"}
	}
	suspended, present, err := unstructured.NestedBool(obj.Object, "spec", "suspend")
	if err != nil || !present || !suspended {
		return &Permanent{Reason: "native staging requires a suspended Workflow"}
	}
	for _, field := range []string{"ttlStrategy", "podGC", "volumeClaimGC", "shutdown"} {
		if _, exists, err := unstructured.NestedFieldNoCopy(obj.Object, "spec", field); err != nil || exists {
			return &Permanent{Reason: "native staging does not permit automatic cleanup or shutdown"}
		}
	}
	encoded, err := json.Marshal(obj.Object["spec"])
	if err != nil || len(encoded) > pipelineir.MaxIRBytes {
		return &Permanent{Reason: "native admitted spec exceeds staging limits"}
	}
	if specHash != "" {
		actual, err := PipelineWorkflowSpecHash(obj)
		if err != nil {
			return err
		}
		if actual != specHash {
			return &Permanent{Reason: "native admitted Workflow spec conflict"}
		}
	}
	return nil
}

// AdmitStagedPipelineWorkflow performs server-side dry-run only. Persist its
// normalized spec before a real Create. Metadata/status/UID from the dry-run
// response are not used as an execution binding or copied into the request.
func (c *Client) AdmitStagedPipelineWorkflow(ctx context.Context, ref PipelineWorkflowRef, compiled *unstructured.Unstructured) (*unstructured.Unstructured, string, error) {
	if ref.UID != "" {
		return nil, "", &Permanent{Reason: "cannot prepare an already-bound Workflow"}
	}
	if err := ValidateStagedPipelineWorkflow(compiled, ref, "", false); err != nil {
		return nil, "", err
	}
	admitted, err := c.dyn.Resource(WorkflowGVR).Namespace(ref.Namespace).Create(ctx, compiled.DeepCopy(), metav1.CreateOptions{
		DryRun: []string{metav1.DryRunAll}, FieldValidation: metav1.FieldValidationStrict,
	})
	if err != nil {
		return nil, "", err
	}
	if err := ValidateStagedPipelineWorkflow(admitted, ref, "", false); err != nil {
		return nil, "", err
	}
	fields, _, err := unstructured.NestedMap(admitted.Object, "spec")
	if err != nil {
		return nil, "", err
	}
	want := compiled.DeepCopy()
	want.Object["spec"] = fields
	hash, err := PipelineWorkflowSpecHash(want)
	return want, hash, err
}

// EnsureStagedPipelineWorkflow never updates, resumes or deletes an object.
// Name-based replay is allowed only while UID is unbound; a missing bound UID
// is never recreated. The caller must persist attempted before this method.
func (c *Client) EnsureStagedPipelineWorkflow(ctx context.Context, ref PipelineWorkflowRef, want *unstructured.Unstructured, specHash string, guard func() error) (*unstructured.Unstructured, error) {
	if guard == nil || !validator.IsSHA256(specHash) {
		return nil, fmt.Errorf("native staging requires a frozen spec digest and live execution guard")
	}
	if err := ValidateStagedPipelineWorkflow(want, ref, specHash, false); err != nil {
		return nil, err
	}
	live, err := c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
	if apierrors.IsNotFound(err) {
		if ref.UID != "" {
			return nil, &Permanent{Reason: "bound native Workflow is missing; recreation is forbidden"}
		}
		if err := guard(); err != nil {
			return nil, err
		}
		_, createErr := c.dyn.Resource(WorkflowGVR).Namespace(ref.Namespace).Create(ctx, want.DeepCopy(), metav1.CreateOptions{FieldValidation: metav1.FieldValidationStrict})
		// Always verify with GET, including successful or uncertain Create and
		// AlreadyExists. Never infer ownership from the HTTP response alone.
		live, err = c.ReadPipelineWorkflow(ctx, ref.Namespace, ref.Name)
		if err != nil && createErr != nil {
			return nil, createErr
		}
	}
	if err != nil {
		return nil, err
	}
	if err := ValidateStagedPipelineWorkflow(live, ref, specHash, true); err != nil {
		return nil, err
	}
	return live, nil
}
