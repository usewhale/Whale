---
name: draw-ui
description: >
  Generate UI design mockups and help reconstruct generated UI screenshots into HTML/CSS. Prefer built-in image generation when available; use the draw images API only as fallback or for scripted local outputs.
  TRIGGER when the user says "生成图片", "画图", "设计 UI", "UI 设计", "出图", "create an image", "design a screen",
  "landing page", "设计稿还原", "截图还原 HTML", "把图片复刻成网页", or when another skill needs image generation.
---

# Draw UI Skill

Prefer the built-in image generation tool when it is available in the current agent/runtime. It is usually simpler, keeps reference-image workflows available, and avoids provider drift.

Use `scripts/ask_draw.sh` on macOS/Linux or `scripts/ask_draw.ps1` on Windows only when built-in image generation is unavailable or when you need scripted local output paths. The scripts call the draw images API through a shared Python core. Default model: `gpt-image-2`.

For pure image-generation requests, delegate the generation step to a subagent to keep the main conversation quiet. Before starting the subagent, the main agent must know the wait-budget rule it will use when waiting for the subagent result. For the fallback scripts, derive the budget from runtime configuration: `DRAW_TIMEOUT` is the per-request HTTP timeout and defaults to 600 seconds, `DRAW_RETRY_COUNT` defaults to 3, and `DRAW_RETRY_DELAY` defaults to 2 seconds with linear backoff, so the outer wait budget must cover retries instead of treating 600 seconds as the whole job timeout. The subagent should build the prompt, run the image-generation tool or fallback script, and return only the output path, metadata path, timeout status, and any error. The main agent should avoid streaming polling logs, should not wait indefinitely, and should summarize the final result briefly. Do not delegate HTML/CSS reconstruction wholesale; keep that coordinated in the main agent and use subagents only for isolated image asset generation if needed.

---

## Onboarding：开始前先问用户这些问题

如果用户只是泛泛地说“设计一个页面”，先用下面的问题引导用户，收集到足够信息再动手。用户已经给出明确页面、风格或参考图时，可以直接执行，不要为了流程感反复追问。

### 必问（缺少任何一项都会影响质量）

> **1. 你想设计哪个页面？这个页面的核心功能是什么？**
> 不能只靠页面名称猜功能——业务理解错了，AI 会画出完全错误的东西。

> **2. 你有现有的 App 截图或设计稿可以上传吗？**
> 有截图 → 可以保持导航/侧边栏的视觉一致性。没有也可以生成，但每次结果会有差异。

> **3. 如果有截图，里面有没有你不希望 AI 改动的区域（比如侧边栏、顶部导航）？**
> 这决定了用哪种参考图策略（见下方）。

### 选问（根据情况）

> **你偏向什么风格？** 比如：分析型工具、温暖品牌感、极简、游戏感……
> **需要生成多张保持一致的屏幕吗？**

---

## 根据用户回答，选择参考图策略

**核心原则：参考图里有什么内容，AI 就会倾向于模仿什么——包括你不想让它模仿的部分。**

| 用户情况 | 策略 |
|---------|------|
| 没有截图，纯创意探索 | 不传参考图，完全自由生成 |
| 有截图，但只想锁定导航/侧边栏 ⭐ | 让用户提供或制作**纯净边框图**（把内容区涂成纯色），只传边框 |
| 有截图，需要整体风格精准对齐 | 传完整截图，但告知用户内容区创意会受影响 |

**纯净边框图怎么做**：截一张 App 刚打开时内容区为空白的截图，或用任意工具把内容区覆盖为纯色。

如果需要生成多张一致的屏幕：**必须串行执行**（一张完成后再开始下一张），不能并行。

---

## 设计稿还原为 HTML 的素材策略

当用户想把生成图、截图或设计稿还原成 HTML/CSS 时，先读取 `references/html-reconstruction.md`。核心原则：页面结构优先代码化；logo、品牌符号、复杂插画、3D/玻璃质感、半透明渐变等难复刻视觉元素要素材化。裁图只作为图生图参考和定位依据，最终放进 HTML 的复杂资产要用图生图重绘，再裁边、抠图和清理边缘。

透明素材策略：厂商 logo、深色 wordmark、小号深色图标优先生成大尺寸纯白底素材，再用保守白底转 alpha；复杂彩色插画、hero 装饰、产品图优先绿幕或真实透明输出。不要把小 logo 和大插画塞进同一张素材板。

---

## 构建提示词

### 两种经过验证有效的写法

**类比法（创意效果最好）**
说"这个工具像什么"，让 AI 借用那个参照物的设计语言，而不是描述布局。

```
像乐谱一样解码爆款视频——Think Notion's calm focus meets a music producer's session notes.
```

**清单法（最稳定，适合需要准确落地的页面）**
列出页面上有哪些信息，不说怎么排，让 AI 自己决定布局。

```
页面包含：用户名和头像、近30天数据趋势图、活跃 Campaign 列表（名称/状态/触达数）、快捷操作入口。
```

### 质量规则

- **不写排版规格**（像素、列数、padding）— 描述越具体，AI 越像在执行指令而不是做设计，结果反而更差
- **给真实示例数据**，不用 placeholder — `"2.3M views, 180K saves"` 比 `"显示播放量"` 效果好十倍
- **颜色用 HEX**，不用 HSL — `#f9f5f0` 比 `hsl(28 25% 97%)` 对模型更准确
- 如果传了参考图，在 prompt 开头明确说明哪些区域需要保持不变
- **Prompt 控制在 800 字以内** — 超长会导致 server disconnect

---

## 执行命令

优先级：

1. 有内置生图工具时，优先直接使用内置生图工具。
2. 需要脚本化批量生成、本地固定输出路径，或内置工具不可用时，再使用平台脚本：macOS/Linux 用 `scripts/ask_draw.sh`，Windows PowerShell 用 `scripts/ask_draw.ps1`。
3. 脚本后端支持纯 prompt 生图和参考图图生图；`--frame` 会转成 `--ref`，`--ref` 可重复并会作为参考图上传。

macOS / Linux:

```bash
# 无参考图
scripts/ask_draw.sh --type wide --name "screen-name" --prompt "..."

# 使用参考图；--frame 会转成 --ref 并真实参与图生图
scripts/ask_draw.sh \
  --frame /path/to/reference.png \
  --type wide \
  --name "screen-name" \
  --prompt "..."
```

Windows PowerShell:

```powershell
# 无参考图
.\scripts\ask_draw.ps1 --type wide --name "screen-name" --prompt "..."

# 使用参考图；--frame 会转成 --ref 并真实参与图生图
.\scripts\ask_draw.ps1 `
  --frame C:\path\to\reference.png `
  --type wide `
  --name "screen-name" `
  --prompt "..."
```

如果 PowerShell 阻止脚本执行，可临时使用：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\ask_draw.ps1 --type wide --name "screen-name" --prompt "..."
```

**多张屏幕**：必须串行执行（一张完成再开下一张），不能并行。偶发 disconnect 属正常，重试即可。

`.sh` 和 `.ps1` 都只是包装器，核心逻辑在跨平台 Python 脚本里。

代理优先级：脚本会优先继承调用前已有的 `http_proxy`、`https_proxy`、`all_proxy`、`no_proxy` 等环境变量。若未显式设置这些环境变量，macOS 下会读取当前系统 HTTP/HTTPS 代理配置并用于远程生图请求；系统代理关闭或读取失败时按直连处理。`SOCKS` 系统代理不会被自动套用到 `urllib` 请求中，如需 SOCKS 请显式设置代理环境变量。

---

## Options

| Flag | 说明 | 默认 |
|------|------|------|
| `--type` | `wide`(16:9) / `square`(1:1) / `portrait`(3:4) / `classic`(4:3)，仅用于输出命名和 metadata，不发送给 API | `wide` |
| `--prompt` | 提示词 | 必填 |
| `--frame` | 兼容旧参数，转成 `--ref` 后作为参考图上传 | — |
| `--ref` | 参考图路径，可重复，存在时走图生图编辑接口 | — |
| `--name` | 输出文件名 | auto |
| `-o` | 自定义输出路径 | 当前工作目录的 `images/YYYY-MM-DD/` |
| `--model` | 模型名 | `DRAW_MODEL` 或 `gpt-image-2` |
| `--base-url` | OpenAI 兼容服务根地址 | `DRAW_BASE_URL` 或 `https://image.speedpony.xyz/v1` |

成功输出：`output_path=<path>`

API Key 只从 `DRAW_API_KEY` 环境变量读取。
