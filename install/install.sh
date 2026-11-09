#!/usr/bin/env bash
# 只安装集群依赖；不安装本机软件、不删除资源、不发布 Loom API/web。
set -euo pipefail
INSTALL_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$INSTALL_DIR"

# 只加载本机信任的 Bash 配置；不把镜像仓库密码放进此文件。
if [[ -f install.local.env ]]; then
  source "$INSTALL_DIR/install.local.env"
fi

# 按需修改这些开关，或在运行时通过同名环境变量覆盖。
KUBE_CONTEXT="${1:-${KUBE_CONTEXT:-}}"
INSTALL_CERT_MANAGER="${INSTALL_CERT_MANAGER:-true}"
INSTALL_ISTIO="${INSTALL_ISTIO:-true}"
INSTALL_PROFILES="${INSTALL_PROFILES:-true}"
INSTALL_NOTEBOOK="${INSTALL_NOTEBOOK:-true}"
INSTALL_PIPELINE="${INSTALL_PIPELINE:-false}"
INSTALL_KSERVE="${INSTALL_KSERVE:-false}"
INSTALL_TRAINER="${INSTALL_TRAINER:-false}"
INSTALL_NOTEBOOK_GATEWAY="${INSTALL_NOTEBOOK_GATEWAY:-false}"
ENABLE_POD_EXEC="${ENABLE_POD_EXEC:-false}"
ENABLE_SPACE_DELETE="${ENABLE_SPACE_DELETE:-false}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-600s}"
# 镜像地址与版本统一维护在 manifests/registry/kustomization.yaml。
# 当前配置使用公开镜像代理（mc 为华为云），不读取 Docker 登录文件、不创建仓库凭据 Secret。

if [[ -z "$KUBE_CONTEXT" ]]; then
  echo '用法：bash install.sh <kubectl-context>' >&2
  exit 1
fi
for flag in "$INSTALL_CERT_MANAGER" "$INSTALL_ISTIO" "$INSTALL_PROFILES" "$INSTALL_NOTEBOOK" "$INSTALL_PIPELINE" "$INSTALL_KSERVE" "$INSTALL_TRAINER" \
  "$INSTALL_NOTEBOOK_GATEWAY" "$ENABLE_POD_EXEC" "$ENABLE_SPACE_DELETE"; do
  if [[ "$flag" != true && "$flag" != false ]]; then
    echo '安装开关只能填写 true 或 false。' >&2
    exit 1
  fi
done
for tool in kubectl; do
  command -v "$tool" >/dev/null || { echo "缺少工具：$tool" >&2; exit 1; }
done
if [[ "$INSTALL_NOTEBOOK_GATEWAY" == true && "$INSTALL_ISTIO" != true ]]; then
  echo '复用已有 Istio 时，请人工合并 Notebook 认证策略，不由脚本覆盖共享 mesh。' >&2
  exit 1
fi
if [[ "$INSTALL_PIPELINE" == true ]]; then
  # 仅检查凭据字段，忽略模板注释中的 REPLACE_ME。
  if [[ ! -f manifests/pipeline/secrets.local.yaml ]] || \
    grep -qE '^[[:space:]]*(username|password|accesskey|secretkey):.*REPLACE_ME' manifests/pipeline/secrets.local.yaml; then
    echo '先复制 pipeline/secrets.example.yaml 为 secrets.local.yaml，填写真实凭据。' >&2
    exit 1
  fi
fi
if [[ -n "${PROFILE_CONTROLLER_IMAGE:-}" ]]; then
  echo '提示：忽略旧 PROFILE_CONTROLLER_IMAGE，镜像来源统一以 manifests/registry/kustomization.yaml 为准。' >&2
fi

# 不改变全局 current-context；后续每一条命令都指向同一目标。
KUBECTL=(kubectl --context "$KUBE_CONTEXT")

apply_kustomization() {
  "${KUBECTL[@]}" apply --server-side --field-manager=loom-installer -k "$1"
}

"${KUBECTL[@]}" cluster-info
echo "目标集群 context：$KUBE_CONTEXT"
echo '请确认已审阅安装清单、默认 StorageClass、组件版本及高信任控制器权限。'
if [[ "${CONFIRM_INSTALL:-false}" != true ]]; then
  read -r -p '输入完整 context 确认安装：' confirmed_context
  [[ "$confirmed_context" == "$KUBE_CONTEXT" ]] || { echo '已取消。'; exit 1; }
fi

echo '1. Namespace'
"${KUBECTL[@]}" apply -f manifests/namespaces.yaml

echo '2. cert-manager 1.21.2'
if [[ "$INSTALL_CERT_MANAGER" == true ]]; then
  apply_kustomization manifests/cert-manager
fi
"${KUBECTL[@]}" wait --for=condition=Established crd/certificates.cert-manager.io --timeout="$WAIT_TIMEOUT"
"${KUBECTL[@]}" rollout status deployment/cert-manager -n cert-manager --timeout="$WAIT_TIMEOUT"
"${KUBECTL[@]}" rollout status deployment/cert-manager-webhook -n cert-manager --timeout="$WAIT_TIMEOUT"
"${KUBECTL[@]}" rollout status deployment/cert-manager-cainjector -n cert-manager --timeout="$WAIT_TIMEOUT"

echo '3. Istio 1.31.3'
if [[ "$INSTALL_ISTIO" == true ]]; then
  apply_kustomization manifests/istio/crds
  "${KUBECTL[@]}" wait --for=condition=Established crd/virtualservices.networking.istio.io crd/authorizationpolicies.security.istio.io --timeout="$WAIT_TIMEOUT"
  if [[ "$INSTALL_NOTEBOOK_GATEWAY" == true ]]; then
    "${KUBECTL[@]}" get secret loom-notebook-tls -n istio-system >/dev/null
    apply_kustomization manifests/istio/notebook-auth
  else
    apply_kustomization manifests/istio/base
  fi
  "${KUBECTL[@]}" rollout status daemonset/istio-cni-node -n kube-system --timeout="$WAIT_TIMEOUT"
fi
"${KUBECTL[@]}" rollout status deployment/istiod -n istio-system --timeout="$WAIT_TIMEOUT"
"${KUBECTL[@]}" rollout status deployment/istio-ingressgateway -n istio-system --timeout="$WAIT_TIMEOUT"

echo '4. Profile Controller v2.0.1 与 Notebook Controller v1.11.1'
apply_kustomization manifests/roles
if [[ "$INSTALL_PROFILES" == true ]]; then
  apply_kustomization manifests/profiles
  "${KUBECTL[@]}" wait --for=condition=Established crd/profiles.kubeflow.org --timeout="$WAIT_TIMEOUT"
  "${KUBECTL[@]}" rollout status deployment/profiles-deployment -n kubeflow --timeout="$WAIT_TIMEOUT"
fi
if [[ "$INSTALL_NOTEBOOK" == true ]]; then
  apply_kustomization manifests/notebook
  "${KUBECTL[@]}" wait --for=condition=Established crd/notebooks.kubeflow.org --timeout="$WAIT_TIMEOUT"
  "${KUBECTL[@]}" rollout status deployment/notebook-controller-deployment -n kubeflow --timeout="$WAIT_TIMEOUT"
fi

if [[ "$INSTALL_PIPELINE" == true ]]; then
  # 这是新安装配置，不把正在使用 SeaweedFS 的集群偷偷切到空 MinIO。
  existing_seaweedfs="$("${KUBECTL[@]}" get deployment seaweedfs -n kubeflow --ignore-not-found -o name)"
  if [[ -n "$existing_seaweedfs" ]]; then
    echo '发现已有 SeaweedFS。请先迁移存储和空间凭据，不能用本脚本直接在线替换。' >&2
    exit 1
  fi
  echo '5. MinIO / KFP 2.18.0 / Argo v4.1.6'
  apply_kustomization manifests/pipeline/crds
  "${KUBECTL[@]}" wait --for=condition=Established crd/workflows.argoproj.io crd/decoratorcontrollers.metacontroller.k8s.io crd/scheduledworkflows.kubeflow.org crd/viewers.kubeflow.org --timeout="$WAIT_TIMEOUT"
  "${KUBECTL[@]}" apply -f manifests/pipeline/secrets.local.yaml
  apply_kustomization manifests/minio
  "${KUBECTL[@]}" rollout status statefulset/minio -n kubeflow --timeout="$WAIT_TIMEOUT"
  apply_kustomization manifests/minio/bootstrap
  "${KUBECTL[@]}" wait --for=condition=Complete job/loom-minio-bootstrap-v4 -n kubeflow --timeout="$WAIT_TIMEOUT"
  # 仅复制独立的日志/产物只读凭据给 API；不复制 root 或任务写入凭据。
  "${KUBECTL[@]}" get secret minio-log-reader -n kubeflow -o 'go-template=apiVersion: v1
kind: Secret
metadata:
  name: loom-log-archive
  namespace: loom-system
type: Opaque
data:
  accesskey: {{index .data "accesskey"}}
  secretkey: {{index .data "secretkey"}}
' | "${KUBECTL[@]}" apply -f -
  "${KUBECTL[@]}" get secret minio-artifact-reader -n kubeflow -o 'go-template=apiVersion: v1
kind: Secret
metadata:
  name: loom-artifact-store
  namespace: loom-system
type: Opaque
data:
  accesskey: {{index .data "accesskey"}}
  secretkey: {{index .data "secretkey"}}
' | "${KUBECTL[@]}" apply -f -
  apply_kustomization manifests/pipeline/base
  "${KUBECTL[@]}" wait --for=condition=Ready certificate/kfp-cache-cert -n kubeflow --timeout="$WAIT_TIMEOUT"
  "${KUBECTL[@]}" rollout status statefulset/metacontroller -n kubeflow --timeout="$WAIT_TIMEOUT"
  for deployment in mysql workflow-controller ml-pipeline metadata-grpc-deployment metadata-envoy-deployment cache-server kubeflow-pipelines-profile-controller; do
    "${KUBECTL[@]}" rollout status "deployment/$deployment" -n kubeflow --timeout="$WAIT_TIMEOUT"
  done
  "${KUBECTL[@]}" apply -f manifests/pipeline-client-rbac.yaml -f manifests/pipeline-access.yaml
fi

if [[ "$INSTALL_KSERVE" == true ]]; then
  echo '6. KServe v0.21.0（Standard）'
  apply_kustomization manifests/kserve/crds
  "${KUBECTL[@]}" wait --for=condition=Established crd/inferenceservices.serving.kserve.io crd/clusterservingruntimes.serving.kserve.io crd/clusterstoragecontainers.serving.kserve.io --timeout="$WAIT_TIMEOUT"
  # 先部署控制器、证书和 webhook，不在 webhook 就绪前创建运行时 CR。
  apply_kustomization manifests/kserve/controller
  "${KUBECTL[@]}" wait --for=condition=Ready certificate --all -n kserve --timeout="$WAIT_TIMEOUT"
  for deployment in kserve-controller-manager kserve-localmodel-controller-manager llmisvc-controller-manager; do
    "${KUBECTL[@]}" rollout status "deployment/$deployment" -n kserve --timeout="$WAIT_TIMEOUT"
  done
  "${KUBECTL[@]}" wait '--for=jsonpath={.webhooks[0].clientConfig.caBundle}' validatingwebhookconfiguration/clusterservingruntime.serving.kserve.io --timeout="$WAIT_TIMEOUT"
  apply_kustomization manifests/kserve/base
fi

if [[ "$INSTALL_TRAINER" == true ]]; then
  echo '7. Trainer v2.3.0 / JobSet v0.12.0'
  apply_kustomization manifests/trainer/crds
  "${KUBECTL[@]}" wait --for=condition=Established crd/trainjobs.trainer.kubeflow.org crd/trainingruntimes.trainer.kubeflow.org crd/clustertrainingruntimes.trainer.kubeflow.org --timeout="$WAIT_TIMEOUT"
  # 先安装控制器和 JobSet 依赖，再提交官方训练运行时。
  apply_kustomization manifests/trainer/controller
  "${KUBECTL[@]}" wait --for=condition=Established crd/jobsets.jobset.x-k8s.io --timeout="$WAIT_TIMEOUT"
  "${KUBECTL[@]}" rollout status deployment/jobset-controller-manager -n kubeflow-system --timeout="$WAIT_TIMEOUT"
  "${KUBECTL[@]}" rollout status deployment/kubeflow-trainer-controller-manager -n kubeflow-system --timeout="$WAIT_TIMEOUT"
  "${KUBECTL[@]}" wait '--for=jsonpath={.webhooks[0].clientConfig.caBundle}' mutatingwebhookconfiguration/defaulter.trainer.kubeflow.org --timeout="$WAIT_TIMEOUT"
  "${KUBECTL[@]}" wait '--for=jsonpath={.webhooks[0].clientConfig.caBundle}' validatingwebhookconfiguration/validator.trainer.kubeflow.org --timeout="$WAIT_TIMEOUT"
  "${KUBECTL[@]}" wait '--for=jsonpath={.webhooks[0].clientConfig.caBundle}' mutatingwebhookconfiguration/jobset-mutating-webhook-configuration --timeout="$WAIT_TIMEOUT"
  "${KUBECTL[@]}" wait '--for=jsonpath={.webhooks[0].clientConfig.caBundle}' validatingwebhookconfiguration/jobset-validating-webhook-configuration --timeout="$WAIT_TIMEOUT"
  apply_kustomization manifests/trainer/runtimes
fi

echo '8. Loom 执行器权限'
"${KUBECTL[@]}" apply -f platform-worker-rbac.yaml
if [[ "$ENABLE_POD_EXEC" == true ]]; then
  "${KUBECTL[@]}" apply -f manifests/pod-exec-rbac.yaml
fi
if [[ "$ENABLE_SPACE_DELETE" == true ]]; then
  "${KUBECTL[@]}" apply -f manifests/space-delete-rbac.yaml
fi
if [[ "$INSTALL_NOTEBOOK_GATEWAY" == true ]]; then
  "${KUBECTL[@]}" apply -f manifests/gateway/notebook.yaml
fi

echo '所选依赖已应用并完成上述就绪等待。Loom API/web、业务库初始化和访问链路仍需单独配置。'
