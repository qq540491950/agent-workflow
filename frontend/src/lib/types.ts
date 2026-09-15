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
}

export interface Workflow {
  id: string;
  name: string;
  description: string;
  version: number;
  enabled: boolean;
  variables?: Record<string, unknown>;
  nodes: WFNode[];
  edges: WFEdge[];
  settings: WorkflowSettings;
  created_at?: string;
  updated_at?: string;
}

export type ExecutionState =
  | "CREATED"
  | "RUNNING"
  | "PAUSED"
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
  current_node_id?: string;
  iterations?: Record<string, number>;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  error?: string;
  node_states?: Record<string, string>;
  state_data?: Record<string, unknown>;
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
