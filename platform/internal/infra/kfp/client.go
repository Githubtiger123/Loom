// Package kfp 适配官方 backend/api/v2beta1/run.proto HTTP 契约。
// 不复制官方执行引擎，不直接创建 Argo Workflow。
package kfp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type Client struct {
	endpoint  string
	tokenFile string
	http      *http.Client
}

type Run struct {
	ID          string          `json:"run_id"`
	Name        string          `json:"display_name"`
	Namespace   string          `json:"namespace"`
	Description string          `json:"description"`
	State       string          `json:"state"`
	Error       *RuntimeError   `json:"error,omitempty"`
	History     []RuntimeStatus `json:"state_history"`
	CreatedAt   string          `json:"created_at"`
	FinishedAt  string          `json:"finished_at"`
	Details     json.RawMessage `json:"run_details"`
}

type CreateRun struct {
	ID             string          `json:"run_id"`
	Name           string          `json:"display_name"`
	Namespace      string          `json:"namespace"`
	Description    string          `json:"description"`
	ServiceAccount string          `json:"service_account"`
	PipelineSpec   json.RawMessage `json:"pipeline_spec"`
	RuntimeConfig  RuntimeConfig   `json:"runtime_config"`
}

type RuntimeConfig struct {
	Parameters   map[string]any `json:"parameters"`
	PipelineRoot string         `json:"pipeline_root"`
}

// Error 故意不暴露远端响应体、请求 URL 或 token。
type Error struct{ Status int }

func (e *Error) Error() string { return fmt.Sprintf("KFP HTTP status %d", e.Status) }

func New(endpoint, tokenFile string, timeout time.Duration) *Client {
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), tokenFile: tokenFile,
		http: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("KFP request encoding failed")
		}
		body = bytes.NewReader(data)
	}
	address := c.endpoint + "/apis/v2beta1" + path
	if len(query) != 0 {
		address += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return fmt.Errorf("KFP request configuration invalid")
	}
	// 每次读取以兼容 projected ServiceAccount token 轮换，不缓存到日志或业务库。
	token, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return fmt.Errorf("KFP service token unavailable")
	}
	value := strings.TrimSpace(string(token))
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("KFP service token invalid")
	}
	req.Header.Set("Authorization", "Bearer "+value)
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("KFP transport unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{Status: resp.StatusCode}
	}
	if output == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 || json.Unmarshal(data, output) != nil {
		return fmt.Errorf("KFP response invalid or oversized")
	}
	return nil
}

func (c *Client) CreateRun(ctx context.Context, input CreateRun) (*Run, error) {
	var out Run
	err := c.request(ctx, http.MethodPost, "/runs", nil, input, &out)
	return &out, err
}

func (c *Client) GetRun(ctx context.Context, id string) (*Run, error) {
	if !validator.IsCatalogID(id) {
		return nil, fmt.Errorf("KFP run ID invalid")
	}
	var out Run
	err := c.request(ctx, http.MethodGet, "/runs/"+url.PathEscape(id), nil, nil, &out)
	return &out, err
}

func (c *Client) TerminateRun(ctx context.Context, id string) error {
	if !validator.IsCatalogID(id) {
		return fmt.Errorf("KFP run ID invalid")
	}
	return c.request(ctx, http.MethodPost, "/runs/"+url.PathEscape(id)+":terminate", nil, struct{}{}, nil)
}

func (c *Client) DeleteRun(ctx context.Context, id string) error {
	if !validator.IsCatalogID(id) {
		return fmt.Errorf("KFP run ID invalid")
	}
	return c.request(ctx, http.MethodDelete, "/runs/"+url.PathEscape(id), nil, nil, nil)
}
