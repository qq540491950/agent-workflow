# 07: Agent 与技能注册表中文化(Agents + Skills)

**What to build:** Agent 注册表页与技能页全部为中文:Agents 页标题 `Agent`(实体名保留);权限表头 `文件读取/文件写入/Git 读取/Git 提交/Git 推送`(Switch 的 aria-label 保留后端键名 filesystem_read 等,读屏与后端口径一致);`Test→测试` 按钮(测试中… 保留);Skills 页标题 `技能`、表头 `名称/提供方`、值 `builtin→内置`、`Test→测试`。

**Blocked by:** 01 预重构:新建共享文案模块 labels.ts + 定稿英文白名单.

**Status:** ready-for-agent

- [ ] Agents 权限表头中文;aria-label 保留原始权限键名
- [ ] Agents/Skills 的 Test 按钮中文(测试中… 状态保留)
- [ ] Skills 表头与 builtin 中文化;副标题里的 Skill/Workflow Runtime 术语保留
- [ ] Settings 安全说明中权限口径(fs:write / git:commit / git:push)与 Agents 表头同口径中文
- [ ] `npx vitest run pages/Agents.test.tsx pages/Skills.test.tsx pages/Settings.test.tsx` 绿
