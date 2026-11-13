package runtimeapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

type Operation string

const (
	GetRunContext          Operation = "run-context.get"
	CreateExecution        Operation = "executions.create"
	GetExecution           Operation = "executions.get"
	FindExecution          Operation = "executions.find"
	ListDAGExecutions      Operation = "executions.list-dag"
	PrePublishExecution    Operation = "executions.prepublish"
	PublishExecution       Operation = "executions.publish"
	GetExecutionInputs     Operation = "executions.inputs"
	GetExecutionOutputs    Operation = "executions.outputs"
	UpdatePluginProperties Operation = "executions.update-plugin-properties"
	UpdateDAGState         Operation = "executions.update-dag-state"
	EnsureArtifact         Operation = "artifacts.ensure"
	GetArtifacts           Operation = "artifacts.get"
	GetArtifactEvents      Operation = "artifacts.events"
	EnsureArtifactType     Operation = "artifacts.ensure-type"
	FindArtifact           Operation = "artifacts.find"
	LookupCache            Operation = "cache.lookup"
	PublishCache           Operation = "cache.publish"
)

func (op Operation) Valid() bool {
	switch op {
	case GetRunContext, CreateExecution, GetExecution, FindExecution, ListDAGExecutions, PrePublishExecution, PublishExecution,
		GetExecutionInputs, GetExecutionOutputs, UpdatePluginProperties, UpdateDAGState, EnsureArtifact, GetArtifacts, GetArtifactEvents, EnsureArtifactType, FindArtifact, LookupCache, PublishCache:
		return true
	}
	return false
}

type ClientOptions struct {
	// Endpoint is an operator-configured HTTPS origin, never a request URL.
	Endpoint  string
	CAFile    string
	TokenFile string
}

type Client struct {
	origin string
	http   *http.Client
}

type Envelope struct {
	Protocol string `json:"protocol"`
	// RequestID is content-addressed. The server must combine it with verified
	// scope, operation and writer identity; it is not an authorization token.
	RequestID string          `json:"requestId"`
	Payload   json.RawMessage `json:"payload"`
}

type Reply struct {
	Protocol string          `json:"protocol"`
	Payload  json.RawMessage `json:"payload"`
}

// NewClient reuses client-go's TLS and rotating token-file transport. No inline
// token, insecure TLS mode, user cookies or fallback KFP/MLMD client is accepted.
func NewClient(options ClientOptions) (*Client, error) {
	origin, err := url.Parse(options.Endpoint)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" ||
		origin.Opaque != "" || (origin.Path != "" && origin.Path != "/") || strings.TrimSpace(options.TokenFile) == "" {
		return nil, fmt.Errorf("runtime requires a trusted HTTPS origin and projected token file")
	}
	transport, err := rest.HTTPClientFor(&rest.Config{
		Host:            options.Endpoint,
		BearerTokenFile: options.TokenFile,
		TLSClientConfig: rest.TLSClientConfig{CAFile: options.CAFile},
		Timeout:         15 * time.Second,
		UserAgent:       Protocol,
	})
	if err != nil {
		return nil, fmt.Errorf("runtime TLS/token transport could not be initialized")
	}
	transport.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{origin: origin.Scheme + "://" + origin.Host, http: transport}, nil
}

// HTTPError contains only a stable status, never response bodies, payloads,
// tokens, storage URLs or wrapped transport errors. Callers own bounded retry.
type HTTPError struct{ Status int }

func (err *HTTPError) Error() string {
	return fmt.Sprintf("runtime request rejected (HTTP %d)", err.Status)
}

func (c *Client) Call(ctx context.Context, op Operation, payload json.RawMessage) (json.RawMessage, error) {
	if c == nil || c.http == nil || !op.Valid() {
		return nil, fmt.Errorf("runtime transport or operation is not configured")
	}
	if len(payload) == 0 || len(payload) > MaxMessageBytes/2 || !json.Valid(payload) {
		return nil, fmt.Errorf("runtime payload is invalid or exceeds the message limit")
	}
	digest := sha256.Sum256(append([]byte(Protocol+"\x00"+string(op)+"\x00"), payload...))
	body, err := json.Marshal(Envelope{Protocol: Protocol, RequestID: hex.EncodeToString(digest[:]), Payload: payload})
	if err != nil || len(body) > MaxMessageBytes {
		return nil, fmt.Errorf("runtime request exceeds the message limit")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+"/internal/v1/pipeline-runtime/"+string(op), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("runtime request could not be prepared")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("runtime transport unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &HTTPError{Status: response.StatusCode}
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, MaxMessageBytes+1))
	if err != nil || len(encoded) > MaxMessageBytes {
		return nil, fmt.Errorf("runtime response is unreadable or exceeds the message limit")
	}
	var reply Reply
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&reply) != nil || reply.Protocol != Protocol || len(reply.Payload) == 0 {
		return nil, fmt.Errorf("runtime response protocol is invalid")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("runtime response has trailing data")
	}
	return reply.Payload, nil
}
