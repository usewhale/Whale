# 参与 Whale 开发

Whale 仍处于实验阶段。小而聚焦的变更比夹杂行为变更的大范围重构更容易审查。

## 开始之前

- 阅读 [README.md](README.md) 了解公开命令界面。
- 阅读 [AGENTS.md](../AGENTS.md) 了解仓库布局和本地开发规范。
- 如果要新增顶层命令、修改会话文件格式、或扩展 DeepSeek 之外的 provider 支持，请先开一个 issue 讨论方向。

## 获取代码

```bash
git clone https://github.com/usewhale/Whale.git
cd Whale
make build
make test
```

## 开发环境

```bash
make build
make test
```

Whale 通过 Makefile 使用仓库本地的 `.gocache`，因此上述命令是推荐的默认方式。在 Windows 或没有 `make` 的系统上，使用等价的跨平台命令：

```bash
go run ./cmd/dev build
go run ./cmd/dev test
```

常用的聚焦命令：

```bash
make test-tui
make test-evals
make run
```

- `make test` — 运行所有离线 Go 测试。
- `make test-tui` — 运行 TUI 相关的测试子集。
- `make test-evals` — 运行 eval 相关的测试子集。
- `go run ./cmd/dev test-windows` — 在 Windows 上运行支持的 CI 测试子集。

实时的 smoke 脚本位于 `scripts/smoke/`，但它们需要真实的 DeepSeek API Key 和付费 API 访问：

```bash
DEEPSEEK_API_KEY=... ./scripts/smoke/real_stream.sh
DEEPSEEK_API_KEY=... ./scripts/smoke/real_cache.sh
```

## 提交 Issue

- 使用 Bug 报告模板反馈行为回归、崩溃、安装失败或文档错误。
- 使用功能请求模板提出新的工作流需求或命令界面变更。
- 请附上 Whale 版本、操作系统、复现步骤，以及是否涉及本地 hooks 或自定义配置。
- 如果变更涉及顶层命令、会话格式或 provider 范围，请先开 issue 再写代码。

## 贡献指南

- 保持变更范围聚焦、以行为驱动。
- 在可行的情况下随代码变更添加或更新测试。
- 优先使用离线测试、临时目录和 mock provider，而非真实的网络调用。
- 不要提交本地状态，例如 `.whale/`、会话文件、使用日志或 API key。
- 在复现其他工作区的问题时，将 `./.whale/settings.json` 视为不可信输入，因为 hooks 可以执行 shell 命令。

## Pull Request

PR 应包含：

- 什么行为发生了变化
- 为什么需要这个变更
- 你运行了哪些测试
- 涉及 CLI/TUI 用户可见变更时，附上终端输出或截图

小的 Bug 修复和文档更新可以直接提交 PR。对于较大的行为变更，尤其是涉及 CLI 界面、会话持久化或 provider 支持的，请先通过 issue 讨论方向。

如果变更是故意破坏性的（breaking change），请在 PR 描述中明确说明。
