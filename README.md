# Whale

<p align="center">
  <img src="docs/logo.svg" alt="Whale — DeepSeek-native coding agent for the terminal" width="640">
</p>

<p align="center">
  <a href="./README.zh.md">简体中文</a> · <strong>English</strong>
</p>

<p align="center">
  <a href="https://github.com/usewhale/DeepSeek-Code-Whale/releases"><img src="https://img.shields.io/github/v/release/usewhale/DeepSeek-Code-Whale?label=release" alt="release"></a>
  <a href="https://github.com/usewhale/DeepSeek-Code-Whale/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/usewhale/DeepSeek-Code-Whale/ci.yml?label=CI" alt="CI"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/usewhale/DeepSeek-Code-Whale" alt="license"></a>
  <a href="https://github.com/usewhale/DeepSeek-Code-Whale/stargazers"><img src="https://img.shields.io/github/stars/usewhale/DeepSeek-Code-Whale?style=flat&logo=github&label=stars" alt="GitHub stars"></a>
  <a href="https://discord.gg/7Fw7j7Kf"><img src="https://img.shields.io/badge/chat-Discord-5865f2?logo=discord&logoColor=white" alt="Discord"></a>
</p>

<p align="center">
  <b>A terminal-native coding agent built for DeepSeek.</b><br>
  Persistent sessions, long context, tools, and programmable workflows —<br>
  all in your terminal, no IDE required.
</p>

---

## ✨ At a Glance

| What | Why it matters |
|---|---|
| 🐋 **DeepSeek-native** | Built for DeepSeek's long context (1M tokens), tool calling, and cost efficiency — no generic multi-model wrapper |
| 💬 **Persistent sessions** | Come back days later, context is still there. Search, branch, resume. |
| 🎛️ **TUI + CLI + Headless** | Interactive TUI, one-shot CLI commands, or headless automation — pick your mode |
| ⚙️ **Tools & MCP** | Read/edit files, run commands, search web — and plug in 1,000+ MCP servers |
| 🧩 **Skills + Plugins** | Install community skills (code review, git workflows, etc.) or write your own |
| 🔁 **Dynamic Workflows** | Write JavaScript scripts that orchestrate multiple agents — fan-out research, multi-perspective review, pipelines. Claude Code compatible. |
| 💰 **Cost-efficient** | DeepSeek's aggressive pricing paired with prompt caching makes AI-assisted coding affordable at scale |

---

## 🚀 Quick Start

macOS:

```bash
brew install usewhale/tap/whale
```

Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.ps1 | iex
```

Windows CMD:

```cmd
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/usewhale/DeepSeek-Code-Whale/main/scripts/install.ps1 | iex"
```

```bash
# Set your DeepSeek API key
whale setup

# Launch the interactive TUI
whale
```

That's it. Type your question and Whale starts working — reading files, running commands,
editing code, searching the web.

> Need a different model provider, proxy, or custom config? See [Configuration](docs/configuration.en.md).

---

## 🔁 Dynamic Workflows

Whale's **Dynamic Workflows** let you script multi-agent orchestration in JavaScript:

```js
// .whale/workflows/research.js
const results = await parallel([
  () => agent("Search for best practices in Go error handling"),
  () => agent("Find common Go error handling mistakes"),
]);
return agent("Synthesize both findings into a concise guide");
```

**Fan-out research · Multi-perspective review · Pipeline processing · Adversarial validation**

> ✅ **Claude Code compatible** — workflow scripts written for Claude Code work as-is in Whale.

Learn more: [Workflow Overview](docs/workflows.en.md) · [Custom Workflow Guide](docs/custom-workflows.en.md)

---

## 🧰 MCP, Skills & Plugins

| Extension | What it does | Get started |
|---|---|---|
| **MCP Servers** | Connect to 1,000+ tools (databases, APIs, browser automation) | [docs/mcp.en.md](docs/mcp.en.md) |
| **Skills** | Load domain expertise — code review, git-worktree, and more | [docs/skills.en.md](docs/skills.en.md) |
| **Plugins** | Extend Whale's runtime with custom logic | [docs/plugins.en.md](docs/plugins.en.md) |

---

## 📸 How It Works

Whale runs in three modes:

| Mode | When to use |
|---|---|
| **`whale`** (TUI) | Interactive coding sessions — chat, review, iterate with full context |
| **`whale ask "..."`** (CLI) | One-shot questions, quick code reviews, single commands |
| **`whale --headless`** | CI/CD, automated PR reviews, scheduled tasks |

---

## 🎯 Non-goals

- **Multi-model shell.** Whale is DeepSeek-first — optimized for DeepSeek's caching, tools, and pricing.
- **IDE replacement.** Whale works in your terminal alongside your shell, git, and test commands.

## 📦 Project Status

Whale is in active development. Best suited for personal projects, experimental repositories,
and workflows where changes can be reviewed and rolled back.

> **Disclaimer:** This project is not affiliated with DeepSeek Inc. It is an independent open-source community project.

---

## 🤝 Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for local development, testing, issues, and PRs.

Current direction and available tasks: [ROADMAP.md](ROADMAP.md).

Security issues: [SECURITY.md](SECURITY.md).

---

## Star History

<a href="https://www.star-history.com/?repos=usewhale%2FDeepSeek-Code-Whale&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=usewhale/DeepSeek-Code-Whale&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=usewhale/DeepSeek-Code-Whale&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=usewhale/DeepSeek-Code-Whale&type=date&legend=top-left" />
 </picture>
</a>

## FAQ

### What is this project?

Whale — a terminal AI agent built for DeepSeek. 1M context, persistent sessions

### Key Features

| Feature | Description |
|---------|-------------|
| AI-powered | Built for AI agents with long context and tool calling |
| Open-source | Free and open source, MIT or Apache 2.0 license |
| Easy to install | Quick start with one-line installation command |
| Extensible | Support MCP servers, skills, and plugins |
| Cost-efficient | Optimized for AI token usage and cost control |

### How to install?

Visit the official documentation or GitHub repository for installation instructions.

### Is it free?

Yes, this project is free and open source. Check the LICENSE file for details.

### Where to get help?

- GitHub Issues: Report bugs and request features
- Documentation: Official docs site
- Discord/Community: Join the community chat

### License

MIT License or Apache 2.0 License - see LICENSE file for details.
