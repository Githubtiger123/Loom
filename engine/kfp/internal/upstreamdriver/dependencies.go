package driver

import (
	"context"

	"github.com/githubtiger123/loom/engine/kfp/metadata"
	"github.com/kubeflow/pipelines/api/v2alpha1/go/pipelinespec"
	"github.com/kubeflow/pipelines/backend/src/v2/objectstore"
)

// MetadataClient includes the upstream launcher contract and the two methods
// used by the driver but omitted from that contract at the pinned revision.
type MetadataClient interface {
	metadata.ClientInterface
	GetInputArtifactsByExecutionID(context.Context, int64) (map[string]*pipelinespec.ArtifactList, error)
	UpdateExecutionPluginProperties(context.Context, *metadata.Execution, map[string]string) error
}

var _ MetadataClient = (*metadata.Client)(nil)

// StoreConfig is implemented by the official config.Config. The caller owns
// loading it from trusted configuration; RootDAG never creates a kube client.
type StoreConfig interface {
	DefaultPipelineRoot() string
	GetStoreSessionInfo(string) (objectstore.SessionInfo, error)
}
