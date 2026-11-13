package kfpengine

import (
	"fmt"
	"github.com/githubtiger123/loom/engine/kfp/runtimeapi"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"net/url"
)

type RuntimeTransportPolicy struct {
	Endpoint    string
	CAConfigMap string
}

// BindRuntimeTransport adds only trusted transport volumes/environment; the
// compiler's official templates and driver-produced command are left intact.
func BindRuntimeTransport(result *Result, policy RuntimeTransportPolicy) error {
	if result == nil || result.Workflow == nil {
		return fmt.Errorf("compiled Workflow is required")
	}
	endpoint, err := url.Parse(policy.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "" && endpoint.Path != "/" || len(validation.IsDNS1123Subdomain(policy.CAConfigMap)) != 0 {
		return fmt.Errorf("trusted runtime HTTPS origin and CA ConfigMap are required")
	}
	wf := result.Workflow
	for i := range wf.Spec.Templates {
		template := &wf.Spec.Templates[i]
		if template.Container == nil {
			continue
		}
		for _, volume := range template.Volumes {
			if volume.Name == "loom-runtime" || volume.Name == "loom-runtime-ca" {
				return fmt.Errorf("runtime transport volume name is reserved")
			}
		}
		for _, env := range template.Container.Env {
			if env.Name == "LOOM_RUNTIME_ENDPOINT" {
				return fmt.Errorf("runtime transport environment name is reserved")
			}
		}
		for _, mount := range template.Container.VolumeMounts {
			if mount.Name == "loom-runtime" || mount.Name == "loom-runtime-ca" || mount.MountPath == "/var/run/secrets/loom-runtime" || mount.MountPath == "/var/run/secrets/loom-runtime-ca" {
				return fmt.Errorf("runtime transport mount is reserved")
			}
		}
		mode := int32(0444)
		expires := int64(600)
		template.Volumes = append(template.Volumes, corev1.Volume{Name: "loom-runtime", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{DefaultMode: &mode, Sources: []corev1.VolumeProjection{{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Audience: runtimeapi.Audience, ExpirationSeconds: &expires, Path: "token"}}}}}}, corev1.Volume{Name: "loom-runtime-ca", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: policy.CAConfigMap}, Items: []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}}}}})
		template.Container.Env = append(template.Container.Env, corev1.EnvVar{Name: "LOOM_RUNTIME_ENDPOINT", Value: policy.Endpoint})
		template.Container.VolumeMounts = append(template.Container.VolumeMounts, corev1.VolumeMount{Name: "loom-runtime", MountPath: "/var/run/secrets/loom-runtime", ReadOnly: true}, corev1.VolumeMount{Name: "loom-runtime-ca", MountPath: "/var/run/secrets/loom-runtime-ca", ReadOnly: true})
	}
	// Changing the spec invalidates its compiler-only digest. Always freeze the
	// final transport-bound spec; never mutate a plan after its UID is bound.
	return refreshDigest(result)
}
