# 01: 预重构:新建共享文案模块 labels.ts + 定稿英文白名单

**What to build:** 为中文化工作提供唯一文案来源:新建共享文案模块,包含执行/节点状态的中文映射(`stateLabel`)等纯函数与其单测;模块注释中定稿"保留英文白名单"(产品名、实体名 Agent/Skill/Workflow、Prompt、协议枚举值 decision/mode、git 子命令等)。本票**无任何 UI 变化**,纯增量,全量测试保持绿。

**Blocked by:** None (can start immediately).

**Status:** ready-for-agent

- [x] `frontend/src/lib/labels.ts` 存在,导出 `STATE_LABELS`、`stateLabel()`(未知状态回退原文)
- [x] labels 配套单测覆盖:已知状态映射、未知状态回退
- [x] 模块头注释写明白名单与使用规则(界面文案"该中文的都中文")
- [x] `npx vitest run` 与 `npm run build` 全绿,无 UI 行为变化
