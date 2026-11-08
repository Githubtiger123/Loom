export const resourceModules = {
  pipelines: {
    title: 'Pipeline',
    eyebrow: 'ORCHESTRATION',
    description: '组织可复用的训练流程，管理定义和版本。',
    create: '创建 Pipeline',
  },
  models: {
    title: '模型管理',
    eyebrow: 'MODEL REGISTRY',
    description: '在统一目录中管理模型、版本和模型产物。',
    create: '登记模型',
  },
  'kserve-services': {
    title: '推理服务',
    eyebrow: 'MODEL SERVING',
    description: '查看 KServe 服务的发布状态、版本和访问地址。',
    create: null,
  },
} as const;

export type ResourceKind = keyof typeof resourceModules;
