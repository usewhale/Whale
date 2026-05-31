# 配置

## 🚀 快速配置

最快的方式：

```bash
whale setup
```

这会把你的 DeepSeek API Key 保存到 `~/.whale/credentials.json`。

也可以用环境变量（优先级更高）：

```bash
DEEPSEEK_API_KEY=sk-... whale
```

任何时候想确认当前配置，运行 `whale doctor`。

---

## 常见操作

### 使用其他模型或 endpoint

```toml
# .whale/config.toml（项目级）或 ~/.whale/config.toml（全局）
[model]
provider = "openai-compatible"
model = "deepseek-chat"
base_url = "https://api.deepseek.com/v1"
```

Whale 是 DeepSeek 原生的，但可以指向任何兼容 OpenAI 的 endpoint。
其他模型可能不支持全部功能（工具调用、长上下文）。

### 设置代理

```toml
[model]
http_proxy = "http://127.0.0.1:7890"
https_proxy = "http://127.0.0.1:7890"
```

Whale 也支持 `$HTTP_PROXY` 和 `$HTTPS_PROXY` 环境变量。

### 自定义系统提示词

```toml
[settings]
prompt = "你是一个偏爱 Rust 而非 Go 的编程助手。"
```

这个提示词会在每个新会话的开头注入。

### 项目级配置

```toml
# .whale/config.toml — 可以提交到 git，团队共享
[model]
model = "deepseek-chat"
```

```toml
# .whale/config.local.toml — 个人覆盖，不要提交
[model]
model = "deepseek-reasoner"
```

配置文件合并顺序：`默认值 < 全局 < 项目共享 < 项目本地 < CLI 标志/环境变量`

### 禁用特定工具

```toml
[disabled_tools]
tools = ["web_search", "web_fetch"]
```

---

## 参考

### 配置文件路径

| 路径 | 范围 | 是否提交？ |
|---|---|---|
| `~/.whale/config.toml` | 全局 — 所有项目 | 否 |
| `.whale/config.toml` | 项目 — 团队共享 | 是 |
| `.whale/config.local.toml` | 项目 — 个人覆盖 | 否 |

Windows 上默认全局目录是 `%USERPROFILE%\\.whale`。
设置 `WHALE_HOME` 可自定义目录。

### 所有配置项（`config.toml`）

```toml
[model]
provider = "deepseek"                  # 或 "openai-compatible"
model = "deepseek-chat"                # 或 "deepseek-reasoner"
base_url = "https://api.deepseek.com/v1"
http_proxy = ""                        # API 调用代理
https_proxy = ""

[settings]
prompt = ""                            # 自定义系统提示词前缀
max_tokens = 4096                      # 最大响应 token 数

[permissions]
allowed_directories = []               # 限制文件访问目录

[permissions.mcp]
fs = "allow"                           # "allow" | "ask" | "deny" 按 MCP 服务器设置

[disabled_tools]
tools = []                             # 按名称隐藏内置工具

[mcp]
config_path = ""                       # 自定义 MCP 配置路径

[workflows]
max_concurrency = 3                    # 并行 agent 数

[skills]
disabled = []                          # 禁用的技能
enabled = []                           # 强制启用的技能

[plugins]
disabled = []                          # 禁用的插件
enabled = []                           # 强制启用的插件

[hooks]
pre_tool = [""]                        # 每次工具调用前执行的 shell 命令
post_tool = [""]                       # 每次工具调用后执行的 shell 命令

[logging]
level = "info"                         # debug | info | warn | error
```

### 环境变量

| 变量 | 覆盖内容 |
|---|---|
| `DEEPSEEK_API_KEY` | `~/.whale/credentials.json` 中的凭据 |
| `WHALE_HOME` | 全局数据目录（`~/.whale`） |
| `HTTP_PROXY` / `HTTPS_PROXY` | 配置中的代理设置 |
| `WHALE_MCP_CONFIG` | MCP 配置文件路径 |

### Shell 钩子

在每次工具调用前后执行 shell 命令：

```toml
[hooks]
pre_tool = ["echo '即将执行: $TOOL_NAME'"]
post_tool = ["echo '工具执行完毕: $TOOL_NAME'"]
```

钩子可以通过 stdout 返回 JSON，包含 `decision`、`reason`、`updated_input` 等字段来影响 Whale 的行为。

### 工作目录（Worktree）

Whale 支持 git worktree 进行隔离开发：

```bash
whale --worktree
whale exec --worktree
```

退出时，Whale 会自动清理干净的 worktree。有未提交的改动时会询问是否保留。

---

## 本地状态存在哪里？

```
~/.whale/
├── credentials.json    # API key
├── config.toml         # 全局配置
├── mcp.json            # MCP 服务器配置
├── sessions/           # 会话历史
└── usage.jsonl         # 使用量日志
```

不要提交这些文件。

---

## 需要帮助？

```bash
whale doctor     # 检查当前配置
whale --help     # CLI 参考
```
