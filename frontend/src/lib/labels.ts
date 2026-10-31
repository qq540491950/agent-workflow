// 界面文案的共享映射层(中文化专项的唯一来源)。
//
// 为什么要有这个模块:此前界面英文的根因是"文案直接从数据模型贴出来" ——
// 表头 = Go 结构体字段名、状态徽章 = 枚举值首字母大写、导航 = 路由名。
// 中文化后,所有"后端枚举值 → 用户可读文案"的映射必须只有一份来源:
// StateBadge、Executions 过滤按钮、ExecutionMonitor 节点状态共用这里,
// 避免前端自己演化出两份不一致的映射(约定 13"校验期与运行期同源"的
// 前端版)。
//
// ---- 英文白名单(以下内容刻意保留英文,不要翻译) ----
//
//   - 产品与技术栈名:Agent Workflow Orchestrator、ADK Go runtime · Wails 3
//   - 实体名:Agent、Skill、Workflow(作实体)、Git、YAML、DSL、SQLite
//   - AI 术语:Prompt、Token
//   - 协议枚举值(用户要写进 DSL/条件表达式的字面值):
//     decision 的 APPROVED/REJECTED/WAIT_USER/DONE、mode 的
//     plan/execute/review/fix/test/submit、入参类型 string/number/boolean、
//     退避策略 fixed/exponential、git 子命令 status/diff/log/branch/…
//   - 事件类型(node.started 等)与错误码(NOT_FOUND 等)
//   - ID、版本号 v3 等标识符
//
// 规则:**给用户看的词**用中文,**回传后端/写进 DSL 的值**保持原样;
// 未知枚举值一律回退原文(新状态上线而前端未跟上时,显示原文好过
// 显示错误的中文)。

/** 执行/节点状态 → 中文文案(与后端 execution/node 状态机一一对应)。 */
export const STATE_LABELS: Record<string, string> = {
  // 执行状态
  CREATED: "已创建",
  QUEUED: "排队中",
  RUNNING: "运行中",
  WAITING_USER: "等待人工",
  COMPLETED: "已完成",
  FAILED: "失败",
  CANCELLED: "已取消",
  // 节点状态
  PENDING: "待执行",
  SUCCESS: "成功",
  WAITING: "等待中",
  SKIPPED: "已跳过",
};

/**
 * 状态 → 中文;未知状态回退原文。
 * PAUSED 等已从状态机移除的状态也走回退(显示原文,不猜)。
 */
export function stateLabel(state: string): string {
  return STATE_LABELS[state] ?? state;
}

/** 侧栏运行模式(desktop/server)→ 中文;未知值回退原文。 */
export function modeLabel(mode: string): string {
  if (mode === "desktop") return "桌面";
  if (mode === "server") return "服务器";
  return mode;
}

/** 工作流/技能启用状态 → 中文。 */
export function enabledLabel(enabled: boolean): string {
  return enabled ? "已启用" : "已禁用";
}
