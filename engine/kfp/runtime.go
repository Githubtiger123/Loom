package kfpengine

import (
	"context"
	"fmt"

	"github.com/kubeflow/pipelines/backend/src/v2/cacheutils"
	"github.com/kubeflow/pipelines/backend/src/v2/client_manager"
	"github.com/kubeflow/pipelines/backend/src/v2/component"
	"github.com/kubeflow/pipelines/backend/src/v2/metadata"
	"k8s.io/client-go/kubernetes"
)

// Clients injects persistence without reimplementing the upstream launcher.
// Implementations must authenticate each task and scope all metadata/cache
// calls to its assigned run. No default MLMD/KFP network client is created.
type Clients struct {
	Kubernetes kubernetes.Interface
	Metadata   metadata.ClientInterface
	Cache      cacheutils.Client
}

var _ client_manager.ClientManagerInterface = (*Clients)(nil)

func (c *Clients) K8sClient() kubernetes.Interface          { return c.Kubernetes }
func (c *Clients) MetadataClient() metadata.ClientInterface { return c.Metadata }
func (c *Clients) CacheClient() cacheutils.Client           { return c.Cache }

// ExecuteComponent is the integration seam used by the future custom launcher
// executable. Missing adapters fail closed, never fall back to MLMD defaults.
func ExecuteComponent(ctx context.Context, executionID int64, executorInput, componentSpec string, command []string, options *component.LauncherV2Options, clients *Clients) error {
	if clients == nil || clients.Kubernetes == nil || clients.Metadata == nil || clients.Cache == nil || options == nil {
		return ErrRuntimeNotConnected
	}
	launcher, err := component.NewLauncherV2(ctx, executionID, executorInput, componentSpec, command, options, clients)
	if err != nil {
		return fmt.Errorf("create official launcher: %w", err)
	}
	return launcher.Execute(ctx)
}
