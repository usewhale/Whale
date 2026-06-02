# Dynamic Workflows

Whale 支持 **dynamic workflows**：JavaScript 脚本编排多个子 agent，
控制流由脚本决定（循环、扇出、barrier），每个 `agent()` 调用做实际的 LLM 工作。

如果你想先定义可复用的 reviewer、researcher 或 architect 角色，见
[自定义 Subagent](agents.md)。

> **Claude Code 兼容**
>
> Whale 的 workflow 脚本格式与 **Claude Code raw script 完全兼容**。
> 为 Claude Code 编写的 `.js` workflow 文件可以直接复制到
> `.whale/workflows/`（项目级）或 `~/.whale/workflows/`（全局）下使用，
> 无需修改脚本内容。

---

## 何时使用 Workflow

| 维度 | 普通对话 | Workflow |
|---|---|---|
| 谁决定下一步 | 模型逐轮决策 | 脚本 |
| 中间结果在哪 | 对话上下文 | 脚本变量 |
| 可重复性 | 每次即兴 | 编排被代码固化 |
| 规模 | 每轮几个 agent 调用 | 几十上百个 |
| 中断恢复 | 丢失上下文，重来 | 同 session 内可恢复 |

适用场景：

- **扇出式研究** — 并行搜索多个角度，交叉验证结论
- **多视角审查** — 从正确性/安全/性能等维度审查，然后综合
- **流水线处理** — 让多个条目依次经过提取→转换→加载等阶段
- **对抗性验证** — 让独立 agent 互相质疑，剔除不可靠的发现
- **循环直到枯竭** — 持续发现直到连续几轮无新结果

---

## 运行机制

- **隔离执行** — 脚本运行在 QuickJS 沙箱中，与对话上下文隔离
- **可恢复** — 同一 session 内，已完成 `agent()` 返回缓存结果
- **无宿主 API** — 脚本不能直接访问文件系统、网络、`require()`，所有 IO 通过 `agent()` 叶子节点
- **限制：**
  - 默认最大 **3 个并发 agent**
  - 可配置 agent 总调用上限
  - 可选 **token budget** 控制总消耗

---

## 内置 Workflow

### `deep-research`

深度研究：从多个角度并行搜索，抓取来源，对抗性验证，最终合成带引用的报告。

```
阶段：Scope → Search → Fetch → Verify → Synthesize
```

---

## 保存 Workflow

Whale 从两个位置发现 workflow 脚本：

| 位置 | 范围 | 共享方式 |
|---|---|---|
| `.whale/workflows/<name>.js` | **项目级** | 版本控制，团队共享 |
| `~/.whale/workflows/<name>.js` | **全局** | 个人所有项目可用 |

> 从 Claude Code 迁移：直接将 `.claude/workflows/<name>.js` 复制到上述任一目录即可。

项目级覆盖全局同名 workflow。保存后自动被发现——
在对话中描述你的需求，Whale 会按名调用。

---

## 管理运行

`/workflows` 打开工作流面板。

- `↑` / `↓` 选择阶段或 agent
- `Enter` / `→` 钻取详情（prompt、工具调用、结果）
- `Esc` 返回上一层
- `j` / `k` 在 agent 详情中滚动
- `p` 暂停/恢复
- `x` 停止运行

---

## 要求

所有付费计划可用（DeepSeek API）。功能默认关闭，可以按项目开启。

### 配置开关

在 TUI 中运行 `/config` 可以管理 workflow 开关：

- `Dynamic workflows`（`workflows.enabled`）控制 workflow runtime、`workflow` 工具、目录提示和 `/workflows` 面板集成。
- `Workflow keyword trigger`（`workflows.keyword_trigger_enabled`）只控制按 workflow 目录提示自动触发使用；关闭后仍可手动运行 workflow。

在 `/config` 中按 `Space` 只会切换当前项并产生未保存变更；按 `Enter` 或 `Ctrl+S` 才会保存。保存后会写入当前项目的个人配置文件：

```toml
# .whale/config.local.toml
[workflows]
enabled = true
keyword_trigger_enabled = true
```

`.whale/config.local.toml` 只影响当前 workspace，不建议提交到版本控制。如果希望团队共享默认值，可以把同样的 `[workflows]` 配置写入 `.whale/config.toml`。
