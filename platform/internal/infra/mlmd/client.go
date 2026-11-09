package mlmd

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type Execution struct {
	ID         string         `json:"executionId"`
	Name       string         `json:"name"`
	State      string         `json:"state"`
	CreatedAt  string         `json:"createdAtEpochMs"`
	UpdatedAt  string         `json:"updatedAtEpochMs"`
	Properties map[string]any `json:"properties"`
	Custom     map[string]any `json:"customProperties"`
}
type Artifact struct {
	ID              string         `json:"artifactId"`
	Name            string         `json:"name"`
	TypeID          string         `json:"typeId"`
	Type            string         `json:"type"`
	TypeVersion     string         `json:"typeVersion"`
	TypeDescription string         `json:"typeDescription"`
	TypeProperties  map[string]int `json:"typeProperties"`
	URI             string         `json:"uri"`
	StorageURI      string         `json:"-"` // Original location for strict server-side validation, never public credentials.
	State           string         `json:"state"`
	CreatedAt       string         `json:"createdAtEpochMs"`
	UpdatedAt       string         `json:"updatedAtEpochMs"`
	Properties      map[string]any `json:"properties"`
	Custom          map[string]any `json:"customProperties"`
}
type Event struct {
	ExecutionID string `json:"executionId"`
	ArtifactID  string `json:"artifactId"`
	Type        int    `json:"type"`
	Path        []any  `json:"path"`
}
type Snapshot struct {
	ContextID  string
	Executions []Execution
	Events     []Event
	Artifacts  map[string]Artifact
}

type Client struct {
	endpoint, tokenFile string
	http                *http.Client
	gate                chan struct{}
}

func New(endpoint, tokenFile string, timeout time.Duration) *Client {
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), tokenFile: tokenFile,
		http: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, gate: make(chan struct{}, 4)}
}

// ReadRun never accepts a caller-selected metadata ID. Executions are scoped to
// the verified system.PipelineRun context; artifacts are reached ONLY via events
// of those executions (also works for cached artifacts from earlier runs).
// Empty executionID reads graph states only. A selected execution is matched
// against context membership BEFORE querying its events or artifact metadata.
func (c *Client) ReadRun(ctx context.Context, runID, namespace, executionID string) (*Snapshot, error) {
	if !validator.IsCatalogID(runID) || !validator.IsDNS1123Label(namespace) {
		return nil, fmt.Errorf("invalid metadata scope")
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	budget := 16 << 20
	call := func(method string, request []byte) ([]field, error) {
		data, err := c.call(ctx, method, request, &budget)
		if err != nil {
			return nil, err
		}
		return fields(data)
	}
	fs, err := call("GetContextByTypeAndName", bytesField(bytesField(nil, 1, []byte("system.PipelineRun")), 2, []byte(runID)))
	if err != nil {
		return nil, err
	}
	var scope record
	for _, f := range fs {
		if f.n == 1 {
			scope, err = parseRecord(f.b, "context")
			if err != nil {
				return nil, err
			}
		}
	}
	if scope.ID == "" || scope.ID == "0" || scope.Name != runID || (scope.Type != "" && scope.Type != "system.PipelineRun") {
		return nil, fmt.Errorf("metadata context not ready")
	}
	if ns, ok := scope.Custom["namespace"]; ok && ns != namespace {
		return nil, fmt.Errorf("metadata namespace mismatch")
	}
	if ns, ok := scope.Properties["namespace"]; ok && ns != namespace {
		return nil, fmt.Errorf("metadata namespace mismatch")
	}
	out := &Snapshot{ContextID: scope.ID, Executions: []Execution{}, Events: []Event{}, Artifacts: map[string]Artifact{}}
	scopeID, err := strconv.ParseUint(scope.ID, 10, 63)
	if err != nil {
		return nil, fmt.Errorf("invalid context ID")
	}
	token := ""
	seen := map[string]bool{}
	for page := 0; ; page++ {
		if page >= 20 {
			return nil, fmt.Errorf("metadata execution limit exceeded")
		}
		options := bytesField(intField(nil, 1, 100), 3, []byte(token))
		fs, err = call("GetExecutionsByContext", bytesField(intField(nil, 1, scopeID), 2, options))
		if err != nil {
			return nil, err
		}
		next := ""
		for _, f := range fs {
			if f.n == 2 {
				next = string(f.b)
			}
			if f.n != 1 {
				continue
			}
			r, err := parseRecord(f.b, "execution")
			if err != nil {
				return nil, err
			}
			if r.ID == "" || r.ID == "0" || seen[r.ID] || len(out.Executions) >= 2000 {
				return nil, fmt.Errorf("invalid or duplicate execution ID")
			}
			seen[r.ID] = true
			if ns, ok := r.Custom["namespace"]; ok && ns != namespace {
				return nil, fmt.Errorf("execution namespace mismatch")
			}
			out.Executions = append(out.Executions, Execution{r.ID, r.Name, enum(r.State, []string{"UNKNOWN", "NEW", "RUNNING", "COMPLETE", "FAILED", "CACHED", "CANCELED"}), r.Created, r.Updated, r.Properties, r.Custom})
		}
		if next == "" {
			break
		}
		if token == next {
			return nil, fmt.Errorf("invalid metadata pagination")
		}
		token = next
	}
	if executionID == "" || (executionID != "*" && !seen[executionID]) {
		return out, nil
	}
	executionIDs := []string{executionID}
	if executionID == "*" {
		executionIDs = make([]string, 0, len(out.Executions))
		for _, e := range out.Executions {
			executionIDs = append(executionIDs, e.ID)
		}
	}
	artIDs := []string{}
	artSeen := map[string]bool{}
	for start := 0; start < len(executionIDs); start += 100 {
		end := min(start+100, len(executionIDs))
		request, err := idRequest(executionIDs[start:end])
		if err != nil {
			return nil, err
		}
		fs, err = call("GetEventsByExecutionIDs", request)
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			if f.n != 1 {
				continue
			}
			e, err := parseEvent(f.b)
			if err != nil {
				return nil, err
			}
			if !seen[e.ExecutionID] {
				return nil, fmt.Errorf("unscoped metadata event")
			}
			out.Events = append(out.Events, e)
			if len(out.Events) > 10000 {
				return nil, fmt.Errorf("metadata event limit exceeded")
			}
			if !artSeen[e.ArtifactID] {
				artSeen[e.ArtifactID] = true
				artIDs = append(artIDs, e.ArtifactID)
			}
		}
	}
	if len(artIDs) > 4000 {
		return nil, fmt.Errorf("metadata artifact limit exceeded")
	}
	typeIDs := []string{}
	typeSeen := map[string]bool{}
	for start := 0; start < len(artIDs); start += 100 {
		end := min(start+100, len(artIDs))
		request, err := idRequest(artIDs[start:end])
		if err != nil {
			return nil, err
		}
		fs, err = call("GetArtifactsByID", request)
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			if f.n != 1 {
				continue
			}
			r, err := parseRecord(f.b, "artifact")
			if err != nil {
				return nil, err
			}
			if !artSeen[r.ID] {
				return nil, fmt.Errorf("unscoped metadata artifact")
			}
			// Connection/Secret references are operational metadata, not artifact business metadata.
			delete(r.Custom, "store_session_info")
			delete(r.Properties, "store_session_info")
			out.Artifacts[r.ID] = Artifact{ID: r.ID, Name: r.Name, TypeID: r.TypeID, Type: r.Type, URI: publicURI(r.URI), StorageURI: r.URI, State: enum(r.State, []string{"UNKNOWN", "PENDING", "LIVE", "MARKED_FOR_DELETION", "DELETED", "ABANDONED", "REFERENCE"}), CreatedAt: r.Created, UpdatedAt: r.Updated, Properties: r.Properties, Custom: r.Custom, TypeProperties: map[string]int{}}
			if !typeSeen[r.TypeID] {
				typeSeen[r.TypeID] = true
				typeIDs = append(typeIDs, r.TypeID)
			}
		}
	}
	for start := 0; start < len(typeIDs); start += 100 {
		end := min(start+100, len(typeIDs))
		request, err := idRequest(typeIDs[start:end])
		if err != nil {
			return nil, err
		}
		fs, err = call("GetArtifactTypesByID", request)
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			if f.n != 1 {
				continue
			}
			parts, err := fields(f.b)
			if err != nil {
				return nil, err
			}
			var id, name, version, description string
			properties := map[string]int{}
			for _, p := range parts {
				switch p.n {
				case 1:
					id = decimal(p.v)
				case 2:
					name = string(p.b)
				case 4:
					version = string(p.b)
				case 5:
					description = string(p.b)
				case 3:
					entry, err := fields(p.b)
					if err != nil {
						return nil, err
					}
					var key string
					var value int
					for _, v := range entry {
						if v.n == 1 {
							key = string(v.b)
						}
						if v.n == 2 {
							value = int(v.v)
						}
					}
					properties[key] = value
				}
			}
			for key, a := range out.Artifacts {
				if a.TypeID == id {
					a.Type = name
					a.TypeVersion = version
					a.TypeDescription = description
					a.TypeProperties = properties
					out.Artifacts[key] = a
				}
			}
		}
	}
	return out, nil
}

func enum(v uint64, values []string) string {
	if v >= uint64(len(values)) {
		return "UNKNOWN"
	}
	return values[v]
}

// Return a location, never URL credentials or a signed download query.
func publicURI(value string) string {
	u, err := url.Parse(value)
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}
func idRequest(ids []string) ([]byte, error) {
	var out []byte
	for _, id := range ids {
		v, err := strconv.ParseUint(id, 10, 63)
		if err != nil || v == 0 {
			return nil, fmt.Errorf("invalid metadata ID")
		}
		out = intField(out, 1, v)
	}
	return out, nil
}

func (c *Client) call(ctx context.Context, method string, data []byte, budget *int) ([]byte, error) {
	frame := make([]byte, 5, len(data)+5)
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(data)))
	frame = append(frame, data...)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/ml_metadata.MetadataStoreService/"+method, bytes.NewReader(frame))
	if err != nil {
		return nil, fmt.Errorf("invalid metadata endpoint")
	}
	if c.tokenFile != "" {
		token, err := os.ReadFile(c.tokenFile)
		if err != nil {
			return nil, fmt.Errorf("metadata token unavailable")
		}
		value := strings.TrimSpace(string(token))
		if value == "" || strings.ContainsAny(value, "\r\n\x00") {
			return nil, fmt.Errorf("invalid metadata token")
		}
		req.Header.Set("Authorization", "Bearer "+value)
	}
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	req.Header.Set("Accept", "application/grpc-web+proto")
	req.Header.Set("X-Grpc-Web", "1")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metadata transport unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metadata request rejected")
	}
	limit := min(*budget, 8<<20)
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil || len(b) > limit {
		return nil, fmt.Errorf("metadata response oversized")
	}
	*budget -= len(b)
	status := resp.Header.Get("Grpc-Status")
	var message []byte
	messages := 0
	trailers := false
	for len(b) > 0 {
		if len(b) < 5 {
			return nil, fmt.Errorf("invalid grpc-web frame")
		}
		flag := b[0]
		n := uint64(binary.BigEndian.Uint32(b[1:5]))
		b = b[5:]
		if n > uint64(len(b)) {
			return nil, fmt.Errorf("invalid grpc-web frame")
		}
		payload := b[:int(n)]
		b = b[int(n):]
		switch flag {
		case 0:
			if trailers {
				return nil, fmt.Errorf("invalid grpc-web ordering")
			}
			message = payload
			messages++
		case 128:
			if trailers {
				return nil, fmt.Errorf("duplicate grpc-web trailer")
			}
			trailers = true
			for _, line := range strings.Split(string(payload), "\r\n") {
				key, value, ok := strings.Cut(line, ":")
				if ok && strings.EqualFold(key, "grpc-status") {
					status = strings.TrimSpace(value)
				}
			}
		default:
			return nil, fmt.Errorf("unsupported grpc-web compression")
		}
	}
	if status == "" {
		status = resp.Trailer.Get("Grpc-Status")
	}
	if status != "0" || messages != 1 {
		return nil, fmt.Errorf("metadata RPC failed")
	}
	return message, nil
}
