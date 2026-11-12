"""Loom Python-v1 launcher. Embedded verbatim; no KFP API or MLMD dependency.

The component image supplies Python 3 and its compiled SDK command (including
the SDK's optional pip bootstrap). Argo handles file transport outside main.
"""
import base64
import hashlib
import json
import math
import os
import pathlib
import signal
import subprocess
import sys


def read_json(path):
    with open(path, "r", encoding="utf-8") as stream:
        return json.load(stream)


def encode(value):
    return json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(",", ":"))


def scalar(kind, value):
    if kind == "STRING":
        valid = isinstance(value, str)
    elif kind == "BOOLEAN":
        valid = type(value) is bool
    elif kind == "NUMBER_INTEGER":
        valid = type(value) is int and -(2**63) <= value < 2**63
    else:
        valid = type(value) in (int, float) and math.isfinite(value)
    if not valid or len(encode(value).encode()) > 8192:
        raise ValueError("component parameter has invalid type or size")
    return value


def port_path(direction, names, name):
    return "/tmp/loom/" + direction + "/p%02d/data" % sorted(names).index(name)


def artifact(name, port, path, metadata=None):
    return {"name": name, "uri": path, "type": port["artifactType"], "metadata": metadata or {}}


def run():
    spec = json.loads(base64.b64decode(os.environ["LOOM_TASK"]))
    rt = spec["runtime"]
    root = json.loads(base64.b64decode(os.environ["LOOM_ROOT"]))
    upstream = {}
    for name, env in spec["upstream"].items():
        upstream[name] = json.loads(base64.b64decode(os.environ[env]))
    parameters = {}
    for name, binding in rt["parameters"].items():
        if "root" in binding:
            value = root[binding["root"]]
        elif "task" in binding:
            value = upstream[binding["task"]]["parameters"][binding["output"]]
        else:
            value = binding["value"]
        parameters[name] = scalar(rt["inputs"]["parameters"][name]["parameterType"], value)
    inputs = {"parameterValues": parameters, "artifacts": {}}
    for name, binding in rt["artifacts"].items():
        path = port_path("inputs", rt["inputs"]["artifacts"], name)
        metadata = upstream[binding["task"]]["artifacts"][binding["output"]]["metadata"]
        inputs["artifacts"][name] = {"artifacts": [artifact(name, rt["inputs"]["artifacts"][name], path, metadata)]}
    outputs = {"parameters": {}, "artifacts": {}, "outputFile": "/tmp/loom/executor-output.json"}
    for name in rt["outputs"].get("parameters", {}):
        path = "/tmp/loom/parameters/p%02d" % sorted(rt["outputs"]["parameters"]).index(name)
        pathlib.Path(path).parent.mkdir(parents=True, exist_ok=True)
        outputs["parameters"][name] = {"outputFile": path}
    for name, port in rt["outputs"].get("artifacts", {}).items():
        path = port_path("outputs", rt["outputs"]["artifacts"], name)
        pathlib.Path(path).parent.mkdir(parents=True, exist_ok=True)
        outputs["artifacts"][name] = {"artifacts": [artifact(name, port, path)]}
    pathlib.Path("/tmp/loom").mkdir(parents=True, exist_ok=True)
    executor_input = encode({"inputs": inputs, "outputs": outputs})
    placeholder = "{" + "{" + "$" + "}" + "}"
    # The full placeholder must be an argv element, not shell source or a
    # substring: no root value can become executable source via substitution.
    argv = [executor_input if value == placeholder else value for value in spec["command"] + spec["args"]]
    process = subprocess.Popen(argv, start_new_session=True)
    def forward(sig, _frame):
        try:
            os.killpg(process.pid, sig)
        except ProcessLookupError:
            pass
    signal.signal(signal.SIGTERM, forward)
    signal.signal(signal.SIGINT, forward)
    code = process.wait()
    if code:
        return code if code > 0 else 128 - code
    result = read_json(outputs["outputFile"]) if os.path.exists(outputs["outputFile"]) else {}
    bundle = {"protocol": "loom-python-output-v1", "parameters": {}, "artifacts": {}}
    for name, port in rt["outputs"].get("parameters", {}).items():
        values = result.get("parameterValues", {})
        if name in values:
            value = values[name]
        else:
            text = pathlib.Path(outputs["parameters"][name]["outputFile"]).read_text()
            value = text if port["parameterType"] == "STRING" else json.loads(text)
        bundle["parameters"][name] = scalar(port["parameterType"], value)
    for name, port in rt["outputs"].get("artifacts", {}).items():
        path = outputs["artifacts"][name]["artifacts"][0]["uri"]
        reported = result.get("artifacts", {}).get(name, {}).get("artifacts", [])
        if len(reported) > 1:
            raise ValueError("artifact lists are not supported")
        metadata = reported[0].get("metadata", {}) if reported else {}
        if reported and reported[0].get("uri", path) != path:
            raise ValueError("component relocated a managed artifact")
        if port["artifactType"]["schemaTitle"] == "system.Metrics":
            for key, value in metadata.items():
                if not isinstance(key, str) or len(key) > 128 or type(value) not in (float, int) or not math.isfinite(value):
                    raise ValueError("only finite scalar metrics are supported")
            pathlib.Path(path).write_text(encode(metadata), encoding="utf-8")
        target = pathlib.Path(path)
        if target.is_symlink() or not target.is_file():
            raise ValueError("output must be one regular file")
        digest = hashlib.sha256()
        size = 0
        with target.open("rb") as stream:
            for block in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(block)
                size += len(block)
        bundle["artifacts"][name] = {"metadata": metadata, "sha256": digest.hexdigest(), "size": size}
    payload = encode(bundle).encode()
    if len(payload) > 48 * 1024:
        raise ValueError("output metadata exceeds 48 KiB")
    pathlib.Path("/tmp/loom/bundle").write_text(base64.b64encode(payload).decode(), encoding="ascii")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(run())
    except Exception as error:
        # Tracebacks/values may contain user parameters or remote credentials.
        print("Loom component launcher failed: " + type(error).__name__, file=sys.stderr)
        sys.exit(1)
