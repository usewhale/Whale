# Whale

<p align="center">
  <img src="docs/logo.svg" alt="Whale — 面向 DeepSeek 的终端 AI 编程 Agent" width="640">
</p>

<p align="center">
  <strong>简体中文</strong> · <a href="./README.en.md">English</a>
</p>

<p align="center">
  <a href="https://github.com/usewhale/whale/releases"><img src="https://img.shields.io/github/v/release/usewhale/whale?label=release" alt="release"></a>
  <a href="https://github.com/usewhale/whale/actions/workflows/ci.yml"><img src="https://github.com/usewhale/whale/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/usewhale/whale" alt="license"></a>
  <a href="https://github.com/usewhale/whale/stargazers"><img src="https://img.shields.io/github/stars/usewhale/whale?style=flat&logo=github&label=stars" alt="GitHub stars"></a>
</p>

<p align="center">
  <strong>Whale 是一个非官方的 DeepSeek CLI / DeepSeek 编程 Agent。</strong><br>
  运行在终端里，支持读代码、改代码、运行命令、MCP 和 Skills。
</p>

<p align="center">
  <strong>90% live prefix-cache hit</strong> · <strong>~30x cheaper per task vs Claude Code</strong> · terminal-first · open source
</p>

---

## 快速开始

macOS / Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/usewhale/whale/main/scripts/install.sh | sh
```

使用 Homebrew 安装：

```bash
brew install usewhale/tap/whale
```

首次运行：

```bash
whale setup
whale doctor
whale
```

升级：

```bash
brew upgrade whale
# 或重新运行安装脚本
```

Windows:

1. 从 [GitHub Releases](https://github.com/usewhale/whale/releases) 下载 `whale-windows-amd64.zip`，并解压出 `whale.exe`。
2. 将 `whale.exe` 放到 PATH 中的目录，例如 `$env:USERPROFILE\bin`。
3. 在 PowerShell 中运行：

```powershell
whale --version
whale setup
whale
```

Whale 在 Windows 上使用 PowerShell 执行 shell 命令。推荐安装 PowerShell 7（`pwsh`）；如果找不到 `pwsh`，Whale 会尝试使用系统自带的 `powershell.exe`。如果直接下载 `whale-windows-amd64.exe`，请先重命名为 `whale.exe` 再放入 PATH，或者使用完整文件名运行它。

Windows 上的未签名 exe 可能触发 Windows SmartScreen。请确认文件来自 Whale 的 GitHub Release，并对照 Release 中的 `checksums.txt` 校验下载内容。
Whale 当前使用 DeepSeek API。运行前请先在 [DeepSeek Platform](https://platform.deepseek.com/) 创建 API key。API 细节见 [DeepSeek API docs](https://api-docs.deepseek.com/)。

<p align="center">
  <img src="docs/screenshot-02.png" alt="Whale TUI 截图" width="860">
</p>

也可以直接运行一次性任务：

```bash
whale exec "解释这个仓库是做什么的"
printf '总结当前目录\n' | whale exec
```

---

## 和其他工具有什么区别

|                          | Whale | Claude Code | Codex CLI | Cursor | Aider |
|--------------------------|-------|-------------|-----------|--------|-------|
| 主要形态                 | 终端 TUI/CLI | 终端 Agent | 终端 Agent | IDE | CLI |
| 默认后端                 | DeepSeek | Anthropic | OpenAI | 多模型 | 多模型 |
| DeepSeek 优化            | 是 | 否 | 否 | 否 | 有限 |
| Prefix-cache 友好        | 是 | n/a | n/a | 取决于模型 | 有限 |
| 本地读写代码             | 是 | 是 | 是 | 是 | 是 |
| 运行 shell / test        | 是 | 是 | 是 | 部分 | 是 |
| `/ask` 只读模式          | 是 | 部分 | 部分 | n/a | 部分 |
| `/plan` 规划模式         | 是 | 是 | 是 | n/a | 部分 |
| MCP                      | 是 | 是 | 取决于版本 | 部分 | 部分 |
| Skills / 工作流复用      | 是 | 是 | 是 | 部分 | 有限 |
| 开源                     | 是 | 否 | 是 | 否 | 是 |

Whale 的重点不是“支持最多模型”，而是把 DeepSeek API 变成一个更稳定、便宜、适合长时间打开的本地编程 Agent。

<details>
<summary><strong>为什么 DeepSeek-only？</strong></summary>

DeepSeek 的低价只是第一层优势。真正适合做长会话编程 Agent 的关键，是 prefix cache。

DeepSeek 的 prefix cache 对字节稳定很敏感。Whale 的工具循环围绕这个特点设计：尽量保持追加式 turn、稳定的上下文顺序和可恢复的会话记录，让长任务能持续吃到缓存收益。

这也是 Whale 不急着做通用 provider 抽象的原因。Claude、OpenAI、DeepSeek 的缓存机制、tool-call 形态和 reasoning 行为并不一样。强行套一层通用接口，通常会牺牲 DeepSeek 最有价值的部分。

Whale 针对 DeepSeek 做了这些适配：

| 通用 Agent 常见假设 | DeepSeek 实际会发生 | Whale 的处理 |
|---------------------|---------------------|--------------|
| tool-call JSON 总是稳定 | payload 可能 malformed、被转义或混进 reasoning | repair / scavenge 路径 |
| 深层 tool schema 能稳定保留 | 部分深层嵌套参数可能丢失 | 工具参数尽量扁平化 |
| 失败工具需要统一 replan | 有些失败应该原样反馈给模型 | 更细的失败分类和 recovery 策略 |
| 用户取消就是普通工具失败 | 取消后不应该继续恢复或补计划 | 中断路径单独处理 |
| reasoning 深度只靠 prompt | DeepSeek 暴露 `reasoning_effort` | runtime 里保留 effort 控制 |

Whale 的目标是让 DeepSeek 的价格优势、缓存优势和编码能力真正落到终端工作流里。

</details>

---

## Whale 能做什么

- **理解代码库**：读取文件、搜索代码、总结项目结构和关键模块。
- **修改代码**：生成 patch、编辑文件、补测试、修 bug、重构局部模块。
- **运行命令**：执行 shell 命令、测试、构建、诊断脚本，并把结果带回对话。
- **交互式工作流**：在本地 TUI 里连续对话，支持会话保存和 `whale resume` 恢复。
- **只读提问**：用 `/ask` 进入只读问答模式，适合先理解代码，不让 Agent 修改文件。
- **先规划再执行**：用 `/plan` 先产出计划，再决定是否让 Agent 实施。
- **扩展工具能力**：通过 MCP 接入外部工具，通过 Skills 复用固定工作流。
- **无头执行**：用 `whale exec` 在脚本、CI 或一次性任务里运行单条 prompt。

## 常用命令

| 命令 | 作用 |
|------|------|
| `whale` | 启动交互式 TUI |
| `whale setup` | 保存 DeepSeek API key |
| `whale doctor` | 运行健康检查 |
| `whale exec "prompt"` | 非交互运行单条 prompt |
| `whale resume` | 打开会话选择器 |
| `whale resume --last` | 直接恢复最近会话 |
| `whale resume <id>` | 恢复指定会话 |
| `/ask [prompt]` | 只读提问模式 |
| `/plan [prompt]` | 先规划，再决定是否执行 |
| `/skills` | 查看本地 skills |
| `/mcp` | 查看 MCP server 状态 |

## MCP

Whale 支持从 MCP server 加载工具。MCP 工具会作为普通 Whale 工具注册，并继续走审批流程。

当前支持：

- stdio MCP server
- Streamable HTTP MCP server
- `disabled_tools`
- HTTP headers 和环境变量展开
- filesystem server allowed-directories 校验

配置说明见 [docs/mcp.md](docs/mcp.md)。

## Skills

Whale 支持本地 Agent Skills。一个 skill 是包含 `SKILL.md` 的指令目录，可用于沉淀固定工作流、团队规范或特定工具用法。

默认发现路径：

- `.whale/skills`
- `.agents/skills`
- `~/.whale/skills`
- `~/.agents/skills`

在 TUI 中可以这样触发：

```text
$my-skill 帮我按这个工作流处理当前任务
```

更多说明见 [docs/skills.md](docs/skills.md)。

## 配置

Whale 会把本地状态存放在 `~/.whale/` 下。API key、偏好设置、会话记录和 MCP 配置都在本地管理。

更多说明见 [docs/configuration.md](docs/configuration.md)。

---

## Non-goals

- **不做通用多模型外壳。** Whale 当前就是 DeepSeek-only，优先把 DeepSeek 的缓存、工具调用和成本优势做好。
- **不做 IDE。** Whale 是 terminal-first，和你的 shell、git、测试命令一起工作，不替代 Cursor 这类 IDE。

## 项目状态

Whale 仍在快速迭代中，建议先用于个人项目、实验仓库或可回滚的开发流程。功能和交互可能会继续变化。

> **免责声明：** 本项目与 DeepSeek Inc. 无关，系独立开源社区项目。

## 参与贡献

关于克隆、开发、测试、issues 和 pull requests，请查看 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 安全

如果是安全相关问题，请查看 [SECURITY.md](SECURITY.md)。
