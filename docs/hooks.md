# Hooks

Hooks 让你在 Whale 的关键生命周期点运行自己的脚本。常见用途包括：启动会话时写入团队上下文、工具执行前做策略检查、工具执行后做日志记录、用户提交 prompt 时做格式检查，或在 Whale 结束本轮回复前补充检查。

Hooks 是 shell 命令。Whale 会把当前事件的 JSON payload 写到命令的 stdin，命令可以什么都不输出，也可以在 stdout 返回 JSON 来影响后续行为。

## 放在哪里

项目级 hooks 写在仓库的 `.whale/config.toml`：

```toml
[[hooks.SessionStart]]
command = "printf 'Whale session started\n' >> .whale/hooks.log"
```

个人 hooks 可以写在 `.whale/config.local.toml`，不要提交：

```toml
[[hooks.UserPromptSubmit]]
command = "python3 .whale/hooks/check_prompt.py"
timeout = 10
```

全局 hooks 可以写在 `~/.whale/config.toml`，会应用到所有项目。

## 最小可用例子

这个 hook 会在 `shell_run` 工具执行前运行：

```toml
[[hooks.PreToolUse]]
match = "shell_run"
command = "python3 .whale/hooks/check_shell.py"
timeout = 600
```

`timeout` 的单位是秒，默认值是 `600`。如果 hook 超时，Whale 会记录 `decision:timeout`。对于 `PreToolUse`、`PermissionRequest` 和 `UserPromptSubmit`，超时会阻止后续动作。

## 事件

| 事件 | 什么时候运行 | 常见用途 |
|---|---|---|
| `SessionStart` | 新会话开始时 | 注入项目说明、检查环境 |
| `UserPromptSubmit` | 用户提交 prompt 时 | 检查输入、阻止危险请求、改写 prompt |
| `PreToolUse` | 工具执行前 | 审核 shell 命令、阻止危险工具调用 |
| `PermissionRequest` | Whale 请求权限时 | 自动允许或拒绝特定权限请求 |
| `PostToolUse` | 工具执行后 | 记录日志、把工具结果转换为反馈 |
| `PreCompact` | 上下文压缩前 | 补充需要保留的摘要信息 |
| `PostCompact` | 上下文压缩后 | 记录 compact 结果 |
| `SubagentStart` | 子 agent 创建时 | 写入子 agent 专用上下文 |
| `SubagentStop` | 子 agent 结束前 | 检查子 agent 输出 |
| `Stop` | Whale 结束本轮回复前 | 做最终检查、要求继续处理 |

只有 `PreToolUse` 和 `PostToolUse` 使用 `match` 按工具名匹配。`match` 是正则表达式；省略或设为 `*` 表示匹配全部工具。

## 输入

Whale 会把事件 payload 作为一行 JSON 写入 stdin。脚本可以从 stdin 读取需要的信息：

```sh
#!/usr/bin/env sh
payload="$(cat)"
printf '%s\n' "$payload" >> .whale/hook-input.log
```

常用字段包括：

| 字段 | 含义 |
|---|---|
| `event` | 当前 hook 事件名 |
| `cwd` | 当前工作目录 |
| `session_id` | 会话 ID |
| `tool_name` | 工具名，例如 `shell_run` |
| `tool_args` | 工具参数 |
| `tool_result` | 工具结果，仅部分后置事件有 |
| `prompt` | 用户提交的 prompt |
| `last_assistant_text` | Whale 本轮最后生成的文本 |

字段会随事件变化。第一次接入时，建议先把 stdin 写到临时日志里，看清楚实际 payload，再写策略。

## 输出

如果脚本退出码为 `0` 且 stdout 是 JSON，Whale 会读取这些字段：

```json
{
  "decision": "pass",
  "reason": "ok",
  "updated_input": {"command": "pwd"},
  "additional_context": "Remember that this repo uses make test."
}
```

| 字段 | 用途 |
|---|---|
| `decision` | `pass`、`warn`、`block`、`halt`、`error` |
| `reason` / `message` | 展示给用户或传回模型的说明 |
| `updated_input` | 改写工具参数或用户 prompt |
| `additional_context` | 给模型补充上下文 |
| `metadata` | 自定义结构化信息 |

对于 `PreToolUse`、`PermissionRequest` 和 `UserPromptSubmit`，`decision = "block"` 会阻止后续动作。其他事件返回 `block` 会降级为 warning，不会直接挡住主流程。

## 退出码

| 退出码 | 行为 |
|---|---|
| `0` | 成功；如果 stdout 是 JSON，Whale 会解析输出 |
| `2` | 对 blocking 事件表示阻止；stderr 会作为原因 |
| 其他非零 | 记录为 warning 或 error，取决于事件 |

即使 stdout 里有 `"decision":"pass"`，只要进程超时或启动失败，Whale 不会让这个输出覆盖真实失败。

## 信任与启停

项目配置里的 hooks 会执行 shell 命令，所以 Whale 会把它们当作需要 review 的内容。未信任或已修改的项目 hooks 不会运行。

在 TUI 中运行：

```text
/hooks
```

你可以查看所有 hooks、哪些正在生效、哪些需要 review。常用命令：

```text
/hooks trust all
/hooks trust <key>
/hooks disable <key>
/hooks enable <key>
```

建议团队把共享 hooks 放在 `.whale/config.toml`，个人实验放在 `.whale/config.local.toml`。

## 接入建议

先从只记录日志的 hook 开始，确认事件触发点和 payload。再加入 `decision:block` 这类会改变主流程的逻辑。

不要在 hook 中写无限等待、长时间网络请求或交互式命令。需要外部服务时，给 hook 设置明确的 `timeout`，并让脚本失败时输出清楚的 stderr。

不要把 API key、token 或个人路径提交到 `.whale/config.toml`。需要秘密值时用环境变量，或放到不提交的 `.whale/config.local.toml`。
