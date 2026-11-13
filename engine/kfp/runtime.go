package kfpengine

import (
	"context"
	"fmt"

	client_manager "github.com/githubtiger123/loom/engine/kfp/internal/runtimeclients"
	component "github.com/githubtiger123/loom/engine/kfp/internal/upstreamcomponent"
	"github.com/githubtiger123/loom/engine/kfp/metadata"
	"github.com/kubeflow/pipelines/backend/src/v2/cacheutils"
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

type LauncherOptions = component.LauncherV2Options

func (c *Clients) K8sClient() kubernetes.Interface          { return c.Kubernetes }
func (c *Clients) MetadataClient() metadata.ClientInterface { return c.Metadata }
func (c *Clients) CacheClient() cacheutils.Client           { return c.Cache }

// ExecuteComponent is the integration seam used by cmd/launcher-v2's executable.
// Missing adapters fail closed, never fall back to MLMD defaults.
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
