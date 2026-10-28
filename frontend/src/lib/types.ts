// 领域类型(与后端 model 保持一致)。

export type NodeType =
  | "agent"
  | "skill"
  | "condition"
  | "parallel"
  | "merge"
  | "human"
  | "script"
  | "git"
  | "subworkflow";

export interface Position {
  x: number;
  y: number;
}

export interface WFNode {
  id: string;
  name: string;
  type: NodeType;
  config?: Record<string, unknown>;
  position: Position;
}

export interface WFEdge {
  from: string;
  to: string;
  condition?: string;
}

export interface WorkflowSettings {
  max_iterations?: number;
  on_loop_limit?: string;
  timeout_seconds?: number;
}

/** 声明式运行入参(后端 model.InputDecl;DSL 根级 inputs 段)。 */
export interface InputDecl {
  name: string;
  /** string | number | boolean(后端 InputType) */
  type: string;
  required?: boolean;
  default?: unknown;
  description?: string;
}

export interface Workflow {
  id: string;
  name: string;
  description: string;
  version: number;
  enabled: boolean;
  variables?: Record<string, unknown>;
  /**
   * 声明式运行入参(后端 model.Workflow.Inputs;DSL 根级 inputs 段)。
   * 设计器的工作流设置面板提供编辑器(InputsEditor);toWorkflowModel
   * 的原样带回逻辑保留 —— 面板没触碰过的字段仍不得在保存时静默抹掉。
   */
  inputs?: InputDecl[];
  /**
   * 工作流级权限覆盖(后端 model.Workflow.Permission;DSL 里写作根级
   * permissions 段)。
   *
   * 设计器**不提供**编辑入口,但保存时必须原样带回 —— 保存路径会重建整个
   * 工作流对象,漏掉这个字段等于把用户没碰过的权限覆盖静默抹掉。
   */
  permission?: Record<string, unknown>;
  nodes: WFNode[];
  edges: WFEdge[];
  settings: WorkflowSettings;
  created_at?: string;
  updated_at?: string;
  /**
   * 导入 DSL 时被解析器丢弃的键(仅导入接口的响应带这个字段)。
   * 空数组 = 没有键被忽略。
   */
  ignored_keys?: string[];
  /**
   * 保存/导入时的校验警告(响应带这个字段)。
   *
   * 不阻断保存,但必须让用户看到:此前警告只进后端日志,直连 API 的调用方
   * 永远看不到 —— 用户以为"DEPRECATED_NODE_CONFIG 的设置生效了"。
   * 两种模式形状一致(服务器模式平铺在响应体顶层,桌面模式由绑定返回)。
   */
  warnings?: ValidationError[];
}

// 执行状态(与后端 model.ExecutionState 一致)。
//
// **没有 "PAUSED"**:PAUSED 曾是执行状态,但**从来不可达** —— 没有任何代码
// 路径会把执行迁移到它(状态机里没有这条边)。后端已把它整条移除,老库里
// 遗留的行在**读出口**归一为 CANCELLED(不改库里的数据)。
//
// 前端此前还留着 "PAUSED":用户永远看不到这个状态(后端不会返回它),
// 留着只会让人以为"还有一种暂停中的执行",并写出永远走不到的分支。
export type ExecutionState =
  | "CREATED"
  | "RUNNING"
  | "WAITING_USER"
  | "COMPLETED"
  | "FAILED"
  | "CANCELLED";

export interface Execution {
  id: string;
  workflow_id: string;
  workflow_version: number;
  workflow_name: string;
  state: ExecutionState;
  task: string;
  /**
   * 执行变量(DSL variables 与声明式入参 inputs 合并后的生效值,已脱敏)。
   * 监控页据此展示"这次运行带了什么入参"。
   */
  variables?: Record<string, unknown>;
  current_node_id?: string;
  iterations?: Record<string, number>;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  error?: string;
  node_states?: Record<string, string>;
  state_data?: Record<string, unknown>;
  /**
   * 启动前的校验警告(响应带这个字段)。
   *
   * 不阻断运行,但必须让用户看到:此前只进后端日志,用户以为设置生效了。
   */
  warnings?: ValidationError[];
}

export interface ExecutionNode {
  id: string;
  execution_id: string;
  node_id: string;
  node_type: NodeType;
  node_name: string;
  state: string;
  attempt: number;
  output?: string;
  result_json?: string;
  error?: string;
  started_at?: string;
  finished_at?: string;
  duration_ms?: number;
}

export interface UIEvent {
  type: string;
  execution_id: string;
  node_id?: string;
  timestamp: string;
  data?: Record<string, unknown>;
}

export interface Artifact {
  id: string;
  execution_id: string;
  node_id: string;
  name: string;
  content_type: string;
  content: string;
  created_at: string;
}

export interface PermissionPolicy {
  filesystem_read: boolean;
  filesystem_write: boolean;
  git_read: boolean;
  git_commit: boolean;
  git_push: boolean;
}

export interface AgentInfo {
  id: string;
  name: string;
  model?: string;
  permissions: PermissionPolicy;
}

export interface SkillDTO {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
}

export interface ValidationError {
  code: string;
  node?: string;
  edge?: string;
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationError[];
  warnings: ValidationError[];
}

export interface AgentConfig {
  model: string;
  base_url: string;
  timeout_seconds: number;
  extra_args: string[];
  env: Record<string, string> | null;
  /**
   * 注意:后端结构体仍保留 `default_working_dir` 字段(存量配置要能解析),
   * 但它是**死设置** —— 运行期从不读取,后端会拒绝任何新值。
   * 前端不再声明、不再下发它(见 Agents.tsx 的说明文案)。
   */
  /** 仅 Mock Agent:演示用决策序列脚本 */
  behavior?: Record<string, { decisions?: string[]; summary_template?: string; fail_with?: string }> | null;
}
