package kfpengine

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	driver "github.com/githubtiger123/loom/engine/kfp/internal/upstreamdriver"
	"github.com/kubeflow/pipelines/backend/src/v2/cacheutils"
	"github.com/kubeflow/pipelines/backend/src/v2/common/plugins"
	"github.com/kubeflow/pipelines/backend/src/v2/config"
	"google.golang.org/protobuf/proto"
)

type DriverOptions = driver.Options
type DriverExecution = driver.Execution
type DriverMetadataClient = driver.MetadataClient
type DriverStoreConfig = driver.StoreConfig

var _ DriverStoreConfig = (*config.Config)(nil)

type DriverKind string

const (
	RootDAGDriver   DriverKind = "ROOT_DAG"
	DAGDriver       DriverKind = "DAG"
	ContainerDriver DriverKind = "CONTAINER"
)

// DriverClients are injected by the authenticated runtime entrypoint. Keeping
// cache disabled does not remove the driver's metadata read/write dependency.
type DriverClients struct {
	Metadata DriverMetadataClient
	Cache    cacheutils.Client
	Store    DriverStoreConfig
}

// ExecuteDriver preserves upstream input/condition/iteration/cache semantics.
// This is not a CLI, a Kubernetes submitter, or a runtime authorization bypass:
// clients must already be scoped to their authenticated workflow/task.
func ExecuteDriver(ctx context.Context, kind DriverKind, opts DriverOptions, clients DriverClients) (*DriverExecution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if clients.Metadata == nil {
		return nil, ErrRuntimeNotConnected
	}
	if opts.PipelineName == "" || opts.RunID == "" || opts.Namespace == "" || opts.Component == nil {
		return nil, fmt.Errorf("pipeline, run, namespace and component identity are required")
	}
	// Platform extensions require admission/RBAC policies beyond IR compilation.
	if len(opts.Component.GetSinglePlatformSpecs()) != 0 || proto.Size(opts.KubernetesExecutorConfig) != 0 || strings.HasPrefix(opts.Container.GetImage(), "argostub/") {
		return nil, fmt.Errorf("Kubernetes platform operations require an explicit policy adapter")
	}
	if err := validateContainerPolicy(opts.Container); err != nil {
		return nil, err
	}
	// No implicit external plugin initialization in the Loom entrypoint.
	opts.PluginDispatcher = plugins.NoOpDispatcher{}
	opts.DriverType = string(kind)
	switch kind {
	case RootDAGDriver:
		if clients.Store == nil {
			return nil, ErrRuntimeNotConnected
		}
		root, err := url.Parse(opts.RuntimeConfig.GetGcsOutputDirectory())
		if err != nil || root.Host == "" || root.User != nil || root.RawQuery != "" || root.Fragment != "" || (root.Scheme != "s3" && root.Scheme != "gs" && root.Scheme != "minio") {
			return nil, fmt.Errorf("root driver requires the frozen operator-configured pipeline root")
		}
		return driver.RootDAG(ctx, opts, clients.Metadata, clients.Store)
	case DAGDriver:
		return driver.DAG(ctx, opts, clients.Metadata)
	case ContainerDriver:
		if clients.Cache == nil {
			return nil, ErrRuntimeNotConnected
		}
		return driver.Container(ctx, opts, clients.Metadata, clients.Cache)
	default:
		return nil, fmt.Errorf("unsupported driver kind %q", kind)
	}
}
