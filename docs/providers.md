# Provider 配置指南

Whale 原生使用 DeepSeek API，但也可以通过第三方渠道接入 DeepSeek 模型。本文档介绍如何通过不同 provider 使用 DeepSeek 模型。

> **兼容性提示**：通过第三方渠道使用 DeepSeek 模型时，工具调用、长上下文等功能取决于渠道对 OpenAI 兼容接口的支持程度。切换前建议先用 `whale exec` 做小规模测试。

---

## DeepSeek 官方（默认）

无需额外配置。`whale setup` 后即可使用。

```bash
whale setup
DEEPSEEK_API_KEY=sk-... whale
```

---

## 阿里云百炼 — DeepSeek 模型

阿里云百炼（DashScope）提供了 DeepSeek 模型的托管服务，可通过 OpenAI 兼容接口调用。


### 配置

```toml
# .whale/config.toml 或 ~/.whale/config.toml
[model]
provider = "openai-compatible"
model = "deepseek-v4-flash"              # 或 deepseek-v4-pro / deepseek-r1
base_url = "https://dashscope.aliyuncs.com/compatible-mode/v1"
```

API Key 通过 `DEEPSEEK_API_KEY` 环境变量传入：

```bash
DEEPSEEK_API_KEY=sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx whale
```

或通过 `whale setup` 保存到 `~/.whale/credentials.json`。

### 可用 DeepSeek 模型

包括：

| 模型 | 说明 |
|---|---|
| `deepseek-v4-flash` | DeepSeek V4 Flash |
| `deepseek-v4-pro` | DeepSeek V4 Pro |
---

## OpenCode Go — DeepSeek 模型

[OpenCode Go](https://opencode.ai/docs/go/) 是 OpenCode 官方的低价订阅计划，提供 DeepSeek 等模型的 API 访问。


### 配置

```toml
# .whale/config.toml 或 ~/.whale/config.toml
[model]
provider = "openai-compatible"
model = "opencode-go/deepseek-v4-pro"    # 或 opencode-go/deepseek-v4-flash
base_url = "https://opencode.ai/zen/go/v1"
```

```bash
DEEPSEEK_API_KEY=ocg-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx whale
```

### 可用 DeepSeek 模型

| 模型 | Whale 配置名称 |
|---|---|
| DeepSeek V4 Pro | `opencode-go/deepseek-v4-pro` |
| DeepSeek V4 Flash | `opencode-go/deepseek-v4-flash` |


### 已知限制

- Go 计划有明确的用量上限，超额后需等待周期刷新
- DeepSeek 以外的模型（如 MiniMax、Anthropic 格式模型）不在本文档覆盖范围内

---

## OpenCode Zen — DeepSeek 模型

[OpenCode Zen](https://opencode.ai/docs/zen/) 是 OpenCode 官方的按量付费 AI 网关，同样提供 DeepSeek 模型访问。


### 配置

```toml
# .whale/config.toml 或 ~/.whale/config.toml
[model]
provider = "openai-compatible"
model = "opencode/deepseek-v4-flash"     # 或 opencode/deepseek-v4-flash-free
base_url = "https://opencode.ai/zen/v1"
```

```bash
DEEPSEEK_API_KEY=ocz-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx whale
```

### 可用 DeepSeek 模型

| 模型 | Whale 配置名称 |
|---|---|
| DeepSeek V4 Flash | `opencode/deepseek-v4-flash` |
| DeepSeek V4 Flash Free | `opencode/deepseek-v4-flash-free` |
