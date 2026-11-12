"""最小 Pipeline：加法 → 打印结果并记录指标。

安装 SDK：python -m pip install 'kfp>=2,<3'
编译 IR：python python_simple.py
将生成的 python_simple.yaml 导入 Loom Pipeline 版本，使用 NATIVE_ARGO 运行。
这只是人工测试示例，不会在编译时向集群提交任务。
"""

from pathlib import Path

from kfp import compiler, dsl


@dsl.component(base_image="python:3.11-slim")
def add(a: int, b: int) -> int:
    result = a + b
    print(f"{a} + {b} = {result}", flush=True)
    return result


@dsl.component(base_image="python:3.11-slim")
def show_result(value: int, metrics: dsl.Output[dsl.Metrics]):
    print(f"Pipeline result: {value}", flush=True)
    metrics.log_metric("result", value)


@dsl.pipeline(name="loom-simple-demo")
def simple_pipeline(a: int = 2, b: int = 3):
    calculation = add(a=a, b=b).set_caching_options(False)
    show_result(value=calculation.outputs["Output"]).set_caching_options(False)


if __name__ == "__main__":
    output = Path(__file__).with_suffix(".yaml")
    compiler.Compiler().compile(
        pipeline_func=simple_pipeline,
        package_path=str(output),
    )
    print(f"Compiled IR: {output}")
