# Whale

<p align="center">
  <img src="docs/logo.svg" alt="Whale — 面向 DeepSeek 的终端 AI 编程 Agent" width="640">
</p>

<p align="center">
  <strong>简体中文</strong> · <a href="./README.md">English</a>
</p>

<p align="center">
  <a href="https://github.com/usewhale/DeepSeek-Code-Whale/releases"><img src="https://img.shields.io/github/v/release/usewhale/DeepSeek-Code-Whale?label=release" alt="release"></a>
  <a href="https://github.com/usewhale/DeepSeek-Code-Whale/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/usewhale/DeepSeek-Code-Whale/ci.yml?label=CI" alt="CI"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/usewhale/DeepSeek-Code-Whale" alt="license"></a>
  <a href="https://github.com/usewhale/DeepSeek-Code-Whale/stargazers"><img src="https://img.shields.io/github/stars/usewhale/DeepSeek-Code-Whale?style=flat&logo=github&label=stars" alt="GitHub stars"></a>
</p>

<p align="center">
  <b>专为 DeepSeek 打造的终端 AI 编程 Agent。</b><br>
  持久会话、超长上下文、工具调用、可编程工作流——<br>
  全部在终端里完成，无需 IDE。
</p>

---

## ✨ 一览

| 特性 | 为什么重要 |
|---|---|
| 🐋 **DeepSeek 原生** | 针对 DeepSeek 的 1M 长上下文、工具调用和成本优势深度优化，不做通用多模型外壳 |
| 💬 **持久会话** | 隔天回来上下文还在，支持搜索、分支、恢复 |
| 🎛️ **TUI + CLI + Headless** | 交互式 TUI、一次性 CLI 命令、后台自动化——按需切换 |
| ⚙️ **工具 & MCP** | 读/写文件、执行命令、搜索网页，接入 1,000+ MCP 服务器 |
| 🧩 **Skills + 插件** | 安装社区技能（代码审查、git 工作流等）或自己编写 |
| 🔁 **动态 Workflow** | 用 JavaScript 编排多个子 agent——扇出研究、多视角审查、流水线处理。兼容 Claude Code。 |
| 💰 **成本优势** | DeepSeek 极低定价 + prompt caching，让 AI 辅助编码不再心疼账单 |

---

## 🚀 快速开始

macOS：

```bash
brew install usewhale/tap/whale
```

Linux：

```bash
curl -fsSL https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.sh | sh
```

Windows PowerShell：

```powershell
irm https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.ps1 | iex
```

Windows CMD：

```cmd
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.ps1 | iex"
```

```bash
# 配置 DeepSeek API Key
whale setup

# 启动交互式 TUI
whale
```

搞定。输入你的问题，Whale 就开始工作——读取文件、执行命令、编辑代码、搜索网页。

> 需要其他模型、代理或自定义配置？见 [配置文档](docs/configuration.md)。

---

## 🔁 动态 Workflow

Whale 的 **Dynamic Workflow** 让你用 JavaScript 脚本编排多 agent：

```js
// .whale/workflows/research.js
const results = await parallel([
  () => agent("搜索 Go 错误处理的最佳实践"),
  () => agent("找出 Go 错误处理的常见坑"),
]);
return agent("综合两边的发现，写一份简洁指南");
```

**扇出式研究 · 多视角审查 · 流水线处理 · 对抗性验证**

> ✅ **兼容 Claude Code** — 为 Claude Code 编写的 workflow 脚本无需修改即可在 Whale 中运行。

了解更多：[Workflow 概览](docs/workflows.md) · [自定义 Workflow 指南](docs/custom-workflows.md)

---

## 🧰 MCP、Skills 与插件

| 扩展 | 作用 | 开始使用 |
|---|---|---|
| **MCP 服务器** | 接入 1,000+ 工具（数据库、API、浏览器自动化） | [docs/mcp.md](docs/mcp.md) |
| **Skills 技能** | 加载领域知识——代码审查、git-worktree 等 | [docs/skills.md](docs/skills.md) |
| **插件** | 扩展 Whale 运行时 | [docs/plugins.md](docs/plugins.md) |
| **Hooks** | 在生命周期事件上运行脚本 | [docs/hooks.md](docs/hooks.md) |

---

## 📸 三种运行模式

| 模式 | 适用场景 |
|---|---|
| **`whale`**（TUI） | 交互式编程——对话、审查、迭代，全程上下文不断 |
| **`whale ask "..."`**（CLI） | 一次性提问、快速代码审查、单条命令 |
| **`whale --headless`** | CI/CD、自动化 PR 审查、定时任务 |

---

## 🎯 非目标

- **通用多模型外壳。** Whale 是 DeepSeek-first，优先把 DeepSeek 的缓存、工具和定价优势做好。
- **IDE。** Whale 是 terminal-first，和你的 shell、git、测试命令一起工作。

## 📦 项目状态

Whale 仍在快速迭代中。建议先用于个人项目、实验仓库或可回滚的开发流程。

> **免责声明：** 本项目与 DeepSeek Inc. 无关，系独立开源社区项目。

---

## 🤝 参与贡献

关于克隆、开发、测试、issues 和 pull requests，请查看 [CONTRIBUTING.md](CONTRIBUTING.md)。

当前开发方向和可认领任务见 [ROADMAP.md](ROADMAP.md)。

安全相关问题请查看 [SECURITY.md](SECURITY.md)。

---

## Star History（Star 历史）

<a href="https://www.star-history.com/?repos=usewhale%2FDeepSeek-Code-Whale&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=usewhale/DeepSeek-Code-Whale&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=usewhale/DeepSeek-Code-Whale&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=usewhale/DeepSeek-Code-Whale&type=date&legend=top-left" />
 </picture>
</a>
