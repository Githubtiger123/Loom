// Package runtimeclients preserves the upstream launcher injection contract.
package runtimeclients

import (
	"github.com/githubtiger123/loom/engine/kfp/metadata"
	"github.com/kubeflow/pipelines/backend/src/v2/cacheutils"
	"k8s.io/client-go/kubernetes"
)

type ClientManagerInterface interface {
	K8sClient() kubernetes.Interface
	MetadataClient() metadata.ClientInterface
	CacheClient() cacheutils.Client
}
