// Package runtimeclient adapts official runtime clients to Loom's scoped API.
package runtimeclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/githubtiger123/loom/engine/kfp/internal/upstreamcache"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/kubeflow/pipelines/api/v2alpha1/go/cachekey"
	"github.com/kubeflow/pipelines/api/v2alpha1/go/pipelinespec"
	api "github.com/kubeflow/pipelines/backend/api/v1beta1/go_client"
	"github.com/kubeflow/pipelines/backend/src/v2/cacheutils"
	"google.golang.org/protobuf/encoding/protojson"
)

var ErrCacheDisabled = errors.New("runtime cache is disabled for this execution")

type Cache struct {
	ctx                      context.Context
	transport                *runtimeapi.Client
	namespace, pipeline, run string
	enabled                  bool
}

var _ cacheutils.Client = (*Cache)(nil)

func NewCache(ctx context.Context, transport *runtimeapi.Client, namespace, pipelineName, runID string, enabled bool) (*Cache, error) {
	if ctx == nil || transport == nil || namespace == "" || pipelineName == "" || runID == "" {
		return nil, fmt.Errorf("runtime cache requires transport and bound execution identity")
	}
	return &Cache{ctx: ctx, transport: transport, namespace: namespace, pipeline: "pipeline/" + pipelineName, run: runID, enabled: enabled}, nil
}

func (c *Cache) GenerateCacheKey(inputs *pipelinespec.ExecutorInput_Inputs, outputs *pipelinespec.ExecutorInput_Outputs, outputParametersTypeMap map[string]string,
	cmdArgs []string, image string, pvcNames []string, env []*pipelinespec.PipelineDeploymentConfig_PipelineContainerSpec_EnvVar) (*cachekey.CacheKey, error) {
	return upstreamcache.GenerateCacheKey(inputs, outputs, outputParametersTypeMap, cmdArgs, image, pvcNames, env)
}

func (c *Cache) GenerateFingerPrint(key *cachekey.CacheKey) (string, error) {
	if key == nil {
		return "", fmt.Errorf("cache key is required")
	}
	return upstreamcache.GenerateFingerPrint(key)
}

func validFingerprint(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func validExecutionID(value string) bool {
	id, err := strconv.ParseInt(value, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == value
}

func (c *Cache) GetExecutionCache(fingerprint, pipelineName, namespace string) (string, error) {
	if !c.enabled {
		return "", ErrCacheDisabled
	}
	if pipelineName != c.pipeline || namespace != c.namespace || !validFingerprint(fingerprint) {
		return "", fmt.Errorf("cache lookup is outside its bound scope or has an invalid fingerprint")
	}
	// No context parameter in the upstream interface: preserve the driver's
	// invocation context instead of silently using context.Background().
	payload, err := json.Marshal(struct {
		Fingerprint string `json:"fingerprint"`
	}{fingerprint})
	if err != nil {
		return "", err
	}
	encoded, err := c.transport.Call(c.ctx, runtimeapi.LookupCache, payload)
	if err != nil {
		return "", err
	}
	var result struct {
		Hit         *bool  `json:"hit"`
		ExecutionID string `json:"executionId"`
	}
	if json.Unmarshal(encoded, &result) != nil || result.Hit == nil {
		return "", fmt.Errorf("invalid cache lookup response")
	}
	if !*result.Hit {
		if result.ExecutionID != "" {
			return "", fmt.Errorf("cache miss contains an execution ID")
		}
		return "", nil
	}
	if !validExecutionID(result.ExecutionID) {
		return "", fmt.Errorf("cache hit has an invalid execution ID")
	}
	return result.ExecutionID, nil
}

func (c *Cache) CreateExecutionCache(ctx context.Context, task *api.Task) error {
	if !c.enabled {
		return ErrCacheDisabled
	}
	if task == nil || task.GetNamespace() != c.namespace || task.GetPipelineName() != c.pipeline || task.GetRunId() != c.run ||
		!validFingerprint(task.GetFingerprint()) || !validExecutionID(task.GetMlmdExecutionID()) || task.GetCreatedAt() == nil || task.GetFinishedAt() == nil ||
		task.GetCreatedAt().CheckValid() != nil || task.GetFinishedAt().CheckValid() != nil || task.GetFinishedAt().AsTime().Before(task.GetCreatedAt().AsTime()) {
		return fmt.Errorf("cache publication has invalid identity, fingerprint or timestamps")
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	raw, err := protojson.Marshal(task)
	if err != nil {
		return fmt.Errorf("cache task could not be encoded")
	}
	payload, err := json.Marshal(struct {
		Task json.RawMessage `json:"task"`
	}{raw})
	if err != nil {
		return err
	}
	encoded, err := c.transport.Call(ctx, runtimeapi.PublishCache, payload)
	if err != nil {
		return err
	}
	var result struct {
		Published bool `json:"published"`
	}
	if json.Unmarshal(encoded, &result) != nil || !result.Published {
		return fmt.Errorf("cache publication was not confirmed")
	}
	return nil
}
