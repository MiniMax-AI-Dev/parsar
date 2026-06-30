# Spec & Memory 使用指南

> 适用对象:产品 / 运营 / 在 Web 后台维护 spec 与 memory 的最终用户。
> 内部架构与扩展说明见 [spec-memory-module.md](./spec-memory-module.md) 与 [spec-memory-dev.md](./spec-memory-dev.md)。

Spec 与 Memory 是把"项目约定 / 用户偏好 / 长期背景"沉淀下来,自动注入到每次 agent 对话的中心化机制。
维护一次,所有 sandbox(claude code / opencode / codex)自动读到;agent 在 sandbox 内发现新的稳定信息也能写回。

---

## 1. UI 入口

后台导航 → **沉淀资产**:

```text
?admin=specs    # 工程 spec(workspace 级,跟项目走)
?admin=memory   # 记忆(user / project 两个 tab)
```

进入页面前请确认当前已选 workspace;Memory 的 project tab 还需要再选一个 project。

---

## 2. Spec(工程约定)

**Scope:** workspace 级。同一 workspace 下所有 project / sandbox 共享。

**典型内容:**
- 项目用的技术栈、必须遵守的命名规则、目录约定
- 不能用某个库 / 函数(以及原因)
- 代码风格(行宽、注释语言、commit message 格式)
- 团队的 review checklist

**字段:**
| 字段 | 必填 | 说明 |
|------|------|------|
| Title | 必填 | 一句话概括 |
| Body | 必填 | 完整规则。允许多段 markdown,但 agent 注入时按纯文本处理 |
| Tags | 可选 | 逗号分隔。MVP 不按 tag 过滤注入,但能帮 UI 检索 |

**Source 标记(只读):**
- `manual` — 用户在 UI 手动建/改
- `agent` — agent 在 sandbox 内通过 `parsar spec add` 写回
- `import` — 从粘贴的文本批量导入

### 2.1 手动创建

页面右上角 **新建片段** → 填 Title + Body + Tags(可选) → 保存。
保存后立即生效;下一个开启的 sandbox 在 SessionStart 阶段就会注入。

### 2.2 从文本导入

页面右上角 **导入** → 粘贴一段 markdown → **预览**。

后端按 H2/H3 切片:每个 `##` 或 `###` 标题成为一个 fragment,标题=fragment.title,标题下的正文=fragment.body。
预览里能看到每个 fragment 的 title / body,**只读**(切错就回退去改原文重新预览)。
确认无误 → **导入**,所有 fragment 一次性入库,source=`import`。

部分失败的情况:服务端写一半挂掉时会返回错误,已经入库的 fragment 保留(可在列表里看到);未入库的部分需要重新整理后再粘贴。

### 2.3 编辑 / 删除

点列表行 → 弹编辑框。
删除走二次确认,执行后写 audit。

---

## 3. Memory(记忆)

**Scope:** 两种,通过 tab 切换。

| Scope | 跟随 | 典型用途 |
|-------|------|----------|
| **user** | 当前账号(跨 workspace) | 个人偏好、长期角色信息、不想每次重复说明的事 |
| **project** | 选中的 project | 当前项目的背景、决策动因、阶段性目标 |

**4 种 memory type:**

| Type | 用法 | Why 字段建议 |
|------|------|-------------|
| **user** | 用户的角色、偏好、长期目标 | 通常不填 |
| **feedback** | 用户明确纠正过的事 / 已确认的非显然决策 | **强烈建议填**(避免日后忘了为什么) |
| **project** | 项目背景、里程碑、决策动因 | **强烈建议填** |
| **reference** | 外部仪表盘 / 文档 / Slack channel 等指针 | 一般不填 |

**字段:**
| 字段 | 必填 | 说明 |
|------|------|------|
| Type | 必填 | 4 选 1。**编辑模式下不可修改**(若选错,删除后重建) |
| Title | 可选 | 简短标题,辅助记忆 |
| Body | 必填 | 主体内容 |
| Why | 可选 | 推荐 feedback / project 类填,解释决策背景 |
| Tags | 可选 | 逗号分隔 |

### 3.1 类型筛选

列表顶部的胶囊按钮可按 type 过滤(All / user / feedback / project / reference)。
仅本地筛选,不影响后端拉取。

### 3.2 Audit(仅 project memory)

每条 project memory 行右侧有 **Audit** 按钮 → 弹出该条 memory 的事件时间线(谁、何时、做了什么、actor 类型是 user 还是 agent)。

**user memory 没有 Audit 入口** — MVP 阶段的 audit 读接口只支持 project scope,user-scope 的事件目前查不到。
要看记录请用全局 Audit 页(`?admin=audit`)。

### 3.3 Agent 写回

Sandbox 里的 agent 会在合适时机自动调 `parsar memory add`(由注入到 system prompt 的 memory-write-guide 引导)。
agent 写入的行 source 列显示 `agent`,Audit 时间线里能看到 actor_type=agent 的事件。

---

## 4. 自动注入

| 时机 | Claude Code | OpenCode | Codex |
|------|-------------|----------|-------|
| Sandbox 启动(SessionStart) | hook 注入 spec + memory 全量 | plugin 注入 spec + memory 全量 | 启动时生成 `AGENTS.md` |
| 每轮 prompt 前(per-turn) | hook 注入增量 memory | plugin 注入增量 memory | **不支持** |
| Agent 写回 memory | 当轮 hook 拉到 | 当轮 plugin 拉到 | **下一次会话才生效** |

**Codex 已知限制:** 没有 per-turn hook,sandbox 内 agent 写回的 memory 必须重启 session 才会进入 prompt。这是平台限制,不是 bug。

**注入大小:** MVP 一次性全量注入,不做截断。如果你 memory 攒到几百条导致 prompt 体积明显膨胀,先删一些过时项(二期会按 tag / 时间窗智能筛选)。

---

## 5. Sandbox 内的 `parsar` CLI

sandbox 启动时 `/usr/local/bin/parsar` 已经预装,token 通过 `PARSAR_RUNNER_TOKEN` 环境变量注入,无需登录。

常用命令:

```sh
# spec
parsar spec list
parsar spec add --title "代码风格" --body "行宽 100,4 空格缩进"
parsar spec rm <id>

# memory
parsar memory list --scope user
parsar memory list --scope project --type feedback
parsar memory add --type feedback --body "..." --why "..."
parsar memory rm <id>

# 调试用:重拉当前注入快照
parsar sync
```

人类用户基本不需要直接调 CLI — UI 已能覆盖所有场景。CLI 主要是给 agent 在 sandbox 内调用,以及偶尔运维排查。

---

## 6. 什么该写 / 不该写

**该写:**
- 跨会话稳定的事实、偏好、约定
- 用户明确给过的纠正(尤其是不显然的)
- 项目的关键决策动因(避免下次又被问"为什么这么做")

**不该写:**
- 从代码 / git history 能推断的内容(agent 自己会读)
- bug fix 流水账(代码就是答案)
- 一次性任务上下文(临时聊一聊就过的)

如果不确定要不要写,先不写 — 错过比错存好回滚。

---

## 7. 常见问题

**Q: spec 改了为什么 agent 还引用旧的?**
A: 当前 sandbox session 已经开了,新 spec 要等下一次 SessionStart 才生效(per-turn 增量只走 memory 不走 spec)。重开一个 sandbox 即可。

**Q: 误删能恢复吗?**
A: DB 层是软删,但 UI 未提供"已删除列表"。如果删错,请联系平台维护者从 DB 直接恢复 deleted_at=NULL。

**Q: 不同 workspace 能共享 spec 吗?**
A: 不能。Spec 强绑定 workspace。如果是公司级通用规则,目前需要在每个 workspace 各维护一份(二期会做团队 / 组织级 scope)。

**Q: User memory 切换 workspace 会丢吗?**
A: 不会。User memory 绑定账号本身,跨 workspace 始终可见。

**Q: 看到 source=`agent` 的条目想删,会不会被 agent 再写回来?**
A: 不会自动 — agent 写 memory 的触发是用户对话里的新信息,删掉的内容除非用户再次提起,否则不会复活。
