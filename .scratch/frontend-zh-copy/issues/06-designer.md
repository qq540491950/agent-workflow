# 06: 设计器中文化(Designer)

**What to build:** 设计器全部为中文:节点类型名(条件/并行/汇合/人工/脚本/子工作流;Agent/Skill/Git 保留)用于左侧拖拽面板与节点副标题;属性面板行标签(模型/执行模式/工作目录/超时/参数(JSON)/Git 操作/子工作流 ID/变量/循环上限);下拉选项中的枚举值(plan/execute、fixed/exponential、string/number/boolean、git 子命令)保留英文。既有中文(校验/保存/运行/错误浮层等)不回退。

**Blocked by:** 01 预重构:新建共享文案模块 labels.ts + 定稿英文白名单.

**Status:** ready-for-agent

- [ ] nodeMeta 标签中文;左侧面板、画布节点副标题一致
- [ ] NodePropertyPanel 行标签中文;枚举值下拉保留英文
- [ ] WorkflowPropertyPanel 行标签(变量/循环上限/达到上限后/超时)中文
- [ ] `pages/designer.modes.test.tsx`(Mode→执行模式 2 处)等受影响断言同步
- [ ] `npx vitest run pages/designer.modes.test.tsx pages/designer.inputs.test.tsx pages/designer.rundialog.test.tsx` 绿
