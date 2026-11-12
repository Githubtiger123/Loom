"""Manual acceptance example; compile with kfp SDK 2.x in your own environment.

No KFP server is used. Import the generated single-document IR into Loom and
submit with engine=NATIVE_ARGO. Images must be reachable from worker nodes.
"""
from kfp import compiler, dsl


@dsl.component(base_image="python:3.11-slim")
def produce(count: int, data: dsl.Output[dsl.Dataset]) -> int:
    import json
    with open(data.path, "w", encoding="utf-8") as stream:
        json.dump(list(range(count)), stream)
    print("produced", count, "rows", flush=True)
    return count


@dsl.component(base_image="python:3.11-slim")
def summarize(
    count: int,
    data: dsl.Input[dsl.Dataset],
    report: dsl.Output[dsl.Artifact],
    metrics: dsl.Output[dsl.Metrics],
):
    import json
    with open(data.path, encoding="utf-8") as stream:
        rows = json.load(stream)
    assert len(rows) == count
    with open(report.path, "w", encoding="utf-8") as stream:
        stream.write("rows=%d sum=%d\n" % (len(rows), sum(rows)))
    metrics.log_metric("row_count", len(rows))
    metrics.log_metric("sum", sum(rows))
    print("summary written", flush=True)


@dsl.pipeline(name="loom-python-file-dag")
def pipeline(count: int = 5):
    first = produce(count=count).set_caching_options(False)
    summarize(count=first.outputs["Output"], data=first.outputs["data"]).set_caching_options(False)


if __name__ == "__main__":
    compiler.Compiler().compile(pipeline, "loom-python-file-dag.yaml")
