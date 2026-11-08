// Package model 是数据库表的运行时投影，字段与 migrations/*.sql 一一对应。
//
// 约定：
//   - 时间字段一律为 UTC
//   - 不做物理删除，生命周期用 status 表达
//   - json 标签用于返回前端，不用于数据库映射
package model

import (
	"encoding/json"
	"time"
)

// User 保存稳定身份和业务状态；可来自外部 IdP 或显式配置的本地 issuer。
// 本地密码与 Access/Refresh 摘要保存在独立 SQL 表，不混入业务身份响应。
type User struct {
	UpdatedAt time.Time  `json:"-" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
	DeletedAt *time.Time `json:"-"`

	// 自增主键。内部引用一律用它，不用 username 或 email 做外键。
	ID uint64 `json:"id" gorm:"primaryKey;autoIncrement"`

	// OIDC issuer（IdP 的标识 URL）。
	// 必须与 Subject 一起构成唯一身份：同一个邮箱可能存在于两个 IdP，单靠 subject 会串号。
	Issuer string `json:"issuer"`

	// OIDC subject，身份的唯一稳定标识。
	// 允许为空，是因为"先被邀请、后首次登录"时平台只有邮箱，登录成功后回填。
	Subject *string `json:"subject"`

	// 登录邮箱，必须与 kubeflow-userid 完全一致（大小写、别名都要一致），
	// 否则平台建的绑定和 Istio 的 AuthorizationPolicy 都匹配不上。
	Username string `json:"username"`

	// 展示名，来自 IdP 的 name claim，仅用于 UI，不参与任何判定。
	DisplayName string `json:"displayName"`

	// 邮箱副本，可能为空（部分 IdP 的 subject 模式不返回 email）。
	// 判定身份用 Username，不要用 Email。
	Email *string `json:"email"`

	// 账号状态：ACTIVE 正常 / DISABLED 禁用。
	// 禁用后所有权限判定立即失效，但历史绑定保留，便于恢复与审计。
	Status string `json:"status"`

	// 授权版本号，每次权限变更 +1。
	// 权限缓存以此为失效依据：版本变了，旧缓存自然不再被命中，
	// 比"逐个删 key"更可靠（不会漏删、不需要遍历）。
	AuthorizationVersion uint64 `json:"authorizationVersion" gorm:"default:1"`
	// 与业务授权版本分离：登出全部设备、改密、重置及状态变更均使旧会话失效。
	SessionVersion uint64 `json:"-" gorm:"default:1"`

	// 最后一次成功登录时间，用于闲置账号治理；从未登录过时为空。
	LastLoginAt *time.Time `json:"lastLoginAt"`

	// 创建时间，UTC。
	CreatedAt time.Time `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
}

// Permission 是一个"权限点"，粒度是资源 + 动作，例如 notebook.create。
//
// 权限点是最小授权单元：角色是它的集合，用户通过角色拿到它。
// 代码里禁止比较角色码做判断，一律比较权限点。
type Permission struct {
	// 主键。
	ID uint64 `json:"id"`

	// 权限码，格式 resource.action，全局唯一。
	// 不存通配符（如 notebook.*），通配符只在"配置角色"时展开成具体码。
	Code string `json:"code"`

	// 人读名称，用于权限矩阵页面。
	Name string `json:"name"`

	// 生效范围：global（平台级，如 group.create）或 group（组内，如 notebook.create）。
	// 绑定的 scope 与角色的 scope 必须与它一致，判定才有效，
	// 这样可以防止"组角色被挂到全局"这类越权。
	Scope string `json:"scope" gorm:"column:scope_type"`

	// 所属模块，仅用于前端分组展示（group / notebook / quota / audit…）。
	Module string `json:"module"`
}

// Role 是权限点的集合，同时决定"绑定能挂在哪一层"。
type Role struct {
	CreatedAt time.Time `json:"-" gorm:"default:CURRENT_TIMESTAMP(3)"`
	UpdatedAt time.Time `json:"-" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`

	// 主键。
	ID uint64 `json:"id"`

	// 角色码，全局唯一，也是 API 入参使用的值（如 group_owner）。
	Code string `json:"code"`

	// 人读名称。
	Name string `json:"name"`

	// 角色的作用范围：global 或 group。
	// 与权限点的 scope、绑定的 scope 三者必须一致，否则该绑定不参与判定。
	Scope string `json:"scope" gorm:"column:scope_type"`

	// 访问等级：edit / view / none。
	// 只用于列表页展示"这个人对我这组有多大权限"，
	// 真正的授权判定仍走权限点，不要用等级代替权限码。
	AccessLevel string `json:"accessLevel"`

	// 是否内置角色。内置角色不允许删除，也不允许改 Code。
	Builtin bool `json:"builtin"`

	// 是否启用。停用后该角色带来的权限立即失效，绑定行保留。
	Enabled bool `json:"enabled"`

	// 角色配置版本号，权限点集合变化时 +1，用于判断缓存是否过期。
	Version uint64 `json:"version" gorm:"default:1"`

	// 描述，用于权限矩阵页面说明"这个角色能干什么"。
	Description string `json:"description"`

	// 该角色包含的权限码。不是数据库列，由 tb_role_permission 联表填充。
	Permissions []string `json:"permissions" gorm:"-"`
}

// Group 是"项目空间"：一个 Group 对应一个 Kubernetes namespace，也对应一个 Profile。
//
// 平台以组为资源与成本的边界：额度、成员、数据集都按组划分。
// 注意：Profile 的 owner（InfrastructureOwner）不是业务上的组负责人，
// 真正的成员授权在 tb_role_binding 里。
type Group struct {
	UpdatedAt time.Time  `json:"-" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`
	DeletedAt *time.Time `json:"-"`

	// 主键，业务侧引用组一律用它。
	ID uint64 `json:"id"`

	// 对外暴露的不可猜测 ID（UUID），用于日志与操作单关联，避免暴露自增规律。
	InstanceID string `json:"instanceId"`

	// 所属集群标识，多集群场景下区分同名组。
	Cluster string `json:"cluster"`

	// 组名，同时是 Profile 名和 namespace 名（DNS-1123，≤63 字符）。
	Name string `json:"name"`

	// 展示名，仅 UI 使用，可随意修改。
	DisplayName string `json:"displayName"`

	// 描述。
	Description string `json:"description"`

	// 创建者的用户 ID，仅审计用，不参与授权判定。
	CreatedBy uint64 `json:"createdBy"`

	// 基础设施责任人：写进 Profile.spec.owner 的身份。
	// 通常是固定的运维账号，不随组员流动而变化。
	InfrastructureOwner string `json:"infrastructureOwner"`

	// 期望状态：CREATING / ACTIVE / DEGRADED / DELETING / DELETED / FAILED。
	// 由平台写入，代表"我们希望它是什么样"。
	Status string `json:"status"`

	// 观测状态：从集群实际读到的结果（READY / ERROR / PENDING…）。
	// 与 Status 分离存放，避免"提交成功就当成已就绪"。
	ObservedState string `json:"observedState"`

	// Profile 对象的 UID。用于识别"同名但已被重建"：UID 变了说明对象被删过。
	ProfileUID *string `json:"profileUid"`

	// namespace 对象的 UID，用途同上。
	NamespaceUID *string `json:"namespaceUid"`

	// 期望额度（CPU/内存/GPU 等维度的原始 JSON），提交 Profile 时的输入。
	// 用 JSON 存是因为维度会变（以后可能加 NPU、存储），不值得为每个维度建一列。
	QuotaRequested json.RawMessage `json:"quotaRequested" gorm:"type:json"`

	// 观测额度：从 ResourceQuota 读回的实际值。
	// 只用于展示与对账，不作为额度判定依据（判定读 K8s）。
	QuotaObserved json.RawMessage `json:"quotaObserved" gorm:"type:json"`

	// 最后一次成功观测的时间；从未成功同步过时为空。
	ObservedAt *time.Time `json:"observedAt"`

	// 配置版本号，每次修改组的期望配置 +1。
	// 同步器用它判断"手上这个操作是否已过期"（乐观并发控制）。
	Revision uint64 `json:"revision" gorm:"default:1"`

	// 最近一次失败原因，成功后清空。用于前端直接展示，不必翻日志。
	LastError *string `json:"lastError"`
}

// Binding 是"谁在什么范围拥有什么角色"，是平台业务授权的唯一来源。
//
// 注意它和 Kubernetes 的 RoleBinding 是两回事：
// 这里决定"能不能调用平台 API"，K8s 那份决定"容器里能不能操作对象"。
type Binding struct {
	Status    string     `json:"-" gorm:"default:ACTIVE"`
	GrantedBy uint64     `json:"-"`
	GrantedAt time.Time  `json:"-" gorm:"default:CURRENT_TIMESTAMP(3)"`
	RevokedBy *uint64    `json:"-"`
	RevokedAt *time.Time `json:"-"`
	Version   uint64     `json:"version" gorm:"default:1"`

	// 主键。
	ID uint64 `json:"id"`

	// 被授权的用户 ID。
	UserID uint64 `json:"userId"`

	// 授予的角色 ID。
	RoleID uint64 `json:"roleId"`

	// 角色码副本，联表查询时填充，方便直接返回前端（不是数据库列）。
	RoleCode string `json:"roleCode" gorm:"->;-:migration"`

	// 授权范围：global（全局，如平台管理员）或 group（某个组内）。
	Scope string `json:"scope" gorm:"column:scope_type"`

	// 范围 ID：scope=global 时为 0，scope=group 时为 tb_group.id。
	ScopeID uint64 `json:"scopeId"`

	// 过期时间。为空表示永久有效；到期后判定自动失效，不需要定时任务删除。
	ExpiresAt *time.Time `json:"expiresAt"`
}

// Member 是"组成员视图"，由 user + binding + role 联表查询得到，不是一张表。
type Member struct {
	// 用户 ID。
	UserID uint64 `json:"userId"`

	// 登录邮箱（= kubeflow-userid）。
	Username string `json:"username"`

	// 展示名。
	DisplayName string `json:"displayName"`

	// 该成员在本组的全部有效角色。
	Roles []Binding `json:"roles"`

	// 期望访问级别（edit / view / none），由业务绑定聚合而来。
	DesiredLevel string `json:"desiredLevel"`

	// 已生效的访问级别，从集群实际状态回读。
	// 它与 DesiredLevel 不一致时说明"库里加了人、集群里没加上"，需要重试。
	ObservedLevel string `json:"observedLevel"`

	// 同步状态：PENDING / APPLYING / READY / REVOKING / FAILED / DRIFTED。
	AccessStatus string `json:"accessStatus"`

	// 最近一次同步失败原因。
	LastError *string `json:"lastError"`
}

// Operation 是"长事务操作单"：一次跨系统（DB + K8s）的写操作对应一行。
//
// 它是可重试的载体：外部调用失败后不靠内存重试，而是由 worker 依据状态与租约继续推进。
// 状态机：PENDING → RUNNING → SUCCEEDED / RETRYING / FAILED / SUPERSEDED。
type Operation struct {
	UpdatedAt time.Time `json:"-" gorm:"autoUpdateTime:false;default:CURRENT_TIMESTAMP(3)"`

	// 表内自增主键与业务 UUID 分开映射，避免 GORM 把 UUID 写入 id。
	RowID         uint64     `json:"-" gorm:"column:id;primaryKey;autoIncrement"`
	RequestKey    string     `json:"-"`
	PayloadHash   string     `json:"-"`
	Cluster       string     `json:"-"`
	NextAttemptAt time.Time  `json:"-" gorm:"default:CURRENT_TIMESTAMP(3)"`
	LeaseOwner    *string    `json:"-"`
	LeaseUntil    *time.Time `json:"-"`
	DeadlineAt    *time.Time `json:"-"`
	FinishedAt    *time.Time `json:"-"`

	// 业务主键（UUID），不是自增 ID：它同时是幂等键与日志关联 ID。
	ID string `json:"operationId" gorm:"column:operation_id;uniqueIndex"`

	// 发起操作的用户 ID；0 表示由系统或 worker 发起。
	ActorID uint64 `json:"actorId"`

	// 动作码，如 group.create、access.reconcile，worker 按它分发处理逻辑。
	Action string `json:"action"`

	// 目标类型：group / notebook / access。
	TargetType string `json:"targetType"`

	// 目标标识，空间使用组 ID，访问任务使用 "组ID/用户ID" 组合。
	TargetKey string `json:"targetKey"`

	// 目标版本号。处理时与当前版本比对，不一致说明操作已过期，
	// 直接标记为 SUPERSEDED，避免用旧参数覆盖新配置。
	TargetVersion uint64 `json:"targetVersion" gorm:"default:1"`

	// 当前状态，见上方状态机。
	Status string `json:"status"`

	// 当前步骤，用于失败后从断点继续，也用于前端展示"卡在哪一步"。
	Step string `json:"step"`

	// 请求负载（原始 JSON）。不返回前端（json:"-"），可能含内部参数。
	Payload json.RawMessage `json:"-" gorm:"type:json"`

	// 执行结果（原始 JSON），成功后填充，作为幂等重放时的返回体。
	Result json.RawMessage `json:"result" gorm:"type:json"`

	// 已尝试次数，用于退避与"超过阈值转人工"。
	Attempt uint `json:"attempt"`

	// 最近一次失败原因，成功后清空。
	LastError *string `json:"lastError"`

	// 创建时间，UTC。
	CreatedAt time.Time `json:"createdAt" gorm:"default:CURRENT_TIMESTAMP(3)"`
}

func (User) TableName() string       { return "tb_user" }
func (Permission) TableName() string { return "tb_permission" }
func (Role) TableName() string       { return "tb_role" }
func (Group) TableName() string      { return "tb_group" }
func (Binding) TableName() string    { return "tb_role_binding" }
func (Operation) TableName() string  { return "tb_operation" }
