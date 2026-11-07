package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/k8s/podexec"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/service"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"github.com/gorilla/websocket"
	"golang.org/x/time/rate"
	utilexec "k8s.io/client-go/util/exec"
)

const terminalProtocol = "loom.terminal.v1"

type terminalRequest struct {
	groupID, runID           uint64
	taskID, container, shell string
	width, height            int
}

type terminalMessage struct {
	Type   string `json:"type"`
	Token  string `json:"token,omitempty"`
	Data   string `json:"data,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

func parseTerminalMessage(data []byte) (terminalMessage, error) {
	var message terminalMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		return message, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return message, apperr.New(apperr.CodeInvalid, "终端消息格式无效")
	}
	return message, nil
}

func parseTerminalRequest(r *http.Request) (terminalRequest, error) {
	o := terminalRequest{taskID: r.PathValue("taskId"), shell: "/bin/sh", container: "main", width: 80, height: 24}
	var err error
	o.groupID, o.runID, err = mlIDs(r, "rid")
	if err != nil {
		return o, err
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return o, apperr.New(apperr.CodeInvalid, "终端参数编码无效")
	}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			return o, apperr.New(apperr.CodeInvalid, "终端参数不能为空或重复")
		}
		switch key {
		case "container":
			o.container = values[0]
		case "shell":
			o.shell = values[0]
		case "width", "height":
			n, err := strconv.Atoi(values[0])
			if err != nil {
				return o, apperr.New(apperr.CodeInvalid, "终端尺寸无效")
			}
			if key == "width" {
				o.width = n
			} else {
				o.height = n
			}
		default:
			return o, apperr.New(apperr.CodeInvalid, "不支持的终端参数；禁止在 URL 中携带凭据")
		}
	}
	if !validator.IsDNS1123Label(o.container) || !validator.IsIdempotencyKey(o.taskID) ||
		!validator.IsTerminalSize(o.width, o.height) || !validator.IsTerminalShell(o.shell) {
		return o, apperr.New(apperr.CodeInvalid, "终端节点、容器、shell 或尺寸无效")
	}
	return o, nil
}

// This entry owns its WebSocket authentication: browsers cannot set Authorization
// on new WebSocket(). They send the existing Access JWT in the first frame instead.
func (a *API) PipelineTaskTerminal(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil {
		// Trusted-header mode still uses the existing trusted-proxy authentication.
		a.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a.openPipelineTerminal(w, r, r.Context().Value(actorKey{}).(*model.User))
		})).ServeHTTP(w, r)
		return
	}
	a.openPipelineTerminal(w, r, nil)
}

func (a *API) terminalOrigin(r *http.Request) error {
	if a.auth != nil {
		return a.checkBrowserOrigin(r, true)
	}
	origins := r.Header.Values("Origin")
	if len(origins) != 1 || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return apperr.New(apperr.CodeForbidden, "终端要求同源 Origin")
	}
	u, err := url.Parse(origins[0])
	if err != nil || !strings.EqualFold(u.Host, r.Host) || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return apperr.New(apperr.CodeForbidden, "终端请求来源不匹配")
	}
	return nil
}

func (a *API) openPipelineTerminal(w http.ResponseWriter, r *http.Request, user *model.User) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := a.terminalOrigin(r); err != nil {
		a.error(w, err)
		return
	}
	o, err := parseTerminalRequest(r)
	if err != nil {
		a.error(w, err)
		return
	}
	protocol := false
	for _, value := range websocket.Subprotocols(r) {
		if value == terminalProtocol {
			protocol = true
		}
	}
	if !websocket.IsWebSocketUpgrade(r) || !protocol {
		a.error(w, apperr.New(apperr.CodeInvalid, "需要 WebSocket 和 loom.terminal.v1 子协议"))
		return
	}
	select {
	case a.terminalPending <- struct{}{}:
	default:
		a.error(w, apperr.New(apperr.CodeRateLimited, "终端握手过多，请稍后重试"))
		return
	}
	var pendingOnce sync.Once
	releasePending := func() { pendingOnce.Do(func() { <-a.terminalPending }) }
	defer releasePending()
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, ReadBufferSize: 4096, WriteBufferSize: 16384,
		Subprotocols: []string{terminalProtocol}, CheckOrigin: func(r *http.Request) bool { return a.terminalOrigin(r) == nil }}
	conn, err := upgrader.Upgrade(w, r, http.Header{"Cache-Control": []string{"no-store"}})
	if err != nil {
		return
	} // Upgrade already wrote the HTTP error, never append JSON.
	defer conn.Close()
	socket := &terminalSocket{conn: conn}
	conn.SetReadLimit(16 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), podexec.MaxDuration)
	defer cancel()
	socket.abort = cancel
	// HTTP Server shutdown does not close hijacked sockets. The API base context
	// cancellation and the hard session deadline both close the connection explicitly.
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	access := ""
	if a.auth != nil {
		kind, data, err := conn.ReadMessage()
		if err != nil || kind != websocket.TextMessage {
			return
		}
		message, err := parseTerminalMessage(data)
		if err != nil || message.Type != "auth" || message.Token == "" || message.Data != "" || message.Width != 0 || message.Height != 0 {
			socket.fail(apperr.New(apperr.CodeUnauthorized, "首帧必须为 Access JWT 认证消息"))
			return
		}
		authCtx, authCancel := context.WithTimeout(ctx, 5*time.Second)
		principal, err := a.auth.Access(authCtx, message.Token)
		authCancel()
		if err != nil {
			socket.fail(err)
			return
		}
		if principal.PasswordChangeRequired {
			socket.fail(apperr.New(apperr.CodeForbidden, "必须先修改密码"))
			return
		}
		user, access = principal.User, message.Token
	}
	releasePending()
	release, err := a.terminalStreams.reserve(user.ID)
	if err != nil {
		socket.fail(apperr.New(apperr.CodeRateLimited, "终端连接已达上限，请关闭其他终端窗口后重试"))
		return
	}
	defer release()
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	session, err := a.service.PreparePipelineTerminal(ctx, user.ID, o.groupID, o.runID, o.taskID, o.container, o.shell)
	if err != nil {
		if apperr.From(err).Code == apperr.CodeForbidden {
			auditCtx, auditCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			if auditErr := a.service.Denied(auditCtx, user.ID, "pipeline.exec"); auditErr != nil {
				a.log.Error("终端拒绝审计失败", "err", auditErr)
			}
			auditCancel()
		}
		socket.fail(err)
		return
	}
	check := func() error {
		checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
		defer checkCancel()
		if a.auth != nil {
			p, err := a.auth.Access(checkCtx, access)
			if err != nil {
				return err
			}
			if p.User.ID != user.ID || p.PasswordChangeRequired {
				return apperr.New(apperr.CodeUnauthorized, "终端登录已失效")
			}
		}
		return session.Check(checkCtx)
	}
	a.runPipelineTerminal(ctx, cancel, socket, session, check, o)
}

// All normal WebSocket writes are serialized; control frames have their own deadline.
type terminalSocket struct {
	conn         *websocket.Conn
	mu           sync.Mutex
	output       int64
	abort        context.CancelFunc
	outputFailed atomic.Bool
}

func (s *terminalSocket) json(value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.conn.WriteJSON(value)
}

func (s *terminalSocket) fail(err error) {
	e := apperr.From(err)
	_ = s.json(map[string]any{"type": "error", "code": e.Code, "message": e.Message})
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "terminal rejected"), time.Now().Add(time.Second))
}

func (s *terminalSocket) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if int64(len(p)) > (64<<20)-s.output {
		s.outputFailed.Store(true)
		s.abort()
		return 0, apperr.New(apperr.CodeQuotaExceeded, "终端输出超过会话上限")
	}
	written := 0
	for len(p) > 0 {
		n := min(len(p), 16<<10)
		frame := make([]byte, n+1)
		frame[0] = 1 // Raw TTY output, UTF-8 decoding belongs to the browser's streaming decoder.
		copy(frame[1:], p[:n])
		_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := s.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			s.outputFailed.Store(true)
			s.abort()
			return written, err
		}
		written += n
		s.output += int64(n)
		p = p[n:]
	}
	return written, nil
}

type terminalSizes struct {
	ctx   context.Context
	sizes chan podexec.Size
}

func (q *terminalSizes) Next() *podexec.Size {
	select {
	case <-q.ctx.Done():
		return nil
	case size := <-q.sizes:
		return &size
	}
}
func (q *terminalSizes) put(width, height int) {
	size := podexec.Size{Width: uint16(width), Height: uint16(height)}
	select {
	case q.sizes <- size:
	default:
		select {
		case <-q.sizes:
		default:
		}
		select {
		case q.sizes <- size:
		default:
		}
	}
}

func readTerminalInput(ctx context.Context, socket *terminalSocket, input *io.PipeWriter, sizes *terminalSizes, activity *atomic.Int64) error {
	var consumed int64
	limiter := rate.NewLimiter(100, 200)
	for {
		kind, data, err := socket.conn.ReadMessage()
		if err != nil {
			return err
		}
		if !limiter.Allow() {
			return apperr.New(apperr.CodeRateLimited, "终端输入过于频繁")
		}
		if kind != websocket.TextMessage {
			return apperr.New(apperr.CodeInvalid, "终端输入必须为 JSON 文本消息")
		}
		message, err := parseTerminalMessage(data)
		if err != nil || message.Token != "" {
			return apperr.New(apperr.CodeInvalid, "终端消息无效")
		}
		switch message.Type {
		case "stdin":
			if message.Data == "" || message.Width != 0 || message.Height != 0 {
				return apperr.New(apperr.CodeInvalid, "stdin 消息无效")
			}
			consumed += int64(len(message.Data))
			if consumed > 4<<20 {
				return apperr.New(apperr.CodeQuotaExceeded, "终端输入超过会话上限")
			}
			activity.Store(time.Now().UnixNano())
			if _, err := io.WriteString(input, message.Data); err != nil {
				return err
			}
		case "resize":
			if message.Data != "" || !validator.IsTerminalSize(message.Width, message.Height) {
				return apperr.New(apperr.CodeInvalid, "resize 消息无效")
			}
			sizes.put(message.Width, message.Height)
		case "close":
			if message.Data != "" || message.Width != 0 || message.Height != 0 {
				return apperr.New(apperr.CodeInvalid, "close 消息无效")
			}
			return nil
		default:
			return apperr.New(apperr.CodeInvalid, "不支持的终端消息")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func (a *API) runPipelineTerminal(ctx context.Context, cancel context.CancelFunc, socket *terminalSocket, session *service.PodTerminalSession, check func() error, o terminalRequest) {
	execCtx, stopExec := context.WithCancel(ctx)
	defer stopExec()
	reason := "DISCONNECTED"
	defer func() {
		cancel()
		auditCtx, auditCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer auditCancel()
		if err := session.Finish(auditCtx, reason); err != nil {
			a.log.Error("终端关闭审计失败", "err", err)
		}
	}()
	if err := check(); err != nil {
		reason = "ACCESS_CHANGED"
		socket.fail(err)
		return
	}
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	sizes := &terminalSizes{ctx: ctx, sizes: make(chan podexec.Size, 1)}
	sizes.put(o.width, o.height)
	var activity atomic.Int64
	activity.Store(time.Now().UnixNano())
	if err := socket.json(map[string]any{"type": "ready", "pod": session.Target.Pod, "podUID": session.Target.PodUID,
		"container": session.Target.Container, "shell": o.shell, "maxSeconds": int(podexec.MaxDuration.Seconds())}); err != nil {
		return
	}
	inputDone := make(chan error, 1)
	go func() { inputDone <- readTerminalInput(execCtx, socket, writer, sizes, &activity) }()
	execDone := make(chan error, 1)
	go func() { execDone <- session.Run(execCtx, input, socket, sizes) }()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			reason = "DEADLINE_OR_SHUTDOWN"
			if socket.outputFailed.Load() {
				reason = "OUTPUT_ERROR"
			}
			return
		case err := <-inputDone:
			stopExec()
			if err == nil {
				reason = "CLIENT_CLOSED"
			} else {
				var protocolErr *apperr.Error
				if errors.As(err, &protocolErr) {
					reason = "INPUT_REJECTED"
					socket.fail(protocolErr)
				}
			}
			return
		case err := <-execDone:
			if socket.outputFailed.Load() {
				reason = "OUTPUT_ERROR"
				return
			}
			code := 0
			reason = "EXITED"
			if err != nil {
				var exit utilexec.ExitError
				if errors.As(err, &exit) {
					code = exit.ExitStatus()
				} else {
					reason = "SOURCE_ERROR"
					socket.fail(apperr.New(apperr.CodeUnavailable, "终端连接失败或中断，请确认容器内存在所选 shell、API 身份有 pods/exec 权限且网络支持升级"))
					return
				}
			}
			_ = socket.json(map[string]any{"type": "exit", "exitCode": code})
			_ = socket.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shell exited"), time.Now().Add(time.Second))
			return
		case <-ticker.C:
			if time.Since(time.Unix(0, activity.Load())) > 5*time.Minute {
				reason = "IDLE_TIMEOUT"
				stopExec()
				socket.fail(apperr.New(apperr.CodeConflict, "终端连续 5 分钟无输入，已断开"))
				return
			}
			if err := check(); err != nil {
				reason = "ACCESS_CHANGED"
				stopExec()
				socket.fail(err)
				return
			}
			if err := socket.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}
