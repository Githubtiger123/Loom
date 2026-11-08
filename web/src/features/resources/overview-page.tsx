import { Link, useOutletContext, useParams } from 'react-router-dom';
import { Card } from '@atomus';
import { ArrowUpRight, BookOpen, Boxes, GitBranch, Radio } from 'lucide-react';
import type { Group } from '@/lib/types';
import { useGroupPermissions } from '@/lib/use-group-permissions';
import { PageHeading } from '@/components/feedback';

export function OverviewPage() {
  const { groupId = '' } = useParams();
  const permissions = useGroupPermissions(groupId);
  const group = useOutletContext<Group>();
  const modules = [
    {
      title: '开发环境',
      permission: 'notebook.read',
      path: 'notebooks',
      icon: BookOpen,
      description: '创建独立 Notebook，管理计算资源与开发会话。',
      action: '进入开发环境',
    },
    {
      title: 'Pipeline',
      permission: 'pipeline.read',
      path: 'pipelines',
      icon: GitBranch,
      description: '管理流程定义和版本，追踪每一次运行。',
      action: '浏览 Pipeline',
    },
    {
      title: '模型管理',
      permission: 'model.read',
      path: 'models',
      icon: Boxes,
      description: '按模型、版本和产物组织训练结果。',
      action: '查看模型资产',
    },
    {
      title: '推理服务',
      permission: 'kserve.read',
      path: 'kserve-services',
      icon: Radio,
      description: '查看模型服务的发布状态与访问地址。',
      action: '管理推理服务',
    },
  ];
  return (
    <div className="stack spacious">
      <PageHeading
        eyebrow="WORKSPACE OVERVIEW"
        title={`欢迎来到 ${group.displayName || group.name}`}
        description="从开发到上线，让机器学习工作在同一个空间中协作。"
      />
      <section className="overview-intro">
        <div>
          <span className="eyebrow">BUILD. ORCHESTRATE. SERVE.</span>
          <h2>你的下一次实验，从这里开始。</h2>
          <p className="muted">启动开发环境，编排可复用流程，让模型成为稳定运行的服务。</p>
        </div>
        <div className="intro-symbol" aria-hidden="true">
          <Boxes size={72} strokeWidth={1} />
        </div>
      </section>
      <div className="module-grid">
        {modules
          .filter((module) => permissions.can(module.permission))
          .map(({ title, path, icon: Icon, description, action }) => (
            <Card
              key={path}
              title={
                <>
                  <Icon size={20} />
                  {title}
                </>
              }
              supportingText={description}
              padding="lg"
              footer={
                <Link className="text-link" to={`/workspaces/${groupId}/${path}`}>
                  {action}
                  <ArrowUpRight size={16} />
                </Link>
              }
            />
          ))}
      </div>
    </div>
  );
}
