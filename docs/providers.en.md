# Provider Configuration Guide

Whale uses DeepSeek API natively, but you can also access DeepSeek models through third-party channels. This document explains how to use DeepSeek models via different providers.

> **Compatibility note**: When accessing DeepSeek models through third-party channels, feature support (tool calling, long context) depends on the channel's OpenAI-compatible implementation. Test with `whale exec` before switching.

---

## DeepSeek Official (Default)

No extra configuration needed. Just run `whale setup`.

```bash
whale setup
DEEPSEEK_API_KEY=sk-... whale
```

---

## Alibaba Cloud Bailian — DeepSeek Models

Alibaba Cloud Bailian (DashScope) hosts DeepSeek models with an OpenAI-compatible API.

### Configuration

```toml
# .whale/config.toml or ~/.whale/config.toml
[model]
provider = "openai-compatible"
model = "deepseek-v4-flash"              # or deepseek-v4-pro / deepseek-r1
base_url = "https://dashscope.aliyuncs.com/compatible-mode/v1"
```

Pass the API key via the `DEEPSEEK_API_KEY` environment variable:

```bash
DEEPSEEK_API_KEY=sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx whale
```

Or save it to `~/.whale/credentials.json` with `whale setup`.

### Available DeepSeek Models

Includes:

| Model | Notes |
|---|---|
| `deepseek-v4-flash` | DeepSeek V4 Flash |
| `deepseek-v4-pro` | DeepSeek V4 Pro |

---

## OpenCode Go — DeepSeek Models

[OpenCode Go](https://opencode.ai/docs/go/) is a low-cost monthly subscription that provides API access to DeepSeek and other curated models.

### Configuration

```toml
# .whale/config.toml or ~/.whale/config.toml
[model]
provider = "openai-compatible"
model = "opencode-go/deepseek-v4-pro"    # or opencode-go/deepseek-v4-flash
base_url = "https://opencode.ai/zen/go/v1"
```

```bash
DEEPSEEK_API_KEY=ocg-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx whale
```

### Available DeepSeek Models

| Model | Whale Config Name |
|---|---|
| DeepSeek V4 Pro | `opencode-go/deepseek-v4-pro` |
| DeepSeek V4 Flash | `opencode-go/deepseek-v4-flash` |

### Known Limitations

- Go has explicit usage caps; you must wait for the window to reset when exceeded
- Non-DeepSeek models are outside the scope of this document

---

## OpenCode Zen — DeepSeek Models

[OpenCode Zen](https://opencode.ai/docs/zen/) is OpenCode's pay-as-you-go AI gateway that also provides access to DeepSeek models.

### Configuration

```toml
# .whale/config.toml or ~/.whale/config.toml
[model]
provider = "openai-compatible"
model = "opencode/deepseek-v4-flash"     # or opencode/deepseek-v4-flash-free
base_url = "https://opencode.ai/zen/v1"
```

```bash
DEEPSEEK_API_KEY=ocz-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx whale
```

### Available DeepSeek Models

| Model | Whale Config Name |
|---|---|
| DeepSeek V4 Flash | `opencode/deepseek-v4-flash` |
| DeepSeek V4 Flash Free | `opencode/deepseek-v4-flash-free` |
