# 设计稿还原为 HTML 的素材策略

当用户想把生成图、截图或设计稿还原成 HTML/CSS 时，先判断元素应该**代码化**还是**素材化**。目标是把最难稳定复刻的视觉部分变成高质量图片素材，同时让其他部分用代码保持清晰和可维护。

## 元素分类

| 元素 | 推荐方式 | 原因 |
|------|---------|------|
| 页面布局、卡片、表格、按钮、输入框、筛选器、文字 | HTML/CSS | 几何结构清晰，代码更稳定 |
| 常规线性图标（日历、筛选、刷新、设置、导航图标） | SVG / icon library / CSS | 线宽、颜色、尺寸可控，图生图容易变形 |
| Logo、品牌符号、复杂空状态插画、3D/玻璃质感、半透明渐变、手绘或拟物元素 | 用裁图做参考，再图生图重绘成干净素材 | 视觉细节强，纯代码还原成本高；直接裁图容易糊、带背景色 |
| 微型产品图、关系网络图、卡片底部复杂可视化、带多个小 logo 的集成图 | 用局部裁图做参考，再图生图重绘成高分辨率素材 | 代码能画出结构，但很难还原细腻曲线、柔光、密度和品牌图标；直接裁图会继承低清晰度和背景噪声 |
| 背景纹理、柔光、复杂阴影、装饰插画 | 单独生成图片素材，必要时透明化 | 模型生成的质感比代码更自然 |

## 关键判断

如果一个元素靠简单 SVG 就能精确画出来，优先代码化；如果它有品牌感、手工感、半透明、复杂渐变或微妙光影，就单独素材化。

Logo 虽然形状可能简单，但它是品牌识别的一部分，通常要单独生成或从原图裁切，避免代码临摹失真。

不要只问“能不能用 CSS 画出来”，还要问“画出来是否会显得硬”。如果一个区域包含多条柔和曲线、多个第三方小 logo、复杂信息密度、半透明光效或设计稿里的微妙质感，即使它可以用 HTML/CSS 勉强实现，也应该精细化生成或裁切成素材。

裁图不能直接等同于最终素材。裁图的主要作用是提供参考、构图和元素关系。最终交付到 HTML 的复杂资产，默认必须通过图生图重绘得到更清晰、更干净、背景可控的版本，再经过裁边、抠图、去绿边或白底转透明处理。

如果直接裁图放进 HTML，通常会出现三类问题：原图局部像素不足导致发糊；裁图自带背景色，和页面真实背景不一致；边缘、阴影、半透明区域会残留噪声。只有在用户明确接受这些问题，或者该裁图本身来自足够高清的源图时，才可以把裁图作为最终素材。

## 还原流程

1. 先用 HTML/CSS 搭出页面骨架：布局、间距、字号、卡片、按钮、状态和响应式。
2. 单独校准排版：字体家族、字号、字重、行高、字距、文本块宽度、换行位置、颜色和抗锯齿观感。
3. 标记代码难还原或代码实现会显得生硬的元素：logo、主插画、复杂装饰、特殊质感、微型产品图、关系网络图、卡片内复杂可视化。
4. 从原图裁出局部参考图，只用于指导图生图和定位尺寸，不直接当最终素材，除非用户明确接受低清晰度和背景残留。
5. 用局部参考图做图生图重绘，生成更大、更清晰、白底或绿幕背景的独立素材。
6. 把重绘素材裁切、去白边、白底转透明或绿幕抠透明，检查边缘是否有色边，再放回 HTML。
7. 用浏览器截图和原图对比，逐项调尺寸、位置、颜色、阴影、纹理和文本换行。

## 排版与纹理校准

文字和纹理必须作为一等还原目标，不能只看布局和素材。每次把设计稿还原成 HTML 时，都要先做一张“排版差异清单”，再修改 CSS。

必须检查这些项目：

- 字体家族：标题、正文、导航、按钮是否分别接近原图。不要默认使用系统字体；如果原图明显是衬线标题、圆润无衬线正文或品牌字体，要在 CSS 里分层指定 fallback。
- 字重：标题黑度、正文粗细、按钮粗细、品牌名粗细要逐项对照。AI 生成图里的标题常常比浏览器默认 `700` 更厚，正文又可能比默认 `400` 更轻。
- 字号和行高：不要只看单行高度，还要看整个文本块高度。标题行高、正文行高、按钮文字垂直居中都要单独调。
- 换行位置：标题和正文必须用文本容器宽度、`max-width`、必要的 `<br>` 或更精确的词组拆分来贴近原图。换行错了，即使字体对了，视觉重量也会明显偏。
- 字距：导航、eyebrow、logo 行标题这类小字要检查 `letter-spacing`。正文和大标题通常保持 `letter-spacing: 0`，除非原图明显有拉开。
- 颜色和抗锯齿观感：深色文字不能只用纯黑；要匹配原图里的蓝黑、灰蓝和透明度。必要时通过 `text-shadow` 或更合适的字体重量接近截图质感。
- 纹理与微妙背景：纸感、柔光、渐变雾面、卡片内浅色底纹都要检查。简单线性渐变无法复刻时，把纹理作为单独背景素材生成，或用多层 radial-gradient 近似。

排版校准顺序：

1. 先锁定页面宽度和主要容器宽度，因为文本换行首先由容器决定。
2. 再调字体家族、字号、字重和行高。
3. 再调文本块 `max-width`、显式换行和上下 margin。
4. 最后调颜色、字距、阴影和纹理。

排版不得只依赖像素差异热力图。像素 diff 能发现“哪里不同”，但字体和换行需要人工看 side-by-side：标题是否同样分行、每一行长度是否接近、文字黑度是否一致、正文灰度是否一致、按钮文字是否垂直居中。

## 素材生成规则

**大插画和 logo 分开生成。** 不要把大插画、logo、图标放在同一张素材 sheet 里。大元素会破坏网格尺度，导致自动裁切不稳定。厂商 logo、文字型 logo、小号深色图标要单独生成大尺寸白底素材，不要和 hero 图、产品图、复杂插图放在同一张板里。

**局部裁图要作为图生图参考。** 对 logo、复杂卡片插图、hero 装饰图这类素材，先裁局部图，再用支持图生图/参考图的内置生图工具或脚本后端生成干净高分辨率素材。`ask_draw.sh` / `ask_draw.ps1` 的 `--ref` / `--frame` 会上传本地参考图。直接把裁图放进 HTML 容易出现糊、背景色不一致、边缘噪声和文字碎片。

**复杂视觉资产必须走“裁图参考 -> 图生图重绘 -> 抠图清理 -> HTML 放置”的链路。** 这类资产包括 hero 主图、空状态插画、品牌 logo、卡片底部关系图、集成网络图、玻璃拟物模块和带多个微小图标的产品可视化。不要用直接裁图来冒充最终资产；裁图只解决“看起来像什么”和“放在哪里”，重绘和抠图才解决清晰度与背景融合。

**细文字和厂商 logo 优先白底，不用绿幕。** 对客户 logo row、深色文字、深色线性图标这类素材，绿幕会在抗锯齿边缘留下绿色污染，强抠图还会把细笔画吃掉。更稳的方式是生成纯白底大图，再用保守的白底转透明参数处理。这个方法适合放在白色或浅色页面背景上；如果素材要放在深色背景，优先使用真实透明输出、SVG，或重新生成深色背景专用版本。

**图标 sheet 只适合统一小图标。** 如果确实需要图标 sheet，必须让模型生成机器可切的规则网格：

```text
Machine-cuttable icon sprite sheet.
Pure white background.
Exactly 16 icons in a perfect 4 columns x 4 rows grid.
No borders, no grid lines, no labels, no text, no shadows, no decorative elements, no overlap.
Each icon is centered in its own invisible cell, same visual size, occupying only the central 45 percent of the cell, with wide white padding.
Use thin blue-gray strokes and subtle blue accents.
```

但在实际 HTML 还原里，普通图标通常还是用 SVG/icon library 更好。图标 sheet 只在用户明确想保留 AI 风格图标时使用。

## Logo / 插画素材提示词模板

Logo 单独素材：

```text
Based on the reference image, recreate only the app logo mark as a standalone asset.
Pure white background. Centered. Large size. No text, no border, no mockup, no shadow box, no extra symbols.
Preserve the logo's silhouette, proportions, blue gradient, soft depth, and brand feel.
Leave generous white padding around the logo for cropping.
```

空状态插画单独素材：

```text
Based on the reference image, recreate only the central empty-state illustration as a standalone asset.
Pure white background. Centered. Large size. No surrounding dashboard UI, no text, no buttons, no labels, no border, no grid.
Preserve the soft blue gradient, translucent panels, database cylinder, chart card, orbit line, subtle highlights, and gentle SaaS product style.
Leave generous white padding around the illustration for cropping.
```

如果需要透明 PNG，先生成纯白底或高对比纯色背景素材，再用本地工具抠图；不要让模型在同一张图里同时承担排版和透明裁切任务。

厂商 logo row 白底素材：

```text
Scene:
Pure white #ffffff canvas.

Subject:
One horizontal row of vendor logos: Amplitude, Brex, loom, Notion, Webflow, ramp.

Important details:
Large crisp vector-style dark navy marks and wordmarks, color #273142, clean kerning, consistent baseline, generous spacing, readable at web size.

Use case:
Source asset for HTML reconstruction; the white background will be removed locally.

Constraints:
Pure white background, no shadow, no glow, no texture, no gradient, no border, no labels, no grid, no watermark. Render only the logo row. Text must be legible and spelled exactly.
```

## 浏览器后验验证

把设计稿还原成 HTML 后，必须增加一轮浏览器验证：

1. 用独立浏览器会话打开本地 HTML，设置和原始设计稿接近的 viewport。不要临时修改用户当前窗口，也不要为了截图改页面 CSS。
2. 如果原始设计稿是固定视口图，就截取 viewport screenshot 做像素对比；同时另存一张 full-page screenshot 检查页面完整性。不要把超长整页截图压缩到设计稿高度后再判断组件位置。
3. 用 `scripts/compare_mockup.py` 对比原始设计稿和浏览器截图，输出 candidate、diff、heatmap 和 metrics。
4. 同时做分区对比：hero、导航、客户 logo 行、功能卡、指标区等关键区域都要单独 `--clip`。整页 heatmap 只能发现大问题，不能替代组件级检查。
5. 先看 heatmap 中的大块差异：首屏图片位置、标题换行、卡片间距、背景色、纹理、素材尺寸。
6. 再看组件级差异：字体家族、字重、字号、行高、卡片内部 icon 位置、标题换行、文案行宽、CTA 位置、底部插图位置、表格密度、按钮尺寸。
7. 按差异修改 HTML/CSS 或重新处理素材，然后再次截图对比。

推荐使用一体化验证脚本：

```bash
scripts/verify_html_mockup.sh \
  --html /path/to/page.html \
  --reference /path/to/original-mockup.png \
  --out-dir /path/to/verify-output \
  --viewport 1024x1536
```

Windows PowerShell:

```powershell
.\scripts\verify_html_mockup.ps1 `
  --html C:\path\to\page.html `
  --reference C:\path\to\original-mockup.png `
  --out-dir C:\path\to\verify-output `
  --viewport 1024x1536
```

它会通过 `agent-browser` 启动独立浏览器会话、设置 viewport、打开页面、截取 viewport 图用于对比，并额外保存 full-page 图方便检查。只有当 reference 本身就是整页长图时，才增加 `--full-page`。

`verify_html_mockup.sh` / `verify_html_mockup.ps1` 会调用 `compare_mockup.py`，因此验证路径仍需要 `pillow`（Python import 名为 `PIL`）。如果当前 Python 环境缺少该库，先运行 `python3 -m pip install pillow`。

单独运行对比脚本：

```bash
scripts/compare_mockup.py \
  --reference /path/to/original-mockup.png \
  --candidate /path/to/browser-screenshot.png \
  --out-dir /path/to/compare-output \
  --prefix landing \
  --clip hero:0,0,1024,760 \
  --clip feature-card-1:40,910,305,365
```

这个脚本会把 candidate resize 到 reference 尺寸再做像素差异。它不替代人工设计判断，但能快速暴露布局比例、颜色、素材位置和大面积空白的偏差。对于用户指出的具体区域，必须补一个 `--clip` 对比，生成单独 heatmap 后再判断。

组件级观察清单：

- 卡片：外框位置、圆角半径、padding、icon 容器位置、标题换行、说明文案行宽、CTA 位置、底部图形垂直位置。
- Hero：标题字体、字重、字号、行高、换行、按钮位置、插画大小、插画和文字的重叠关系、首屏底部露出的下一节内容。
- 导航：logo 尺寸、导航字体、导航项间距、按钮大小、左右边距。
- 数据图表：线条密度、表格行高、标签位置、空白比例。
- 纹理：页面背景柔光、卡片底色、渐变方向、雾面颗粒感、阴影扩散范围。

浏览器截图原则：

- 优先使用独立 `agent-browser` 会话做后验验证，避免影响用户当前打开的页面和窗口。
- 如果必须检查用户正在看的真实页面，可以再使用 `browser-use:browser` 获取当前 in-app browser 截图作为补充证据。
- 截图前确认资源加载完成，避免拿到图片未加载时的空白截图。

## 透明素材后处理

当前脚本化生图 API 不支持透明背景参数。需要透明素材时，优先使用纯白底或绿幕，再运行 skill 内置脚本：

```bash
scripts/prepare_image_asset.py input.png output.png \
  --key-color '#00ff00' \
  --key-threshold 62 \
  --feather 54 \
  --despill \
  --edge-contract 1 \
  --padding 10
```

常用策略：

- 厂商 logo、深色 wordmark、深色细线图标：优先生成纯白底大图，再用 `--alpha --threshold 248 --feather 10` 转透明，不要使用 `--edge-contract`。
- 复杂彩色插画、独立装饰图：优先让模型生成纯绿幕背景，再用 `--key-color '#00ff00'` 抠透明。
- 产品界面截图、白色卡片很多的素材：不要用白底抠图，容易误伤主体；使用绿幕。
- 复杂 hero 图如果出现绿边，增加 `--despill`，适当提高 `--key-threshold` 和 `--feather`，再轻微使用 `--edge-contract 1`。
- 如果细线、半透明阴影或柔光仍然有绿边，把素材拆成多张生成：产品面板一张、插画花束一张、装饰粒子一张，最后用 HTML/CSS 叠放。

白底厂商 logo 处理命令：

```bash
scripts/prepare_image_asset.py vendor-logo-white.png vendor-logo-alpha.png \
  --alpha \
  --threshold 248 \
  --feather 10 \
  --padding 16
```

白底抠图只适合浅色页面背景。如果把这类素材放到深色底上，会看到白色抗锯齿边，这是白底混色的正常结果。深色底场景要生成深色底专用图、真实透明图，或改用矢量 SVG。

绿幕素材提示词可以这样写：

```text
Put the asset on a perfectly flat solid #00ff00 chroma-key background for background removal.
The background must be one uniform #00ff00 color with no shadows, gradients, texture, reflections, floor plane, or lighting variation.
Keep the subject fully separated from the green background with generous padding.
Do not use #00ff00 anywhere in the subject.
No semi-transparent glow, no soft shadow touching the background, no hairline strokes on the background, crisp opaque edges.
```
