"""KFP metacontroller hook for MinIO (not a host-side installation script).

Keep official KFP/Argo execution; replace only SeaweedFS-specific provisioning.
Root credentials never become workload attachments. Each namespace gets a
separate MinIO user; its password is bound to namespace UID. No public bucket
policy is created. Recreating a namespace rotates the old account's password.
"""
import base64
import hashlib
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import re
import subprocess
import tempfile
import threading

ENDPOINT = "http://minio.kubeflow.svc.cluster.local:9000"
BUCKET = "mlpipeline"
CLUSTER = os.environ.get("LOG_ARCHIVE_CLUSTER", "default")
ROOT_USER = os.environ["MINIO_ROOT_USER"]
ROOT_PASSWORD = os.environ["MINIO_ROOT_PASSWORD"]
MC = "/tools/mc"
LOCK = threading.Lock()
SEGMENT = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")


def mc(*args):
    # Do not relay mc output/argv: admin operations carry credentials.
    subprocess.run([MC, *args], check=True, stdout=subprocess.DEVNULL,
                   stderr=subprocess.DEVNULL, timeout=15)


def attach_policy(user):
    try:
        mc("admin", "policy", "attach", "local", user, "--user", user)
    except subprocess.CalledProcessError:
        # Some mc/server versions reject an already-attached policy as a
        # no-net-effect update. Verify the real state, never ignore all errors.
        result = subprocess.run([MC, "admin", "user", "info", "local", user, "--json"],
                                check=True, stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, timeout=15)
        if user not in json.loads(result.stdout).get("policyName", "").split(","):
            raise ValueError("MinIO policy attachment failed")


def provision(namespace, uid, existing):
    identity = (namespace + ":" + uid).encode()
    user = "kfp-" + base64.b32encode(hashlib.sha256(namespace.encode()).digest()).decode().lower()[:16]
    password = hmac.new(ROOT_PASSWORD.encode(), identity, hashlib.sha256).hexdigest()[:40]
    data = {"accesskey": base64.b64encode(user.encode()).decode(),
            "secretkey": base64.b64encode(password.encode()).decode()}
    if existing and existing.get("data") != data:
        # Compare before any MinIO mutation, not after rotating a live password.
        raise ValueError("existing namespace storage credentials require migration")
    paths = [f"private-artifacts/{namespace}", f"loom/pipelines/{namespace}",
             f"logs/{CLUSTER}/{namespace}"]
    policy = {
        "Version": "2012-10-17",
        "Statement": [
            {"Effect": "Allow", "Action": ["s3:GetBucketLocation"],
             "Resource": [f"arn:aws:s3:::{BUCKET}"]},
            {"Effect": "Allow", "Action": ["s3:ListBucket"],
             "Resource": [f"arn:aws:s3:::{BUCKET}"],
             "Condition": {"StringLike": {"s3:prefix": [p + "/*" for p in paths]}}},
            {"Effect": "Allow", "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject",
                                             "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"],
             "Resource": [f"arn:aws:s3:::{BUCKET}/{p}/*" for p in paths]},
        ],
    }
    with LOCK:
        with tempfile.NamedTemporaryFile(mode="w", suffix=".json") as config:
            json.dump(policy, config)
            config.flush()
            mc("admin", "policy", "create", "local", user, config.name)
        mc("admin", "user", "add", "local", user, password)
        attach_policy(user)
    return data


def sync(parent, attachments):
    meta = parent.get("metadata", {})
    if meta.get("deletionTimestamp"):
        # Finalization/credential retirement is an explicit operations workflow.
        return {"status": {}, "attachments": []}
    if meta.get("labels", {}).get("pipelines.kubeflow.org/enabled") != "true":
        return {"status": {}, "attachments": []}
    namespace, uid = meta.get("name", ""), meta.get("uid", "")
    if not re.fullmatch(r"[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?", namespace) or not SEGMENT.fullmatch(uid):
        raise ValueError("invalid namespace identity")
    existing = attachments.get("Secret.v1", {}).get(f"{namespace}/mlpipeline-minio-artifact")
    data = provision(namespace, uid, existing)
    provider = {"default": {
        "endpoint": "minio.kubeflow.svc.cluster.local:9000",
        "region": "us-east-1", "disableSSL": True, "forcePathStyle": True,
        "credentials": {"secretRef": {"secretName": "mlpipeline-minio-artifact",
                                      "accessKeyKey": "accesskey", "secretKeyKey": "secretkey"}},
    }}
    repository = {
        "archiveLogs": True,
        "s3": {
            "endpoint": "minio.kubeflow.svc.cluster.local:9000", "bucket": BUCKET,
            # Argo appends /main.log to this archive directory. The immutable KFP
            # run UUID survives business-name reuse and KFP record deletion.
            "keyFormat": "logs/" + CLUSTER + "/" + namespace +
                         "/{{workflow.labels.pipeline/runid}}/{{pod.name}}",
            "insecure": True,
            "accessKeySecret": {"name": "mlpipeline-minio-artifact", "key": "accesskey"},
            "secretKeySecret": {"name": "mlpipeline-minio-artifact", "key": "secretkey"},
        },
    }

    def resource(kind, name, **fields):
        return {"apiVersion": "v1", "kind": kind,
                "metadata": {"name": name, "namespace": namespace}, **fields}

    desired = [
        resource("Secret", "mlpipeline-minio-artifact", type="Opaque", data=data),
        resource("ConfigMap", "kfp-launcher", data={
            "defaultPipelineRoot": f"s3://{BUCKET}/private-artifacts/{namespace}/v2/artifacts",
            "clusterDomain": ".svc.cluster.local",
            "providers": json.dumps({"s3": provider, "minio": provider}),
        }),
        resource("ConfigMap", "metadata-grpc-configmap", data={
            "METADATA_GRPC_SERVICE_HOST": "metadata-grpc-service.kubeflow",
            "METADATA_GRPC_SERVICE_PORT": "8080",
        }),
        resource("ConfigMap", "artifact-repositories", data={"default-namespaced": json.dumps(repository)}),
    ]
    desired[-1]["metadata"]["annotations"] = {
        "workflows.argoproj.io/default-artifact-repository": "default-namespaced"}
    observed = all(attachments.get(kind, {}).get(f"{namespace}/{name}") for kind, name in [
        ("Secret.v1", "mlpipeline-minio-artifact"), ("ConfigMap.v1", "kfp-launcher"),
        ("ConfigMap.v1", "metadata-grpc-configmap"), ("ConfigMap.v1", "artifact-repositories")])
    return {"status": {"kubeflow-pipelines-ready": "True" if observed else "False"}, "attachments": desired}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass  # Neither request attachments nor errors may expose Secrets.

    def do_GET(self):
        self.send_response(200 if self.path == "/healthz" else 404)
        self.end_headers()

    def do_POST(self):
        if self.path != "/sync":
            self.send_error(404)
            return
        try:
            self.connection.settimeout(60)
            size = int(self.headers.get("Content-Length", "0"))
            if not 0 < size <= 2 * 1024 * 1024:
                raise ValueError("request size")
            body = json.loads(self.rfile.read(size))
            result = sync(body["object"], body["attachments"])
            encoded = json.dumps(result).encode()
        except Exception:
            # Fail closed: never return an empty successful attachment list on
            # storage errors (metacontroller could otherwise remove resources).
            self.send_error(503, "MinIO provisioning unavailable; check credentials and namespace migration")
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)


if __name__ == "__main__":
    if not SEGMENT.fullmatch(CLUSTER):
        raise ValueError("invalid archive cluster")
    mc("alias", "set", "local", ENDPOINT, ROOT_USER, ROOT_PASSWORD)
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
