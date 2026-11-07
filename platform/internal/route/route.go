// Package route 统一注册业务路由并装配认证；请求处理由 handler 负责。
package route

import (
	"net/http"

	"github.com/githubtiger123/loom/platform/internal/config"
	"github.com/githubtiger123/loom/platform/internal/executor"
	"github.com/githubtiger123/loom/platform/internal/handler"
)

// New 保持全部业务接口经过同一认证入口，不在 handler 内注册 URL。
func New(api *handler.API) http.Handler {
	mux := http.NewServeMux()

	// 用户。
	register(mux, api, "GET /api/v1/me", api.Me)
	register(mux, api, "GET /api/v1/users", api.Users)
	register(mux, api, "POST /api/v1/users", api.CreateUser)
	register(mux, api, "POST /api/v1/local-users", api.CreateLocalUser)
	register(mux, api, "PUT /api/v1/users/{uid}/roles", api.ReplaceUserRoles)
	register(mux, api, "POST /api/v1/users/{uid}/enable", api.EnableUser)
	register(mux, api, "POST /api/v1/users/{uid}/disable", api.DisableUser)
	register(mux, api, "PUT /api/v1/users/{uid}/password", api.ResetPassword)
	register(mux, api, "DELETE /api/v1/users/{uid}/auth/sessions/{family}", api.AdminRevokeSession)

	// 角色与权限。
	register(mux, api, "GET /api/v1/roles", api.Roles)
	register(mux, api, "GET /api/v1/permissions", api.Permissions)
	register(mux, api, "POST /api/v1/roles", api.CreateRole)
	register(mux, api, "PUT /api/v1/roles/{rid}", api.UpdateRole)

	// 项目空间。
	register(mux, api, "GET /api/v1/groups", api.Groups)
	register(mux, api, "POST /api/v1/groups", api.CreateGroup)
	register(mux, api, "GET /api/v1/groups/{gid}", api.Group)
	register(mux, api, "PATCH /api/v1/groups/{gid}", api.UpdateGroup)
	register(mux, api, "POST /api/v1/groups/{gid}/disable", api.DisableGroup)
	register(mux, api, "POST /api/v1/groups/{gid}/restore", api.RestoreGroup)
	register(mux, api, "DELETE /api/v1/groups/{gid}", api.DeleteGroup)
	register(mux, api, "GET /api/v1/groups/{gid}/quota", api.GroupQuota)
	register(mux, api, "PUT /api/v1/groups/{gid}/quota", api.UpdateGroupQuota)

	// 成员与访问授权。
	register(mux, api, "GET /api/v1/groups/{gid}/members", api.Members)
	register(mux, api, "GET /api/v1/groups/{gid}/access", api.Accesses)
	register(mux, api, "GET /api/v1/groups/{gid}/roles", api.AvailableRoles)
	register(mux, api, "PUT /api/v1/groups/{gid}/members/{uid}/roles", api.ReplaceMemberRoles)
	register(mux, api, "DELETE /api/v1/groups/{gid}/members/{uid}", api.RemoveMember)
	register(mux, api, "POST /api/v1/groups/{gid}/owner-transfer", api.TransferOwner)

	// Notebook 业务：状态查询与访问入口均重新校验对象权限。
	register(mux, api, "GET /api/v1/groups/{gid}/notebook-images", api.NotebookImages)
	register(mux, api, "GET /api/v1/groups/{gid}/notebooks", api.Notebooks)
	register(mux, api, "POST /api/v1/groups/{gid}/notebooks", api.CreateNotebook)
	register(mux, api, "GET /api/v1/groups/{gid}/notebooks/{nid}", api.Notebook)
	register(mux, api, "PATCH /api/v1/groups/{gid}/notebooks/{nid}", api.UpdateNotebook)
	register(mux, api, "DELETE /api/v1/groups/{gid}/notebooks/{nid}", api.DeleteNotebook)
	register(mux, api, "POST /api/v1/groups/{gid}/notebooks/{nid}/start", api.StartNotebook)
	register(mux, api, "POST /api/v1/groups/{gid}/notebooks/{nid}/stop", api.StopNotebook)
	register(mux, api, "POST /api/v1/groups/{gid}/notebooks/{nid}/retry", api.RetryNotebook)
	register(mux, api, "GET /api/v1/groups/{gid}/notebooks/{nid}/status", api.NotebookStatus)
	register(mux, api, "GET /api/v1/groups/{gid}/notebooks/{nid}/events", api.NotebookEvents)
	register(mux, api, "GET /api/v1/groups/{gid}/notebooks/{nid}/logs", api.NotebookLogs)
	register(mux, api, "GET /api/v1/groups/{gid}/notebooks/{nid}/diagnostics", api.NotebookDiagnostics)
	register(mux, api, "GET /api/v1/groups/{gid}/notebooks/{nid}/usage", api.NotebookUsage)
	register(mux, api, "GET /api/v1/groups/{gid}/notebooks/{nid}/access", api.NotebookAccess)
	register(mux, api, "POST /api/v1/groups/{gid}/notebooks/{nid}/owner-transfer", api.TransferNotebookOwner)

	// 工作目录独立于实例保留；关联与删除都执行对象级权限检查。
	register(mux, api, "GET /api/v1/groups/{gid}/workspaces", api.Workspaces)
	register(mux, api, "GET /api/v1/groups/{gid}/workspaces/{wid}", api.Workspace)
	register(mux, api, "DELETE /api/v1/groups/{gid}/workspaces/{wid}", api.DeleteWorkspace)
	register(mux, api, "POST /api/v1/groups/{gid}/workspaces/{wid}/attach", api.AttachWorkspace)
	register(mux, api, "POST /api/v1/groups/{gid}/workspaces/{wid}/detach", api.DetachWorkspace)
	register(mux, api, "POST /api/v1/groups/{gid}/workspaces/{wid}/owner-transfer", api.TransferWorkspaceOwner)

	// Pipeline 定义、不可变版本及运行分离。
	register(mux, api, "GET /api/v1/groups/{gid}/pipelines", api.Pipelines)
	register(mux, api, "POST /api/v1/groups/{gid}/pipelines", api.CreatePipeline)
	register(mux, api, "GET /api/v1/groups/{gid}/pipelines/{pid}", api.Pipeline)
	register(mux, api, "DELETE /api/v1/groups/{gid}/pipelines/{pid}", api.DeletePipeline)
	register(mux, api, "GET /api/v1/groups/{gid}/pipelines/{pid}/versions", api.PipelineVersions)
	register(mux, api, "POST /api/v1/groups/{gid}/pipelines/{pid}/versions", api.CreatePipelineVersion)
	register(mux, api, "GET /api/v1/groups/{gid}/pipelines/{pid}/versions/{vid}", api.PipelineVersion)
	register(mux, api, "GET /api/v1/groups/{gid}/pipeline-runs", api.PipelineRuns)
	register(mux, api, "GET /api/v1/groups/{gid}/pipelines/{pid}/runs", api.PipelineRuns)
	register(mux, api, "POST /api/v1/groups/{gid}/pipelines/{pid}/runs", api.SubmitPipelineRun)
	register(mux, api, "GET /api/v1/groups/{gid}/pipeline-runs/{rid}", api.PipelineRun)
	register(mux, api, "GET /api/v1/groups/{gid}/pipeline-runs/{rid}/runtime", api.PipelineRunRuntime)
	register(mux, api, "DELETE /api/v1/groups/{gid}/pipeline-runs/{rid}", api.DeletePipelineRun)
	register(mux, api, "POST /api/v1/groups/{gid}/pipeline-runs/{rid}/stop", api.StopPipelineRun)
	register(mux, api, "GET /api/v1/groups/{gid}/pipeline-runs/{rid}/history", api.PipelineRunHistory)

	// Trainer 作业与 KServe 服务采用各自的生命周期，不能用统一 CRUD 假装运行。
	register(mux, api, "GET /api/v1/groups/{gid}/trainer-jobs", api.TrainerJobs)
	register(mux, api, "POST /api/v1/groups/{gid}/trainer-jobs", api.CreateTrainerJob)
	register(mux, api, "GET /api/v1/groups/{gid}/trainer-jobs/{jid}", api.TrainerJob)
	register(mux, api, "POST /api/v1/groups/{gid}/trainer-jobs/{jid}/stop", api.StopTrainerJob)
	register(mux, api, "DELETE /api/v1/groups/{gid}/trainer-jobs/{jid}", api.DeleteTrainerJob)
	register(mux, api, "GET /api/v1/groups/{gid}/trainer-jobs/{jid}/history", api.TrainerJobHistory)
	register(mux, api, "GET /api/v1/groups/{gid}/kserve-services", api.KServeServices)
	register(mux, api, "POST /api/v1/groups/{gid}/kserve-services", api.CreateKServe)
	register(mux, api, "GET /api/v1/groups/{gid}/kserve-services/{sid}", api.KServeService)
	register(mux, api, "PATCH /api/v1/groups/{gid}/kserve-services/{sid}", api.UpdateKServe)
	register(mux, api, "POST /api/v1/groups/{gid}/kserve-services/{sid}/deploy", api.DeployKServe)
	register(mux, api, "POST /api/v1/groups/{gid}/kserve-services/{sid}/stop", api.StopKServe)
	register(mux, api, "DELETE /api/v1/groups/{gid}/kserve-services/{sid}", api.DeleteKServe)
	register(mux, api, "GET /api/v1/groups/{gid}/kserve-services/{sid}/revisions", api.KServeRevisions)
	register(mux, api, "GET /api/v1/groups/{gid}/kserve-services/{sid}/history", api.KServeHistory)

	// 操作单与审计。
	register(mux, api, "GET /api/v1/operations/{oid}", api.Operation)
	register(mux, api, "POST /api/v1/operations/{oid}/retry", api.RetryOperation)
	register(mux, api, "GET /api/v1/audits", api.Audits)

	// 认证入口不经过业务 Bearer 校验，否则登录/刷新会形成认证循环。
	root := http.NewServeMux()
	root.HandleFunc("POST /api/v1/auth/login", api.Login)
	root.HandleFunc("POST /api/v1/auth/refresh", api.Refresh)
	root.HandleFunc("POST /api/v1/auth/logout", api.Logout)
	root.HandleFunc("POST /api/v1/auth/logout-all", api.LogoutAll)
	root.HandleFunc("GET /api/v1/auth/sessions", api.Sessions)
	root.HandleFunc("DELETE /api/v1/auth/sessions/{family}", api.RevokeSession)
	root.HandleFunc("POST /api/v1/auth/notebook-session", api.EstablishNotebookSession)
	root.HandleFunc("OPTIONS /api/v1/auth/notebook-session", api.EstablishNotebookSession)
	root.Handle("PUT /api/v1/auth/password", api.Authenticate(http.HandlerFunc(api.ChangePassword)))
	root.HandleFunc("/verify", api.Verify)
	root.HandleFunc("/verify/", api.Verify)
	root.Handle("/", api.Authenticate(mux))
	return root
}

func register(mux *http.ServeMux, api *handler.API, pattern string, endpoint handler.Endpoint) {
	mux.Handle(pattern, api.Wrap(pattern, endpoint))
}

// NewExecutor 只装配到独立 mTLS listener，不挂载到公网业务入口。
func NewExecutor(api *handler.API, cfg config.Executor) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/v1/ml-executions/claim", api.ClaimMLExecution)
	mux.HandleFunc("POST /internal/v1/ml-executions/{oid}/renew", api.RenewMLExecution)
	mux.HandleFunc("POST /internal/v1/ml-executions/{oid}/report", api.ReportMLExecution)
	mux.HandleFunc("POST /internal/v1/ml-executions/{oid}/observe", api.ObserveMLExecution)
	mux.HandleFunc("GET /internal/v1/ml-executions/{oid}", api.MLExecutionOperation)
	return executor.Authenticate(cfg, mux)
}
