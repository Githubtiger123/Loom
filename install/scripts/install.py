#!/usr/bin/env python3
"""Prepare pinned manifests, then install explicitly selected cluster dependencies.

No Python Kubernetes client, shell evaluation, package installation or uninstall.
render is local-only (except fetching a missing pinned source checkout).
"""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import subprocess
import sys
import tempfile

try:
    import yaml
except ImportError:
    sys.exit("缺少 PyYAML，请先按 install/README.md 准备 Python 环境。")


ROOT = Path(__file__).resolve().parents[1]
OWNER = "install.loom.io/installation"
MANAGER = "loom-installer"
WEBHOOKS = {"MutatingWebhookConfiguration", "ValidatingWebhookConfiguration"}
WORKLOADS = {"Deployment", "StatefulSet", "DaemonSet"}
BOOTSTRAP_GROUPS = {"cert-manager.io", "networking.istio.io", "security.istio.io"}
FOUNDATION = {
    "Namespace", "ServiceAccount", "ClusterRole", "ClusterRoleBinding", "Role",
    "RoleBinding", "Service", "ConfigMap", "Secret", "PersistentVolumeClaim",
    "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob", "NetworkPolicy",
    "PodDisruptionBudget", "PriorityClass", "HorizontalPodAutoscaler",
}


def run(argv, *, private=False):
    result = subprocess.run([str(arg) for arg in argv], text=True, capture_output=True)
    if result.returncode:
        # kubectl can include entire Secret patches in error messages.
        detail = "（敏感操作，不打印响应；请检查生成文件和集群事件）" if private else result.stderr.strip()
        raise RuntimeError(f"{argv[0]} {argv[1]} 失败：{detail}")
    return result.stdout


def read_yaml(path):
    return [doc for doc in yaml.safe_load_all(path.read_text(encoding="utf-8")) if doc]


def write_yaml(path, docs):
    path.write_text(yaml.safe_dump_all(docs, sort_keys=False, allow_unicode=True), encoding="utf-8")
    path.chmod(0o600)


def identity(doc):
    meta = doc["metadata"]
    return (doc["apiVersion"].split("/")[0] if "/" in doc["apiVersion"] else "",
            doc["kind"], meta.get("namespace", ""), meta["name"])


def resource(api, kind, name, namespace=None, **fields):
    meta = {"name": name}
    if namespace:
        meta["namespace"] = namespace
    return {"apiVersion": api, "kind": kind, "metadata": meta, **fields}


def configured(path):
    cfg = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(cfg, dict):
        raise ValueError("配置必须是 YAML mapping")
    # Reject misspelled keys rather than silently installing the wrong modules.
    schema = {
        "installation": None, "context": None, "timeout_seconds": None,
        "source": {"local_dir"},
        "components": {"cert_manager", "istio", "pipeline", "kserve"},
        "notebook": {"host"}, "identity": {"userid_header", "userid_prefix"},
        "rbac": {"pod_exec", "space_delete_inventory"},
        "gateway": {"enabled", "service_type", "tls_secret", "auth_service", "auth_port"},
    }
    if set(cfg) != set(schema):
        raise ValueError("配置字段不完整或拼写错误，请从 config.yaml 复制")
    for key, keys in schema.items():
        if keys is not None and (not isinstance(cfg[key], dict) or set(cfg[key]) != keys):
            raise ValueError(f"配置 {key} 字段不完整或拼写错误")
    if not isinstance(cfg["installation"], str) or not re.fullmatch(r"[a-z](?:[a-z0-9-]{0,38}[a-z0-9])?", cfg["installation"]):
        raise ValueError("installation 应为 1–40 位小写名称")
    if not isinstance(cfg["context"], str) or not cfg["context"].strip():
        raise ValueError("必须显式填写 context，不会自动使用当前集群")
    if type(cfg["timeout_seconds"]) is not int or not 30 <= cfg["timeout_seconds"] <= 3600:
        raise ValueError("timeout_seconds 应为 30–3600")
    for key in ("cert_manager", "istio"):
        if cfg["components"][key] not in ("install", "reuse"):
            raise ValueError(f"components.{key} 仅支持 install/reuse")
    for section, keys in (("components", ("pipeline", "kserve")),
                          ("rbac", ("pod_exec", "space_delete_inventory")),
                          ("gateway", ("enabled",))):
        for key in keys:
            if type(cfg[section][key]) is not bool:
                raise ValueError(f"{section}.{key} 必须是 YAML boolean")
    host = cfg["notebook"]["host"]
    if not isinstance(host, str) or len(host) > 253 or "." not in host or any(
            not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label)
            for label in host.split(".")):
        raise ValueError("notebook.host 必须是完整小写 DNS hostname，不带协议/端口/*")
    if not isinstance(cfg["identity"]["userid_header"], str) or not re.fullmatch(r"[a-z][a-z0-9-]*", cfg["identity"]["userid_header"]):
        raise ValueError("identity.userid_header 必须是小写 HTTP header 名")
    if cfg["identity"]["userid_header"] in {"host", "cookie", "authorization", "origin", "content-type", "oidc-sub"}:
        raise ValueError("userid_header 不得占用认证、来源或主体等保留 header")
    prefix = cfg["identity"]["userid_prefix"]
    if not isinstance(prefix, str) or any(ord(char) < 32 for char in prefix):
        raise ValueError("userid_prefix 必须是不含控制字符的字符串")
    gate = cfg["gateway"]
    if gate["service_type"] not in ("ClusterIP", "LoadBalancer", "NodePort"):
        raise ValueError("不支持的 gateway.service_type")
    if gate["enabled"] and cfg["components"]["istio"] != "install":
        raise ValueError("复用 Istio 时请人工合并认证入口；脚本不会改写已有 mesh")
    if type(gate["auth_port"]) is not int or not 1 <= gate["auth_port"] <= 65535:
        raise ValueError("gateway.auth_port 不合法")
    if not isinstance(gate["auth_service"], str) or not isinstance(gate["tls_secret"], str) or not re.fullmatch(r"[a-z0-9.-]+", gate["auth_service"]) or not re.fullmatch(
            r"[a-z0-9][a-z0-9.-]*", gate["tls_secret"]):
        raise ValueError("gateway.auth_service/tls_secret 不合法")
    if not isinstance(cfg["source"]["local_dir"], str):
        raise ValueError("source.local_dir 必须是路径字符串")
    return cfg


def pinned_source(cfg, config_path, lock):
    local = cfg["source"]["local_dir"]
    if local:
        source = (config_path.parent / local).resolve()
    else:
        sibling = ROOT.parents[1] / "community-distribution"
        source = sibling if sibling.is_dir() else ROOT / ".sources" / lock["revision"]
        if not source.exists():
            print(f"下载上游锁定 commit {lock['revision']}…", flush=True)
            source.parent.mkdir(parents=True, exist_ok=True)
            source.mkdir()
            run(["git", "init", str(source)])
            run(["git", "-C", source, "remote", "add", "origin", lock["repository"]])
            run(["git", "-C", source, "fetch", "--depth=1", "origin", lock["revision"]])
            run(["git", "-C", source, "checkout", "--detach", "FETCH_HEAD"])
    actual = run(["git", "-C", source, "rev-parse", "HEAD"]).strip()
    if actual != lock["revision"]:
        raise RuntimeError(f"源码 commit 不匹配：{source}；需要 {lock['revision']}")
    if run(["git", "-C", source, "status", "--porcelain", "--untracked-files=all"]).strip():
        raise RuntimeError(f"上游源码有本地改动：{source}；不会覆盖，请使用干净的锁定 checkout")
    return source


def credentials(cfg):
    # Retain across reruns; never generate new passwords over an existing database.
    scope = hashlib.sha256(cfg["context"].encode()).hexdigest()[:16]
    state = ROOT / ".state" / f"{cfg['installation']}-{scope}"
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    state.chmod(0o700)
    path = state / "pipeline-credentials.json"
    if not path.exists():
        values = {"mysql_password": secrets.token_urlsafe(36),
                  "s3_access_key": secrets.token_hex(12), "s3_secret_key": secrets.token_urlsafe(36)}
        with path.open("x", encoding="utf-8") as stream:
            os.chmod(path, 0o600)
            json.dump(values, stream)
    values = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(values, dict) or set(values) != {"mysql_password", "s3_access_key", "s3_secret_key"} or any(
            not isinstance(value, str) or len(value) < 24 for value in values.values()):
        raise RuntimeError(f"凭据文件损坏，不会自动重置：{path}")
    return values


def auth_provider(cfg):
    return {"name": "loom-auth", "envoyExtAuthzHttp": {
        "service": cfg["gateway"]["auth_service"], "port": cfg["gateway"]["auth_port"],
        "pathPrefix": "/verify", "timeout": "5s", "failOpen": False,
        "includeRequestHeadersInCheck": ["host", "authorization", "cookie", "origin",
                                         "sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "upgrade"],
        "headersToUpstreamOnAllow": [cfg["identity"]["userid_header"], "oidc-sub", "cookie", "authorization"],
        "headersToDownstreamOnDeny": ["content-type"],
    }}


def notebook_gateway(cfg):
    selector = {"app": "istio-ingressgateway", "istio": "ingressgateway"}
    host = cfg["notebook"]["host"]
    return [
        resource("networking.istio.io/v1", "Gateway", "kubeflow-gateway", "kubeflow", spec={
            "selector": {"istio": "ingressgateway"}, "servers": [{
                "port": {"number": 443, "name": "https-notebooks", "protocol": "HTTPS"},
                "hosts": [host], "tls": {"mode": "SIMPLE", "credentialName": cfg["gateway"]["tls_secret"]},
            }]}),
        resource("security.istio.io/v1", "AuthorizationPolicy", "loom-notebook-auth", "istio-system", spec={
            "selector": {"matchLabels": selector}, "action": "CUSTOM", "provider": {"name": "loom-auth"},
            "rules": [{"to": [{"operation": {"hosts": [host], "paths": ["/notebook/*"]}}]}]}),
        resource("networking.istio.io/v1", "VirtualService", "loom-notebook-session", "loom-system", spec={
            "hosts": [host], "gateways": ["kubeflow/kubeflow-gateway"], "http": [{
                "match": [{"uri": {"exact": "/api/v1/auth/notebook-session"},
                           "method": {"regex": "POST|OPTIONS"}}],
                "route": [{"destination": {"host": cfg["gateway"]["auth_service"],
                                            "port": {"number": cfg["gateway"]["auth_port"]}}}],
            }]}),
        resource("security.istio.io/v1", "AuthorizationPolicy", "loom-api-from-gateway", "loom-system", spec={
            "selector": {"matchLabels": {"app": "loom-api"}}, "action": "ALLOW", "rules": [{
                "from": [{"source": {"principals": ["cluster.local/ns/istio-system/sa/istio-ingressgateway-service-account"]}}],
                "to": [{"operation": {"ports": [str(cfg["gateway"]["auth_port"])],
                                        "paths": ["/verify/*", "/api/v1/auth/notebook-session"]}}],
            }]}),
        resource("security.istio.io/v1", "PeerAuthentication", "loom-api-mtls", "loom-system", spec={
            "selector": {"matchLabels": {"app": "loom-api"}}, "mtls": {"mode": "STRICT"}}),
        resource("networking.k8s.io/v1", "NetworkPolicy", "loom-api-from-gateway", "loom-system", spec={
            "podSelector": {"matchLabels": {"app": "loom-api"}}, "policyTypes": ["Ingress"],
            "ingress": [{"from": [{"namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "istio-system"}},
                                    "podSelector": {"matchLabels": selector}}],
                         "ports": [{"protocol": "TCP", "port": cfg["gateway"]["auth_port"]}]}]}),
    ]


def transform(component, docs, cfg, creds):
    result = []
    for doc in docs:
        kind, meta = doc["kind"], doc["metadata"]
        name = meta["name"]
        meta.get("annotations", {}).pop("kubectl.kubernetes.io/last-applied-configuration", None)
        doc.pop("status", None)
        if kind == "ClusterRole" and doc.get("aggregationRule"):
            doc.pop("rules", None) # aggregation controller, not this field manager, owns these rules
        if kind == "Application" or (kind == "CustomResourceDefinition" and name == "applications.app.k8s.io"):
            continue
        if component == "profiles":
            if kind == "Namespace":
                continue
            if meta.get("namespace") == "profiles-system":
                meta["namespace"] = "kubeflow"
            for subject in doc.get("subjects", []):
                if subject.get("namespace") == "profiles-system":
                    subject["namespace"] = "kubeflow"
            if kind == "ConfigMap" and name.startswith("profiles-config-"):
                doc["data"].update({
                    "ADMIN": "", "WORKLOAD_IDENTITY": "",
                    "USERID_HEADER": cfg["identity"]["userid_header"], "USERID_PREFIX": cfg["identity"]["userid_prefix"],
                    "ISTIO_INGRESS_GATEWAY_PRINCIPAL": "cluster.local/ns/istio-system/sa/istio-ingressgateway-service-account",
                    "NOTEBOOK_CONTROLLER_PRINCIPAL": "cluster.local/ns/kubeflow/sa/notebook-controller-service-account",
                    "KFP_UI_PRINCIPAL": "cluster.local/ns/kubeflow/sa/ml-pipeline-ui",
                })
            if kind == "ConfigMap" and name.startswith("profiles-namespace-labels-data-"):
                labels = {"app.kubernetes.io/part-of": "kubeflow-profile", "istio-injection": "enabled",
                          "pod-security.kubernetes.io/enforce": "baseline"}
                if cfg["components"]["pipeline"]:
                    labels["pipelines.kubeflow.org/enabled"] = "true"
                if cfg["components"]["kserve"]:
                    labels["serving.kubeflow.org/inferenceservice"] = "enabled"
                doc["data"]["namespace-labels.yaml"] = yaml.safe_dump(labels)
        if component == "notebook" and kind == "ConfigMap" and name.startswith("notebook-controller-config-"):
            doc["data"].update({"USE_ISTIO": "true", "ISTIO_GATEWAY": "kubeflow/kubeflow-gateway",
                                "ISTIO_HOST": cfg["notebook"]["host"], "CLUSTER_DOMAIN": "cluster.local",
                                "ENABLE_CULLING": "false"})
        if component == "istio":
            if kind == "Gateway":
                continue # Never publish upstream wildcard plaintext listeners.
            if kind == "Sidecar":
                doc["spec"]["egress"] = [{"hosts": ["*/*"]}]
            if kind == "AuthorizationPolicy" and name == "istio-ingressgateway":
                doc["spec"]["rules"] = []
                if cfg["gateway"]["enabled"]:
                    host = cfg["notebook"]["host"]
                    doc["spec"]["rules"] = [{"to": [{"operation": {"hosts": [host], "paths": ["/notebook/*"]}}]},
                        {"to": [{"operation": {"hosts": [host], "paths": ["/api/v1/auth/notebook-session"],
                                                 "methods": ["POST", "OPTIONS"]}}]}]
            if kind == "Service" and name == "istio-ingressgateway":
                doc["spec"]["type"] = cfg["gateway"]["service_type"]
            if kind == "ConfigMap" and name == "istio" and cfg["gateway"]["enabled"]:
                mesh = yaml.safe_load(doc["data"]["mesh"])
                providers = mesh.setdefault("extensionProviders", [])
                providers[:] = [provider for provider in providers if provider["name"] != "loom-auth"]
                providers.append(auth_provider(cfg))
                doc["data"]["mesh"] = yaml.safe_dump(mesh)
        if component == "pipeline":
            if kind == "VirtualService":
                continue # KFP UI is not an unauthenticated Loom browser entry point.
            if kind == "Secret" and name == "mysql-secret":
                doc.pop("data", None)
                doc["stringData"] = {"username": "root", "password": creds["mysql_password"]}
            if kind == "Secret" and name == "mlpipeline-minio-artifact":
                doc.pop("data", None)
                doc["stringData"] = {"accesskey": creds["s3_access_key"], "secretkey": creds["s3_secret_key"]}
            if kind == "Deployment" and name == "mysql":
                container = next(c for c in doc["spec"]["template"]["spec"]["containers"] if c["name"] == "mysql")
                container["env"] = [env for env in container.get("env", []) if env["name"] not in
                                    ("MYSQL_ALLOW_EMPTY_PASSWORD", "MYSQL_ROOT_PASSWORD", "MYSQL_ROOT_HOST")]
                container["env"].extend([
                    {"name": "MYSQL_ROOT_PASSWORD", "valueFrom": {"secretKeyRef": {"name": "mysql-secret", "key": "password"}}},
                    {"name": "MYSQL_ROOT_HOST", "value": "%"},
                ])
            if kind == "Deployment" and name == "metadata-envoy-deployment":
                # Upstream disables its sidecar. Enable it so Loom's scoped mTLS
                # AuthorizationPolicy actually protects the grpc-web endpoint.
                template_meta = doc["spec"]["template"]["metadata"]
                template_meta.setdefault("labels", {})["sidecar.istio.io/inject"] = "true"
                template_meta.setdefault("annotations", {})["sidecar.istio.io/inject"] = "true"
            if kind == "ConfigMap" and name == "pipeline-install-config":
                doc["data"].update({"USERID_HEADER": cfg["identity"]["userid_header"],
                                    "USERID_PREFIX": cfg["identity"]["userid_prefix"]})
            if kind == "ConfigMap" and name == "workflow-controller-configmap":
                repository = yaml.safe_load(doc["data"]["artifactRepository"])
                repository["archiveLogs"] = True
                doc["data"]["artifactRepository"] = yaml.safe_dump(repository)
        if component == "kserve" and kind == "ConfigMap" and name == "inferenceservice-config":
            deploy = json.loads(doc["data"]["deploy"])
            deploy["defaultDeploymentMode"] = "Standard"
            doc["data"]["deploy"] = json.dumps(deploy)
            ingress = json.loads(doc["data"]["ingress"])
            ingress.update({"ingressGateway": "kubeflow/kubeflow-gateway", "ingressDomain": cfg["notebook"]["host"],
                            "urlScheme": "https"})
            doc["data"]["ingress"] = json.dumps(ingress)
        result.append(doc)
    # Post-render ConfigMap changes need a pod rollout even though upstream hash names stay stable.
    checksum = hashlib.sha256(json.dumps([d for d in result if d["kind"] == "ConfigMap"], sort_keys=True).encode()).hexdigest()
    for doc in result:
        if doc["kind"] in WORKLOADS:
            template = doc["spec"]["template"].setdefault("metadata", {})
            template.setdefault("annotations", {})["install.loom.io/config-checksum"] = checksum
        if doc["kind"] == "Secret" and "stringData" in doc:
            # data works predictably with server-side apply; stringData does not.
            doc["data"] = {key: base64.b64encode(value.encode()).decode() for key, value in doc.pop("stringData").items()}
    # Fail locally if an upstream layout ever changes, rather than silently
    # shipping unpatched credentials/controller settings under a new lock.
    required = {
        "profiles": [("ConfigMap", "profiles-config-"), ("ConfigMap", "profiles-namespace-labels-data-")],
        "notebook": [("ConfigMap", "notebook-controller-config-")],
        "pipeline": [("Secret", "mysql-secret"), ("Secret", "mlpipeline-minio-artifact"),
                     ("Deployment", "mysql"), ("ConfigMap", "workflow-controller-configmap")],
        "kserve": [("ConfigMap", "inferenceservice-config")],
        "istio": [("ConfigMap", "istio"), ("Service", "istio-ingressgateway"),
                  ("AuthorizationPolicy", "istio-ingressgateway")],
    }
    for kind, prefix in required.get(component, []):
        if not any(doc["kind"] == kind and doc["metadata"]["name"].startswith(prefix) for doc in result):
            raise RuntimeError(f"上游布局不匹配，缺少 {component}: {kind}/{prefix}")
    return result


def prepare(cfg, config_path, lock):
    source = pinned_source(cfg, config_path, lock)
    creds = credentials(cfg) if cfg["components"]["pipeline"] else {}
    paths = []
    if cfg["components"]["cert_manager"] == "install":
        paths.append(("cert-manager", "common/cert-manager/base"))
    if cfg["components"]["istio"] == "install":
        paths.extend([("istio-crds", "common/istio/istio-crds/base"),
                      ("istio-namespace", "common/istio/istio-namespace/base"),
                      ("istio", "common/istio/istio-install/base")])
    paths.extend([("roles", "common/kubeflow-roles/base"),
                  ("profiles", "applications/dashboard/upstream/profile-controller/base"),
                  ("notebook", "applications/notebooks-v1/upstream/notebook-controller/overlays/kubeflow")])
    if cfg["components"]["pipeline"]:
        paths.append(("pipeline", "applications/pipeline/overlays"))
    if cfg["components"]["kserve"]:
        paths.append(("kserve", "applications/kserve/kserve"))
    bundles = []
    for component, path in paths:
        print(f"准备 {component}…", flush=True)
        raw = run(["kustomize", "build", str(source / path)])
        docs = [doc for doc in yaml.safe_load_all(raw) if doc]
        bundles.append((component, transform(component, docs, cfg, creds)))
    # The role file is a local resource; upstream namespace default NetworkPolicies are not imported.
    bundles.append(("istio-roles", read_yaml(source / "common/istio/kubeflow-istio-resources/base/cluster-roles.yaml")))
    bundles.append(("issuer", read_yaml(source / "common/cert-manager/overlays/kubeflow/cluster-issuer.yaml")))
    rbac = read_yaml(ROOT / "platform-worker-rbac.yaml")
    for flag, filename in (("pod_exec", "pod-exec-rbac.yaml"), ("space_delete_inventory", "space-delete-rbac.yaml")):
        if cfg["rbac"][flag]:
            rbac.extend(read_yaml(ROOT / "manifests" / filename))
    if cfg["components"]["pipeline"]:
        rbac.extend(read_yaml(ROOT / "manifests/pipeline-client-rbac.yaml"))
        rbac.extend(read_yaml(ROOT / "manifests/pipeline-access.yaml"))
    bundles.append(("loom-rbac", rbac))
    if cfg["gateway"]["enabled"]:
        bundles.append(("notebook-gateway", notebook_gateway(cfg)))

    namespaces = {d["metadata"]["name"]: d for d in read_yaml(ROOT / "manifests/namespaces.yaml")}
    cleaned = []
    for component, docs in bundles:
        rest = []
        for doc in docs:
            if doc["kind"] == "Namespace":
                name = doc["metadata"]["name"]
                if name == "kube-system":
                    continue
                namespaces.setdefault(name, doc)
            else:
                rest.append(doc)
        cleaned.append((component, rest))
    bundles = [("namespaces", list(namespaces.values())), *cleaned]
    seen = set()
    for _, docs in bundles:
        for doc in docs:
            key = identity(doc)
            if key in seen:
                raise RuntimeError(f"重复资源：{key}")
            seen.add(key)
            if doc["kind"] == "ClusterRole" and doc.get("aggregationRule"):
                doc.pop("rules", None)
            doc["metadata"].setdefault("labels", {})[OWNER] = cfg["installation"]
            doc["metadata"]["labels"]["app.kubernetes.io/managed-by"] = MANAGER

    output_root = ROOT / ".generated"
    output_root.mkdir(exist_ok=True, mode=0o700)
    output = Path(tempfile.mkdtemp(prefix=f"{cfg['installation']}-", dir=output_root))
    stages = []
    for component, docs in bundles:
        # CRDs -> built-in resources/controllers -> cert-manager/mesh configuration ->
        # admission webhooks -> other CRs. No catch-all retry loop hiding errors.
        groups = [[], [], [], [], []]
        for doc in docs:
            kind = doc["kind"]
            group = doc["apiVersion"].split("/")[0]
            slot = 0 if kind == "CustomResourceDefinition" else 1 if kind in FOUNDATION else (
                2 if group in BOOTSTRAP_GROUPS else 3 if kind in WEBHOOKS else 4)
            groups[slot].append(doc)
        # Certificates must precede waiting on controllers that mount their Secret.
        for slot in (0, 1, 2, 3, 4):
            if groups[slot]:
                filename = f"{len(stages):02d}-{component}-{slot}.yaml"
                write_yaml(output / filename, groups[slot])
                digest = hashlib.sha256((output / filename).read_bytes()).hexdigest()
                stages.append({"component": component, "slot": slot, "file": filename, "sha256": digest})
    digest = hashlib.sha256(json.dumps(cfg, sort_keys=True).encode()).hexdigest()
    (output / "plan.json").write_text(json.dumps({"context": cfg["context"], "config_sha256": digest,
                                                 "lock": lock, "stages": stages}, indent=2), encoding="utf-8")
    print(f"资源已生成：{output}（含 Secret，不要提交/公开）", flush=True)
    return output, stages


class Cluster:
    def __init__(self, cfg):
        self.cfg = cfg
        self.timeout = f"{cfg['timeout_seconds']}s"

    def command(self, *args):
        request_timeout = "0" if args[0] in ("wait", "rollout") else "30s"
        return run(["kubectl", "--context", self.cfg["context"], f"--request-timeout={request_timeout}", *args], private=True)

    def existing(self, doc):
        # Only called after this resource's CRD has been established.
        group, kind, namespace, name = identity(doc)
        args = ["get", f"{kind.lower()}.{group}" if group else kind.lower(), name, "--ignore-not-found", "-o", "json"]
        if namespace:
            args.extend(["-n", namespace])
        raw = self.command(*args)
        return json.loads(raw) if raw.strip() else None

    def preflight(self):
        # read-only until explicit confirmation; an exact context is always passed.
        nodes = json.loads(self.command("get", "nodes", "-o", "json"))["items"]
        if not any(any(c["type"] == "Ready" and c["status"] == "True" for c in n.get("status", {}).get("conditions", [])) for n in nodes):
            raise RuntimeError("没有 Ready 节点")
        classes = json.loads(self.command("get", "storageclasses", "-o", "json"))["items"]
        defaults = [sc for sc in classes if any(sc["metadata"].get("annotations", {}).get(key) == "true" for key in
                    ("storageclass.kubernetes.io/is-default-class", "storageclass.beta.kubernetes.io/is-default-class"))]
        if len(defaults) != 1:
            raise RuntimeError("需要且只能有一个默认 StorageClass；Loom 当前 PVC 未指定 storageClassName")
        print(f"目标 context: {self.cfg['context']}；默认存储: {defaults[0]['metadata']['name']}", flush=True)
        if self.cfg["gateway"]["enabled"]:
            secret = self.existing(resource("v1", "Secret", self.cfg["gateway"]["tls_secret"], "istio-system"))
            if not secret or secret.get("type") != "kubernetes.io/tls" or not all(
                    secret.get("data", {}).get(key) for key in ("tls.crt", "tls.key")):
                raise RuntimeError("需事先在 istio-system 准备 Notebook TLS Secret；不会生成假证书")

    def apply(self, path):
        docs = read_yaml(path)
        for doc in docs:
            existing = self.existing(doc)
            if not existing:
                # A missing Secret with surviving PVC is not a fresh database install.
                if doc["kind"] == "Secret" and doc["metadata"]["name"] in ("mysql-secret", "mlpipeline-minio-artifact"):
                    pvc_name = "mysql-pv-claim" if doc["metadata"]["name"] == "mysql-secret" else "seaweedfs-pvc"
                    pvc = self.existing(resource("v1", "PersistentVolumeClaim", pvc_name, "kubeflow"))
                    if pvc:
                        raise RuntimeError(f"{pvc_name} 已存在但凭据 Secret 缺失；请先恢复原凭据，不自动重置密码")
                continue
            labels = existing["metadata"].get("labels", {})
            if doc["kind"] == "Namespace" and not labels.get(OWNER):
                desired = doc["metadata"]["labels"]
                if any(key in labels and labels[key] != value for key, value in desired.items()
                       if key not in (OWNER, "app.kubernetes.io/managed-by")):
                    raise RuntimeError(f"已有 Namespace 标签不兼容：{doc['metadata']['name']}；不会覆盖现有隔离策略")
                # Namespaces may be pre-created for TLS credentials. Only add compatible
                # metadata; other existing objects still require explicit installer ownership.
                continue
            if labels.get(OWNER) != self.cfg["installation"] or labels.get("app.kubernetes.io/managed-by") != MANAGER:
                raise RuntimeError(f"拒绝接管已有资源：{'/'.join(identity(doc))}；共享组件请选 reuse")
            if doc["kind"] == "Secret" and doc.get("data") and existing.get("data") != doc["data"]:
                raise RuntimeError(f"凭据与集群不一致：{doc['metadata']['name']}；恢复 .state，不自动轮换")
        self.command("apply", "--server-side", "--field-manager", MANAGER, "-f", str(path))
        for doc in docs:
            if doc["kind"] == "CustomResourceDefinition":
                self.command("wait", "--for=condition=Established", f"crd/{doc['metadata']['name']}", f"--timeout={self.timeout}")

    def ready(self, docs):
        for doc in docs:
            meta = doc["metadata"]
            if doc["kind"] in WORKLOADS:
                self.command("rollout", "status", f"{doc['kind']}/{meta['name']}", "-n", meta["namespace"], f"--timeout={self.timeout}")
            if doc["kind"] in ("Certificate", "Issuer", "ClusterIssuer") and doc["apiVersion"].startswith("cert-manager.io/"):
                args = ["wait", "--for=condition=Ready", f"{doc['kind']}/{meta['name']}", f"--timeout={self.timeout}"]
                if meta.get("namespace"):
                    args.extend(["-n", meta["namespace"]])
                self.command(*args)
            if doc["kind"] in WEBHOOKS:
                for index, hook in enumerate(doc.get("webhooks", [])):
                    if hook.get("clientConfig", {}).get("service"):
                        self.command("wait", f"--for=jsonpath={{.webhooks[{index}].clientConfig.caBundle}}",
                                     f"{doc['kind']}/{meta['name']}", f"--timeout={self.timeout}")

    def reuse(self):
        if self.cfg["components"]["cert_manager"] == "reuse":
            for name in ("certificates.cert-manager.io", "issuers.cert-manager.io", "clusterissuers.cert-manager.io"):
                self.command("wait", "--for=condition=Established", f"crd/{name}", f"--timeout={self.timeout}")
            for name in ("cert-manager", "cert-manager-webhook", "cert-manager-cainjector"):
                self.command("rollout", "status", f"deployment/{name}", "-n", "cert-manager", f"--timeout={self.timeout}")
        if self.cfg["components"]["istio"] == "reuse":
            for name in ("gateways.networking.istio.io", "virtualservices.networking.istio.io", "authorizationpolicies.security.istio.io"):
                self.command("wait", "--for=condition=Established", f"crd/{name}", f"--timeout={self.timeout}")
            self.command("rollout", "status", "deployment/istiod", "-n", "istio-system", f"--timeout={self.timeout}")


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description="Loom 锁定版本的集群依赖安装器；不部署 Loom API/web、不卸载")
    parser.add_argument("action", choices=("install", "render"), nargs="?", default="install")
    parser.add_argument("--config", type=Path, default=ROOT / "config.local.yaml")
    parser.add_argument("--yes", action="store_true", help="跳过交互确认，仍必须在配置中指定 context")
    parser.add_argument("--bundle", type=Path, help="安装先前 render 生成的目录；不下载源码或重新渲染，适合离线搬运")
    args = parser.parse_args()
    if args.bundle and args.action != "install":
        raise ValueError("--bundle 只能用于 install")
    for tool in (*([] if args.bundle else ["git", "kustomize"]), *(["kubectl"] if args.action == "install" else [])):
        if not shutil.which(tool):
            raise RuntimeError(f"缺少 {tool}，请先准备工具，不会自动安装")
    config_path = args.config.resolve()
    cfg = configured(config_path)
    lock = json.loads((ROOT / "versions.lock.json").read_text(encoding="utf-8"))
    if args.bundle:
        output = args.bundle.resolve()
        plan = json.loads((output / "plan.json").read_text(encoding="utf-8"))
        digest = hashlib.sha256(json.dumps(cfg, sort_keys=True).encode()).hexdigest()
        if plan["context"] != cfg["context"] or plan["config_sha256"] != digest or plan["lock"] != lock:
            raise ValueError("bundle 与当前配置/版本/context 不一致，请重新 render")
        stages = plan["stages"]
        for stage in stages:
            if (output / stage["file"]).resolve().parent != output:
                raise ValueError("bundle 中的文件路径越界")
            if hashlib.sha256((output / stage["file"]).read_bytes()).hexdigest() != stage["sha256"]:
                raise ValueError("bundle 资源已被修改，请通过配置重新 render")
    else:
        output, stages = prepare(cfg, config_path, lock)
    if args.action == "render":
        return
    cluster = Cluster(cfg)
    cluster.preflight()
    print("即将安装高信任 Profile 控制器和所选依赖；不会删除现有资源。", flush=True)
    if not args.yes and input(f"输入完整 context {cfg['context']} 确认：").strip() != cfg["context"]:
        raise RuntimeError("已取消，未应用集群资源")
    cluster.reuse()
    pending = []
    previous = None
    for stage in stages:
        # A bundle's certificates/custom resources must exist before checking its controllers.
        if previous is not None and stage["component"] != previous:
            cluster.ready(pending)
            pending = []
        if stage["slot"] in (3, 4):
            cluster.ready(pending)
            pending = []
        path = output / stage["file"]
        print(f"安装 {stage['file']}…", flush=True)
        cluster.apply(path)
        pending.extend(read_yaml(path))
        previous = stage["component"]
    cluster.ready(pending)
    print("所选集群依赖已应用并通过控制器就绪等待。尚需配置/部署 Loom 服务、业务数据库及浏览器访问入口。")


if __name__ == "__main__":
    try:
        main()
    except yaml.YAMLError:
        print("安装停止：YAML 无法解析；为避免泄露 Secret，不打印原始行，请在本地检查配置/生成文件。", file=sys.stderr)
        sys.exit(1)
    except (RuntimeError, ValueError, KeyError, OSError) as error:
        # Do not dump tracebacks containing resource/credential values.
        print(f"安装停止：{error}", file=sys.stderr)
        sys.exit(1)
