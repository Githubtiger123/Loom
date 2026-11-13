import { Card } from '@atomus';
import type { Definition } from '../types';

export function policyObject(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}
function scalar(value: unknown, fallback = '未显式声明') {
  return typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean'
    ? String(value)
    : fallback;
}
export function hasCondition(definition?: Definition) {
  return typeof policyObject(definition?.triggerPolicy)?.condition === 'string';
}
export function hasLoop(definition?: Definition) {
  return !!(definition?.parameterIterator || definition?.artifactIterator);
}

export function NodePolicySummary({ definition }: { definition: Definition }) {
  const trigger = policyObject(definition.triggerPolicy);
  const iterator = policyObject(definition.parameterIterator);
  const items = policyObject(iterator?.items);
  const retry = policyObject(definition.retryPolicy);
  const cache = policyObject(definition.cachingOptions);
  const iteratorPolicy = policyObject(definition.iteratorPolicy);
  let count: number | undefined;
  if (typeof items?.raw === 'string') {
    try {
      const value: unknown = JSON.parse(items.raw);
      if (Array.isArray(value)) count = value.length;
    } catch {
      /* Preserve unknown IR, never execute an expression. */
    }
  }
  const strategy = trigger?.strategy;
  const strategyLabel =
    strategy === 'ALL_UPSTREAM_TASKS_SUCCEEDED' || strategy === 1
      ? '上游任务全部成功后触发'
      : strategy === 'ALL_UPSTREAM_TASKS_COMPLETED' || strategy === 2
        ? '上游任务全部结束后触发（包括失败）'
        : strategy === 'TRIGGER_STRATEGY_UNSPECIFIED' || strategy === 0 || strategy === undefined
          ? '默认：上游任务全部成功后触发'
          : scalar(strategy, '未识别的触发规则，请核对原始 IR');
  return (
    <section className="stack">
      <h4>执行策略（只读）</h4>
      <p className="small muted">
        来自此运行固定的
        IR；这里只解释，不在浏览器计算条件、展开循环或修改运行策略。实际行为还受冻结平台策略限制。
      </p>
      <div className="pipeline-columns">
        <Card title="条件与触发">
          <p>{strategyLabel}</p>
          {hasCondition(definition) ? (
            <>
              <p>还需满足以下表达式；由官方 driver 在运行时求值，不是 JavaScript。</p>
              <pre className="pipeline-code">{scalar(trigger?.condition)}</pre>
            </>
          ) : (
            <p className="muted">未声明额外条件表达式；不代表该任务一定执行。</p>
          )}
        </Card>
        <Card title="循环">
          {iterator ? (
            <dl className="details-grid">
              <dt>循环变量输入</dt>
              <dd>{scalar(iterator.itemInput)}</dd>
              <dt>迭代来源</dt>
              <dd>
                {typeof items?.inputParameter === 'string'
                  ? `运行输入参数：${items.inputParameter}`
                  : count !== undefined
                    ? `IR 内置列表 · ${count} 项`
                    : '由运行时解析，当前无法确定项数'}
              </dd>
              <dt>并行度上限</dt>
              <dd>
                {iteratorPolicy?.parallelismLimit === 0 || iteratorPolicy?.parallelismLimit === '0'
                  ? 'IR 未限制；仍受集群／平台限制'
                  : scalar(iteratorPolicy?.parallelismLimit, '未显式设置，遵循引擎／平台配置')}
              </dd>
            </dl>
          ) : (
            <p className="muted">
              {definition.artifactIterator
                ? '声明产物迭代；当前引擎白名单尚不支持，不能据此判断可执行。'
                : '此定义未声明循环；仍可能位于上层循环中。'}
            </p>
          )}
        </Card>
        <Card title="自动重试">
          <dl className="details-grid">
            <dt>额外重试上限</dt>
            <dd>
              {scalar(retry?.maxRetryCount, retry ? '0（未声明额外重试）' : '未配置任务级重试')}
            </dd>
            <dt>初始退避间隔</dt>
            <dd>{scalar(retry?.backoffDuration, '由官方引擎默认值决定')}</dd>
            <dt>退避倍率</dt>
            <dd>{scalar(retry?.backoffFactor, '由官方引擎默认值决定')}</dd>
            <dt>累计重试时长上限</dt>
            <dd>{scalar(retry?.backoffMaxDuration, '由官方引擎默认值决定')}</dd>
            <dt>重试触发策略（IR）</dt>
            <dd>{scalar(retry?.policy, '未显式声明，遵循官方引擎默认规则')}</dd>
          </dl>
          <p className="small muted">
            配置重试不等于已发生重试；物理 Pod 历史以执行记录为准。容器 previous
            日志不等于另一个重试 Pod。
          </p>
        </Card>
        <Card title="缓存">
          <p>
            {cache?.enableCache === true
              ? 'IR 允许缓存；仍需平台允许且有有效缓存结果。'
              : cache?.enableCache === false
                ? 'IR 显式关闭缓存。'
                : 'IR 未显式声明缓存开关，不能由此确认命中。'}
          </p>
          {cache?.cacheKey !== undefined && (
            <p className="break-word">自定义缓存键：{scalar(cache.cacheKey)}</p>
          )}
          <p className="small muted">
            实际命中看实例的 CACHED 状态或缓存来源 ID；缓存命中不是本次新执行，不能把源 Pod
            日志当成本次日志。
          </p>
        </Card>
      </div>
      <details>
        <summary>查看原始 IR 策略字段</summary>
        <pre className="pipeline-code">
          {JSON.stringify(
            {
              triggerPolicy: definition.triggerPolicy,
              parameterIterator: definition.parameterIterator,
              artifactIterator: definition.artifactIterator,
              iteratorPolicy: definition.iteratorPolicy,
              retryPolicy: definition.retryPolicy,
              cachingOptions: definition.cachingOptions,
            },
            null,
            2,
          )}
        </pre>
      </details>
    </section>
  );
}
