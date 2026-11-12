"""SDK source example, not executed as part of this source-only change.

Compile in your own SDK environment; import the resulting IR as a Loom Pipeline
version, check native-compatibility, then submit with engine=NATIVE_ARGO.
Online stop and archived-log reads use the existing Loom run APIs.
"""
from kfp import compiler, dsl
from kfp.dsl import Dataset, Input, Metrics, Output


@dsl.component(base_image="python:3.11")
def produce(label: str, data: Output[Dataset], metrics: Output[Metrics]) -> str:
    import json
    import os

    os.makedirs(data.path, exist_ok=True)
    with open(os.path.join(data.path, "first.txt"), "w", encoding="utf-8") as stream:
        stream.write(label)
    with open(os.path.join(data.path, "metadata.json"), "w", encoding="utf-8") as stream:
        json.dump({"label": label}, stream)
    metrics.log_metric("files", 2)
    return label


@dsl.component(base_image="python:3.11")
def consume(data: Input[Dataset]) -> str:
    import json
    import os

    with open(os.path.join(data.path, "metadata.json"), encoding="utf-8") as stream:
        label = json.load(stream)["label"]
    print("Consumed directory for", label, flush=True)
    return label


@dsl.pipeline(name="loom-native-advanced")
def pipeline():
    with dsl.ParallelFor(items=["alpha", "beta"], parallelism=2) as label:
        made = produce(label=label)
        made.set_caching_options(True)
        made.set_retry(num_retries=2, backoff_duration="5s", backoff_factor=2.0)
        with dsl.If(made.outputs["Output"] != ""):
            consumed = consume(data=made.outputs["data"])
            consumed.set_caching_options(True)


if __name__ == "__main__":
    compiler.Compiler().compile(pipeline_func=pipeline, package_path="python_advanced.yaml")
