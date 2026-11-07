// Package mlmd reads the official MLMD grpc-web API through metadata-envoy.
// Only the read-only wire subset below is implemented; field numbers follow
// KFP third_party/ml-metadata/ml_metadata/proto/metadata_store{,_service}.proto.
package mlmd

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type field struct {
	n protowire.Number
	v uint64
	b []byte
}

func fields(data []byte) ([]field, error) {
	out := []field{}
	for len(data) > 0 {
		if len(out) >= 100000 {
			return nil, fmt.Errorf("MLMD protobuf field limit exceeded")
		}
		n, typ, size := protowire.ConsumeTag(data)
		if size < 0 || n < 1 {
			return nil, fmt.Errorf("invalid MLMD protobuf")
		}
		data = data[size:]
		f := field{n: n}
		switch typ {
		case protowire.VarintType:
			f.v, size = protowire.ConsumeVarint(data)
		case protowire.Fixed64Type:
			f.v, size = protowire.ConsumeFixed64(data)
		case protowire.BytesType:
			f.b, size = protowire.ConsumeBytes(data)
		default:
			size = protowire.ConsumeFieldValue(n, typ, data)
		}
		if size < 0 {
			return nil, fmt.Errorf("invalid MLMD protobuf")
		}
		out = append(out, f)
		data = data[size:]
	}
	return out, nil
}

func bytesField(b []byte, n protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(b, n, protowire.BytesType), value)
}
func intField(b []byte, n protowire.Number, value uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(b, n, protowire.VarintType), value)
}
func decimal(v uint64) string { return strconv.FormatInt(int64(v), 10) }

type record struct {
	ID, Name, TypeID, Type, URI string
	State                       uint64
	Created, Updated            string
	Properties, Custom          map[string]any
}

func parseRecord(data []byte, kind string) (record, error) {
	r := record{Properties: map[string]any{}, Custom: map[string]any{}}
	fs, err := fields(data)
	if err != nil {
		return r, err
	}
	for _, f := range fs {
		switch f.n {
		case 1:
			r.ID = decimal(f.v)
		case 2:
			r.TypeID = decimal(f.v)
		case 4, 5:
			key, value, err := property(f.b)
			if err != nil {
				return r, err
			}
			if f.n == 4 {
				r.Properties[key] = value
			} else {
				r.Custom[key] = value
			}
		}
		if kind == "context" {
			switch f.n {
			case 3:
				r.Name = string(f.b)
			case 6:
				r.Type = string(f.b)
			}
		} else if kind == "execution" {
			switch f.n {
			case 3:
				r.State = f.v
			case 6:
				r.Name = string(f.b)
			case 7:
				r.Type = string(f.b)
			case 8:
				r.Created = decimal(f.v)
			case 9:
				r.Updated = decimal(f.v)
			}
		} else {
			switch f.n {
			case 3:
				r.URI = string(f.b)
			case 6:
				r.State = f.v
			case 7:
				r.Name = string(f.b)
			case 8:
				r.Type = string(f.b)
			case 9:
				r.Created = decimal(f.v)
			case 10:
				r.Updated = decimal(f.v)
			}
		}
	}
	return r, nil
}

func property(data []byte) (string, any, error) {
	fs, err := fields(data)
	if err != nil {
		return "", nil, err
	}
	var key string
	var value any
	for _, f := range fs {
		if f.n == 1 {
			key = string(f.b)
		}
		if f.n != 2 {
			continue
		}
		values, err := fields(f.b)
		if err != nil {
			return "", nil, err
		}
		for _, v := range values {
			switch v.n {
			case 1:
				value = json.Number(decimal(v.v))
			case 2:
				d := math.Float64frombits(v.v)
				if math.IsNaN(d) || math.IsInf(d, 0) {
					return "", nil, fmt.Errorf("invalid MLMD number")
				}
				value = d
			case 3:
				value = string(v.b)
			case 4:
				var s structpb.Struct
				if err := (proto.UnmarshalOptions{RecursionLimit: 64}).Unmarshal(v.b, &s); err != nil {
					return "", nil, fmt.Errorf("invalid MLMD struct")
				}
				b, err := protojson.Marshal(&s)
				if err != nil || json.Unmarshal(b, &value) != nil {
					return "", nil, fmt.Errorf("invalid MLMD value")
				}
			case 5:
				value = map[string]any{"unavailable": "OPAQUE_PROTO"} // Do not expose arbitrary Any payloads.
			case 6:
				value = v.v != 0
			}
		}
	}
	return key, value, nil
}

func parseEvent(data []byte) (Event, error) {
	e := Event{Path: []any{}}
	fs, err := fields(data)
	if err != nil {
		return e, err
	}
	for _, f := range fs {
		switch f.n {
		case 1:
			e.ArtifactID = decimal(f.v)
		case 2:
			e.ExecutionID = decimal(f.v)
		case 4:
			e.Type = int(f.v)
		case 3:
			steps, err := fields(f.b)
			if err != nil {
				return e, err
			}
			for _, step := range steps {
				if step.n != 1 {
					continue
				}
				parts, err := fields(step.b)
				if err != nil {
					return e, err
				}
				for _, part := range parts {
					if part.n == 1 {
						e.Path = append(e.Path, json.Number(decimal(part.v)))
					}
					if part.n == 2 {
						e.Path = append(e.Path, string(part.b))
					}
				}
			}
		}
	}
	return e, nil
}
