# draw-ui — AI UI Design Skill

A universal AI skill that generates UI design mockups and helps reconstruct generated UI screenshots into HTML/CSS. Prefer the runtime's built-in image generation when available; use the configured draw images API for scripted local outputs.

---

## What it does

- Generates high-quality UI mockups from natural language descriptions
- Guides navigation/sidebar consistency using reference-image strategy when the active image-generation runtime supports references
- Uses proven prompt techniques (analogy-style or inventory-style) for better design quality
- Provides cross-platform scripted image generation through macOS/Linux shell and Windows PowerShell entrypoints
- Guides HTML reconstruction with asset strategy, browser screenshot comparison, and background-removal rules for logos and illustrations

## Requirements

- An AI agent that supports the skills protocol (Claude Code, Cursor, etc.)
- For scripted image generation: set `DRAW_API_KEY` as an environment variable.
- Optional scripted generation overrides: `DRAW_BASE_URL` and `DRAW_MODEL`.
- Python 3 for scripted image generation. `compare_mockup.py` and `prepare_image_asset.py` still require `pillow` for image comparison and asset cleanup.

## Installation

```bash
npx skills add oil-oil/draw-ui
```

Or clone manually:

```bash
mkdir -p ~/.claude/skills
git clone https://github.com/oil-oil/draw-ui ~/.claude/skills/draw-ui
```

## Usage

Trigger by saying anything like:

> 帮我设计一个 Dashboard 页面  
> Design a user profile screen  
> 出图，产品详情页

The agent will ask you a few questions first (what the page does, whether you have a reference screenshot, consistency requirements), then generate.

For pure image-generation requests, the agent should run the generation step in a subagent so polling and retry noise stays out of the main conversation. Before starting the subagent, the main agent must know the wait-budget rule it will use when waiting for the result. For the fallback scripts, `DRAW_TIMEOUT` is a per-request HTTP timeout and defaults to 600 seconds; the outer wait budget must also cover retries from `DRAW_RETRY_COUNT` and `DRAW_RETRY_DELAY`. The subagent returns only the output path, metadata path, timeout status, and any error.

### Manual usage

macOS / Linux:

```bash
# No reference image
scripts/ask_draw.sh --type wide --name "dashboard" --prompt "..."

# Reference image; --frame is converted to --ref and uploaded
scripts/ask_draw.sh \
  --frame /path/to/sidebar-reference.png \
  --type wide \
  --name "dashboard" \
  --prompt "..."
```

Windows PowerShell:

```powershell
# No reference image
.\scripts\ask_draw.ps1 --type wide --name "dashboard" --prompt "..."

# Reference image; --frame is converted to --ref and uploaded
.\scripts\ask_draw.ps1 `
  --frame C:\path\to\sidebar-reference.png `
  --type wide `
  --name "dashboard" `
  --prompt "..."
```

If PowerShell blocks local scripts, run once with an execution-policy bypass:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\ask_draw.ps1 --type wide --name "dashboard" --prompt "..."
```

For a persistent user-level policy, use `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`.

The `.sh` and `.ps1` entrypoints are thin wrappers around the same cross-platform Python implementation.

Scripted generation calls `https://image.speedpony.xyz/v1` by default. Set `DRAW_BASE_URL` or `--base-url` to override the OpenAI-compatible base URL, and set `DRAW_MODEL` or `--model` to override the default `gpt-image-2` model. Without references it calls `/images/generations`; with one or more references it calls `/images/edits`.

The scripted backend supports reference-image input. `--frame` is converted to `--ref`, and `--ref` may be repeated to upload multiple local reference images as `image[]`.

Proxy precedence is explicit environment first, macOS system proxy second. Scripted generation inherits any existing `http_proxy`, `https_proxy`, `all_proxy`, `no_proxy`, and related variables from the caller. If none are set, macOS runs read the current system HTTP/HTTPS proxy settings and apply them to remote image generation requests; if system proxy is disabled or unreadable, requests use the default direct behavior. System SOCKS proxy is not auto-applied to `urllib` requests; set proxy environment variables explicitly if SOCKS is required.

### Aspect ratio options

| `--type` | Ratio | Use case |
|----------|-------|----------|
| `wide` | 16:9 | Desktop app screens (default) |
| `classic` | 4:3 | Dashboard, data-heavy layouts |
| `square` | 1:1 | Cards, modals |
| `portrait` | 3:4 | Mobile screens |

For scripted generation, `--type` is currently used only for output naming and metadata. It is not sent to the image API as a size or aspect-ratio field.

## Key concepts

**Reference image strategy**

When the active image-generation runtime supports references, the reference image constrains what AI will copy. If your screenshot has existing content in the main area, AI will mimic that layout — limiting creative freedom.

Best practice: use a "clean frame" — a screenshot with only the sidebar/nav visible and the content area blank. This lets AI keep your chrome consistent while designing the content area freely.

The scripted fallback uploads `--frame` and `--ref` images as references. The built-in image generation tool is still preferred when it is available in the current runtime.

**Prompt writing**

Don't write layout specs (pixels, columns, padding). Instead, describe the *business* using one of two approaches:

- **Analogy** — "Like reading the sheet music behind a hit song. Think Notion's calm meets a music producer's notes." → best for creative quality
- **Inventory** — "The page shows: user name, 30-day trend chart, active campaigns list with status badges." → most reliable for accuracy

Always use real example data instead of placeholders. `"2.3M views"` produces a far more realistic output than `"show view count"`.

**HTML reconstruction**

When turning a generated mockup or screenshot into HTML/CSS, split the work into code and assets:

- Build layout, cards, buttons, text, filters, and ordinary line icons with HTML/CSS/SVG.
- Generate standalone image assets for brand logos, empty-state illustrations, glassy/3D visuals, complex gradients, and other hard-to-code visual details. Use crops only as references for image-to-image redraw, not as final assets unless the source is already high-resolution and background-clean.
- Do not mix large illustrations, logos, and small icons in the same sprite sheet. Generate large illustration assets separately.
- For vendor logo rows, dark wordmarks, and small dark icons, generate a large pure-white source image and remove the white background conservatively. This avoids green fringing and protects thin strokes.
- For colorful illustrations and product visuals, use green-screen or real transparent output when available; white-background keying can damage white cards and highlights.
- If an icon sprite sheet is needed, make it machine-cuttable: pure white background, exact 4x4 grid, no borders, no labels, no shadows, no overlap, and each icon centered with wide padding.

This keeps the HTML clean while preserving the visual parts that image generation is best at.

## License

MIT
