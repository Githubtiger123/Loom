// Derived from upstream metadata.Client.CreateExecution at the pinned revision.
// Modified by Loom: retain config-to-protobuf conversion without RPC persistence.
package metadata

import (
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func BuildExecution(config *ExecutionConfig, typeID int64) (*pb.Execution, error) {
	if typeID <= 0 {
		return nil, fmt.Errorf("execution type ID is required")
	}
	if config == nil {
		return nil, fmt.Errorf("metadata.CreateExecution got config == nil")
	}

	e := &pb.Execution{
		TypeId: &typeID,
		CustomProperties: map[string]*pb.Value{
			keyDisplayName: StringValue(config.DisplayName),
			keyTaskName:    StringValue(config.TaskName),
		},
	}
	if config.Name != "" {
		e.Name = &config.Name
	}
	e.LastKnownState = pb.Execution_RUNNING.Enum()
	if config.NotTriggered {
		// Note, in MLMD, CANCELED state means exactly as what we call
		// not triggered.
		// Reference: https://github.com/google/ml-metadata/blob/3434ebaf36db54a7e67dbb0793980a74ec0c5d50/ml_metadata/proto/metadata_store.proto#L251-L254
		e.LastKnownState = pb.Execution_CANCELED.Enum()
	}
	if config.ParentDagID != 0 {
		e.CustomProperties[keyParentDagID] = intValue(config.ParentDagID)
	}
	if config.IterationIndex != nil {
		e.CustomProperties[keyIterationIndex] = intValue(int64(*config.IterationIndex))
	}
	if config.IterationCount != nil {
		e.CustomProperties[keyIterationCount] = intValue(int64(*config.IterationCount))
	}
	if config.ExecutionType == ContainerExecutionTypeName {
		e.CustomProperties[keyPodName] = StringValue(config.PodName)
		e.CustomProperties[keyPodUID] = StringValue(config.PodUID)
		e.CustomProperties[keyNamespace] = StringValue(config.Namespace)
		e.CustomProperties[keyImage] = StringValue(config.Image)
		if config.CachedMLMDExecutionID != "" {
			e.CustomProperties[keyCachedExecutionID] = StringValue(config.CachedMLMDExecutionID)
		}
		if config.FingerPrint != "" {
			e.CustomProperties[keyCacheFingerPrint] = StringValue(config.FingerPrint)
		}
	}
	if config.InputParameters != nil {
		e.CustomProperties[keyInputs] = &pb.Value{Value: &pb.Value_StructValue{
			StructValue: &structpb.Struct{
				Fields: config.InputParameters,
			},
		}}
	}
	// We save the output parameter and output artifact relationships in MLMD in
	// case they're provided by a sub-task so that we can follow the
	// relationships and retrieve outputs downstream in components that depend
	// on said outputs as inputs.
	if config.OutputParameters != nil {
		// Convert OutputParameters to a format that can be saved in MLMD.
		glog.V(4).Info("runtime payload omitted by Loom policy")
		outputParametersCustomPropertyProtoMap := make(map[string]*structpb.Value)

		for name, value := range config.OutputParameters {
			if outputParameterProtoMsg, ok := interface{}(value).(proto.Message); ok {
				glog.V(4).Info("runtime payload omitted by Loom policy")
				glog.V(4).Info("runtime payload omitted by Loom policy")
				b, err := protojson.Marshal(outputParameterProtoMsg)
				if err != nil {
					return nil, err
				}
				outputValue, _ := structpb.NewValue(string(b))
				outputParametersCustomPropertyProtoMap[name] = outputValue
			}
		}
		e.CustomProperties[keyParameterProducerTask] = &pb.Value{Value: &pb.Value_StructValue{
			StructValue: &structpb.Struct{
				Fields: outputParametersCustomPropertyProtoMap,
			},
		}}
	}
	if config.OutputArtifacts != nil {
		b, err := json.Marshal(config.OutputArtifacts)
		if err != nil {
			return nil, err
		}
		e.CustomProperties[keyArtifactProducerTask] = StringValue(string(b))
	}
	if config.TotalDagTasks != nil {
		e.CustomProperties[keyTotalDagTasks] = intValue(int64(*config.TotalDagTasks))
	}
	for k, v := range config.PluginCustomProperties {
		e.CustomProperties[k] = StringValue(v)
	}

	return e, nil
}
