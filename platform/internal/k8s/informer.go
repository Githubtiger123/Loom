package k8s

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

var (
	NamespaceGVR             = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	PodGVR                   = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	ServiceGVR               = schema.GroupVersionResource{Version: "v1", Resource: "services"}
	ServiceAccountGVR        = schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}
	ResourceQuotaGVR         = schema.GroupVersionResource{Version: "v1", Resource: "resourcequotas"}
	PersistentVolumeClaimGVR = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	StatefulSetGVR           = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
	DeploymentGVR            = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	ReplicaSetGVR            = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}
	RoleBindingGVR           = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}
	ClusterRoleGVR           = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}
)

// Each role owns these informers. Events only schedule a business observation;
// cached objects never authorize adoption or replace live UID/revision checks.
func (c *Client) NewResourceInformer(gvr schema.GroupVersionResource, selector string) cache.SharedIndexInformer {
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(c.informerDyn, 5*time.Minute, metav1.NamespaceAll, func(opts *metav1.ListOptions) {
		opts.LabelSelector = selector
	})
	return factory.ForResource(gvr).Informer()
}
