package runtimeclient

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	"github.com/kubeflow/pipelines/backend/src/v2/objectstore"
	"os"
)

const TokenPath = "/var/run/secrets/loom-runtime/token"
const CAPath = "/var/run/secrets/loom-runtime-ca/ca.crt"

// FrozenStore receives only the authenticated run's server-selected storage
// configuration. It never reads a mutable, tenant-provided ConfigMap.
type FrozenStore struct {
	root    string
	session objectstore.SessionInfo
}

func (s *FrozenStore) DefaultPipelineRoot() string { return s.root }
func (s *FrozenStore) GetStoreSessionInfo(root string) (objectstore.SessionInfo, error) {
	if root != s.root {
		return objectstore.SessionInfo{}, fmt.Errorf("storage root differs from frozen run policy")
	}
	raw, err := json.Marshal(s.session)
	if err != nil {
		return objectstore.SessionInfo{}, err
	}
	var copy objectstore.SessionInfo
	err = json.Unmarshal(raw, &copy)
	return copy, err
}

func FromEnvironment(ctx context.Context, namespace, name, run string, cacheDisabled bool) (*Metadata, *Cache, *FrozenStore, error) {
	transport, err := runtimeapi.NewClient(runtimeapi.ClientOptions{Endpoint: os.Getenv("LOOM_RUNTIME_ENDPOINT"), TokenFile: TokenPath, CAFile: CAPath})
	if err != nil {
		return nil, nil, nil, err
	}
	metadataClient, err := NewMetadata(transport)
	if err != nil {
		return nil, nil, nil, err
	}
	pipeline, err := metadataClient.GetPipeline(ctx, name, run, namespace, "", "", "")
	if err != nil {
		return nil, nil, nil, err
	}
	session := objectstore.SessionInfo{}
	if err = json.Unmarshal([]byte(pipeline.GetStoreSessionInfo()), &session); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid frozen store configuration")
	}
	cacheClient, err := NewCache(ctx, transport, namespace, name, run, !cacheDisabled)
	if err != nil {
		return nil, nil, nil, err
	}
	return metadataClient, cacheClient, &FrozenStore{root: pipeline.GetPipelineRoot(), session: session}, nil
}
