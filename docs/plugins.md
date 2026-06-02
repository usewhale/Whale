# Plugins

插件可以给 Whale 添加新功能：斜杠命令、技能、子智能体、规则、MCP 工具、钩子。
安装和启用是两件事——装完默认是**禁用**的，需要手动启用才会生效。

---

## 用户指南

### 快速上手

三步让一个插件跑起来：

```text
# 1. 安装（从本地目录装）
whale plugin install ./my-plugin

# 2. 确认装上了
whale plugin list

# 3. 启用它
whale plugin enable my-plugin
```

装好并启用后，在 TUI 里就能用插件提供的功能了。
如果插件提供了斜杠命令，直接敲 `/` 就能看到。

---

### 命令速查

| 命令 | 作用 |
|------|------|
| `whale plugin install <路径>` | 从本地目录安装插件 |
| `whale plugin list` | 列出已安装的插件（是否启用、版本） |
| `whale plugin enable <id>` | 启用一个插件 |
| `whale plugin disable <id>` | 禁用一个插件（不卸载） |
| `whale plugin uninstall <id>` | 彻底删除一个插件 |
| `whale plugin inspect <id>` | 查看插件详情：贡献了什么、诊断信息 |

---

### TUI 操作

在 TUI 里敲 `/plugins` 打开插件管理面板：

- 列表显示所有已安装的插件，**绿色**=已启用，**灰色**=已禁用
- 按 `Space` 切换启用/禁用
- 按 `Esc` 关闭面板

启用后，插件的斜杠命令会出现在命令列表里，直接敲 `/` 就能看到。

---

### 常见问题

**Q: 装了但没看到效果？**
A: 安装后默认是**禁用的**。运行 `whale plugin enable <id>` 启用它。

**Q: 怎么知道一个插件提供了什么？**
A: `whale plugin inspect <id>` 会列出它贡献的所有命令、技能、agent、规则等。

**Q: 不想用了怎么彻底删掉？**
A: `whale plugin uninstall <id>` 从磁盘上删除。以后想用需要重新安装。

**Q: 插件装在哪里了？**
A: 安装后的缓存文件在 `~/.whale/plugins/cache/local/<id>/<version>/`。

---

## 开发者指南

### 一个插件就是一个目录

最简单的插件就是一个文件夹加一个 `whale-plugin.toml`：

```
my-first-plugin/
├── whale-plugin.toml   ← 必需，插件的身份证
└── skills/
    └── hello/
        └── SKILL.md    ← 可选，加一个技能试试
```

### 第一步：写 whale-plugin.toml

最少只需要 `id`：

```toml
id = "my-first-plugin"
name = "我的第一个插件"
version = "0.1.0"
description = "我的第一个 Whale 插件"
```

然后安装它：

```text
whale plugin install ./my-first-plugin
whale plugin enable my-first-plugin
```

### 第二步：加个技能

在 `skills/` 目录下放一个 SKILL.md：

```markdown
# Hello

当被问到"hello"相关的问题时，用友好的语气打招呼。
```

回到 TUI 重新开始会话，插件里的技能就能用了。

> `skills/` 目录是自动识别的——即使 `whale-plugin.toml` 里没有声明 `[components]`，只要 `skills/` 目录存在就会被加载。

---

### 还能加什么

插件可以贡献六种东西。下面逐个介绍最小示例。

#### 斜杠命令（Prompt 型）

`commands/` 下的 `.md` 文件变成一条斜杠命令。文件路径决定命令名：

```
commands/
├── explain.md             → /my-first-plugin:explain
└── review/
    └── code.md            → /my-first-plugin:review:code
```

`commands/explain.md` 示例：

```markdown
---
description: 用插件视角解释一个话题
argument_hint: "<话题>"
---
请从我的插件视角解释：{{args}}
```

用户在 TUI 里敲 `/my-first-plugin:explain 什么是插件` 就会触发。

#### 斜杠命令（Shell 型）

如果需要在终端里跑命令，在 `commands/` 下放一个 `commands.toml`：

```toml
[[commands]]
name = "status"
description = "看看插件是否在工作"
command = "echo 'plugin is running'"
timeout_ms = 10000
```

用户敲 `/my-first-plugin:status`，Whale 会用 `shell_run` 执行这条命令（正常走权限和审批，不会绕过安全策略）。

#### 子智能体（Agent）

`agents/` 下的 `.md` 文件变成可 spawn 的子智能体角色：

```markdown
---
description: 按插件惯例审查代码
capabilities: workspace.read
---
你是 {{plugin_id}} 的专家，审查代码是否符合这个插件的惯例。
```

如果你不是在写插件，只是想给当前项目或自己添加一个 subagent，
优先看 [自定义 Subagent](agents.md)。

#### 会话规则（Rules）

`rules/` 下的 `.md` 文件内容会在每次会话启动时注入到上下文：

```markdown
这个项目使用了 my-first-plugin 插件。所有和插件相关的修改请参考其文档。
```

#### MCP 服务器

`mcp.json` 添加外部工具，服务器名会自动加上插件前缀：

```json
{
  "mcpServers": {
    "search": {
      "command": "node",
      "args": ["server.js"]
    }
  }
}
```

启用后 Whale 会把它注册为 `my-first-plugin.search`。

插件 MCP 服务器可以访问三个环境变量：

- `WHALE_PLUGIN_ROOT` — 插件安装目录
- `WHALE_PLUGIN_DATA_DIR` — 插件专属数据目录
- `WHALE_PLUGIN_PROJECT_DIR` — 当前项目专属的插件数据目录

#### 钩子（Hooks）

`hooks.toml` 添加自动化钩子，启用后自动生效：

```toml
[[hooks.SessionStart]]
description = "打一个启动标记"
command = "echo 'plugin started' >> plugin.log"
timeout = 5
```

---

### 开发迭代

改了插件内容后，直接覆盖安装：

```text
whale plugin install ./my-first-plugin
```

安装是原子的：即使拷贝过程出错也会自动回滚到旧版本，不会留下半残的插件。
如果改了技能或规则，重新开始 TUI 会话就会生效。
如果改了命令，在 TUI 里按 Ctrl+R 刷新即可。

---

### 命名规则

- **插件 ID**：小写字母 + 数字 + `.` `-` `_`（下划线会自动转成连字符）
- **命令和 agent 名字**：自动加 `<插件ID>:` 前缀
- **文件路径决定名字**：`commands/review/code.md` → `/my-first-plugin:review:code`
- **覆盖默认名**：在 frontmatter 里写 `name: xxx` 可以手动指定

注意事项：
- 插件 ID 不能和内置插件冲突（内置 ID 是保留的）
- `whale-plugin.toml` 里的组件路径必须是相对路径，不能跳出插件目录
- 路径指向不存在的目录不会报错，只会产生一个 warning（`whale plugin inspect` 可以看到）

---

### 完整最小示例

以下是一个可以一字不差照抄的插件目录：

```
my-plugin/
├── whale-plugin.toml
├── commands/
│   ├── explain.md
│   └── commands.toml
├── skills/
│   └── greet/
│       └── SKILL.md
├── agents/
│   └── reviewer.md
└── rules/
    └── convention.md
```

**whale-plugin.toml**

```toml
id = "my-plugin"
name = "My Plugin"
version = "0.1.0"
description = "一个演示插件"
```

**commands/explain.md**

```markdown
---
description: 解释一个概念
argument_hint: "<概念>"
read_only: true
---
用我的插件视角解释 {{args}}
```

**commands/commands.toml**

```toml
[[commands]]
name = "ping"
description = "测试插件是否在线"
command = "echo pong"
```

**skills/greet/SKILL.md**

```markdown
# Greet

当用户说"你好"时，用活泼的语气打招呼，并介绍一下自己。
```

**agents/reviewer.md**

```markdown
---
description: 按插件惯例审查
capabilities: workspace.read
---
你是 my-plugin 的代码审查专家。
```

**rules/convention.md**

```markdown
本会话使用了 my-plugin 插件。
```

安装并启用：

```text
whale plugin install ./my-plugin
whale plugin enable my-plugin
```

然后打开 TUI 就能看到：

- `/my-plugin:explain` 斜杠命令
- `/my-plugin:ping` 斜杠命令
- `my-plugin:reviewer` 子智能体角色
- `greet` 技能
- 启动规则自动注入
