# Copyright 2023 The Kubeflow Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
"""Adapted from kfp/local/executor_output_utils.py; see thirdparty/kfp/UPSTREAM.md.

Only the scalar parameter helpers are extracted. Enum arguments are canonical
IR strings instead of Python protobuf types, avoiding an extra KFP installation
before the component's SDK bootstrap command can run.
"""
import json
from typing import Any, Dict


def special_dsl_outputpath_read(
    parameter_name: str,
    output_file: str,
    dtype: str,
) -> Any:
    """Read OutputPath strings literally, booleans case-insensitively, else JSON."""
    try:
        with open(output_file, encoding="utf-8") as f:
            value = f.read()

        if dtype == "STRING":
            value = value
        elif dtype == "BOOLEAN":
            value = json.loads(value.lower())
        else:
            value = json.loads(value)
        return value
    except Exception as e:
        raise ValueError(
            f"Could not deserialize output {parameter_name!r} from path {output_file}"
        ) from e


def cast_protobuf_numbers(
    output_parameters: Dict[str, Any],
    output_parameter_types: Dict[str, Dict[str, Any]],
) -> Dict[str, Any]:
    """Restore NUMBER_INTEGER values from protobuf's shared number representation."""
    int_output_keys = [
        output_param_name
        for output_param_name, parameter_spec in output_parameter_types.items()
        if parameter_spec["parameterType"] == "NUMBER_INTEGER"
    ]
    for int_output_key in int_output_keys:
        if int_output_key in output_parameters:
            value = output_parameters[int_output_key]
            # Loom's boundary is stricter than the upstream int() coercion:
            # never silently turn a boolean or fractional result into an int.
            if type(value) not in (int, float) or int(value) != value:
                raise ValueError("integer output is not an integral number")
            output_parameters[int_output_key] = int(value)
    return output_parameters
