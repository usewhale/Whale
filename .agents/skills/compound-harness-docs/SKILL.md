---
name: compound-harness-docs
description: 在 Aiba Pro 中，当一个功能分支的大部分或全部 user stories 已完成，需要基于当前分支真实代码状态、git 提交历史、`scripts/ralph/prd.json`，以及 `scripts/ralph/progress.txt` 的 `Codebase Patterns`，更新 harness / codebase 文档时使用。该 skill 只整理项目相关的目录结构、开发约定、跨项目边界与常见陷阱，写入 `AGENTS.md`、`coding_maps/`、`front/.planning/codebase/`、`back/.planning/codebase/`，禁止修改业务代码或把自动化工具细节写入项目文档。触发词：复利工程、更新 harness、沉淀经验、整理 codebase 文档、根据 git 历史更新 AGENTS。
---

# Harness Compounding

用于“功能做完后统一回收经验”的文档复利整理。

这不是功能开发 skill。它只做一件事：把当前分支已经落地的真实结构、稳定约定、系统边界和高频陷阱，沉淀回项目文档，提升下一轮 AI Coding / Copilot Coding 的准确性。

## 何时使用

在这些场景触发：

- 一个功能分支的大部分或全部 story 已完成，准备做文档收口
- 已经积累了一批 commit，目录结构和开发约定发生了真实变化
- 想让未来 agent 更清楚当前项目结构、边界、写法和易错点
- 用户明确提到“复利工程”“更新 harness”“整理 AGENTS / codebase 文档”

不要用于：

- 单个 story 正在实现中
- 纯代码修复、联调、测试或发布
- 只想更新 PRD / 进度日志

## 硬性约束

1. 只允许修改文档文件；禁止修改业务代码、脚本、配置、测试
2. 项目文档中禁止出现 `scripts/ralph`、`prd.json`、`progress.txt`、validator、自动化 agent、browser skill 等工具实现细节
3. 只沉淀“项目相关、可复用、当前分支仍然成立”的知识
4. 若当前代码状态与历史记录冲突，以当前代码状态为准
5. 若证据不足，宁可不写
6. 不要在根目录新增统一 codebase 映射；遵守现有分层

## 事实优先级

按下面顺序判断事实：

1. 当前分支真实代码与目录结构
2. 当前分支相对默认分支的 git diff / git log
3. `progress.txt` 中的 `## Codebase Patterns`
4. `prd.json` 的交付范围说明

`prd.json` 只用于理解“这轮做了什么”，不是最终事实来源。

## 必读文件

不要预设仓库里一定有哪个子项目，也不要写死 `front`、`back` 或固定的 `.planning/codebase/` 路径。

必须按下面顺序动态发现：

1. 先读根级 `AGENTS.md`
2. 从 `AGENTS.md` 中提取：
   - 当前仓库有哪些子项目 / 子系统
   - 根级系统导航文件在哪里
   - 每个子项目自己的 codebase 文档根目录在哪里
   - 阅读顺序、维护规则、文档分层规则
3. 按 `AGENTS.md` 指定的阅读顺序继续读取对应文件
4. 如果 `AGENTS.md` 明确给出系统总图、架构文档、结构文档、集成文档、问题文档，按其顺序读取
5. 如果 `AGENTS.md` 只给了目录而没列出具体文件，再到对应目录中寻找最接近的：
   - `ARCHITECTURE.md`
   - `STRUCTURE.md`
   - `CONVENTIONS.md`
   - `INTEGRATIONS.md`
   - `CONCERNS.md`
6. 再读 `scripts/ralph/progress.txt` 中仅 `## Codebase Patterns` 部分
7. 再读 `scripts/ralph/prd.json`

原则：

- `AGENTS.md` 是入口和索引，不是摆设
- 任何子项目边界、文档层级、推荐入口，都以 `AGENTS.md` 为准
- 如果 `AGENTS.md` 与你预想的目录结构不同，服从 `AGENTS.md`

## git 取证范围

先确定默认分支，再看当前分支相对默认分支的真实变化。

推荐方式：

```bash
DEFAULT_BRANCH=$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
DEFAULT_BRANCH=${DEFAULT_BRANCH:-main}
MERGE_BASE=$(git merge-base HEAD "origin/$DEFAULT_BRANCH" 2>/dev/null || git merge-base HEAD "$DEFAULT_BRANCH")
git log --reverse --oneline "$MERGE_BASE"..HEAD
git diff --name-status "$MERGE_BASE"..HEAD
```

只关注默认分支到当前 `HEAD` 之间、最终仍然成立的变化。

## 提炼标准

只保留这几类信息：

- 当前目录结构已经真实变化，未来必须知道的新入口、新模块、新页面、新接口归属
- 多个 story 反复出现、未来会复用的稳定开发约定
- 很容易再次踩坑、且与本项目框架/响应模型/数据边界/路由方式强相关的陷阱
- 会影响 AI 编码正确性的系统边界、前后端协作边界、公开接口边界

不要保留这些内容：

- story 编号、提交过程、修复经过
- “需要手动验证”“没有某工具”之类的执行环境信息
- 自动化 harness 自身的实现细节
- 一次性事故、临时占位文件、偶发噪音
- 已在现有文档中准确表达、无需更新的内容

## 文档落位规则

严格按 `AGENTS.md` 定义的分层写入，不要假设仓库一定是前后端双项目。

通用落位原则：

- 某个子项目内部目录结构变化 → 写入该子项目自己的 `STRUCTURE.md`
- 某个子项目的稳定开发约定 → 写入该子项目自己的 `CONVENTIONS.md`
- 某个子项目与其他系统的接口 / 认证 / 数据来源关系 → 写入该子项目自己的 `INTEGRATIONS.md`
- 某个子项目的高频陷阱、技术债、风险 → 写入该子项目自己的 `CONCERNS.md`
- 仓库级阅读顺序、导航、维护规则 → 写入根级 `AGENTS.md`
- 系统级职责划分、跨项目关系、总图导航 → 写入 `AGENTS.md` 指向的系统级地图文档

重要：

- 根级 `AGENTS.md` 只允许写仓库级导航、阅读顺序、维护规则
- 不要把任一子项目的内部实现细节堆到根 `AGENTS.md`
- 如果 `AGENTS.md` 明确要求“不要在根目录生成统一 codebase map”，必须遵守
- 如果只是某个子项目内部重构，不要修改其他子项目的 codebase 文档

## 工作流程

### 1. 建立范围

- 确认当前分支名称
- 找到默认分支与 merge-base
- 获取本分支新增、修改、删除的关键文件与目录

### 2. 交叉取证

同时对照：

- 当前目录结构
- git diff / git log
- `progress.txt` 的 `Codebase Patterns`
- `prd.json` 的最终功能范围

如果 `progress.txt` 里写了经验，但当前代码已不再体现，不要写入文档。

### 3. 归纳而不是抄录

不要把 `Codebase Patterns` 原样搬进文档。

每条候选经验都要先问：

- 它是不是当前代码仍然成立？
- 它是不是项目知识，而不是自动化工具知识？
- 它是不是未来开发会再次遇到？
- 它应该落在哪一层文档，而不是“统统写进 AGENTS”？

### 4. 最小修改

- 优先补充、修正、去重
- 不重写整份文档
- 保持现有文档风格和章节层级
- 一条经验只写一次，写到最合适的位置

### 5. 交付说明

完成后用简短摘要说明：

- 更新了哪些文档
- 每份文档补了什么类型的信息
- 明确排除了哪些不该沉淀的内容

## 写作要求

- 条目短、信息密度高、可验证
- 用项目语言描述，不提自动化流水线
- 优先描述稳定模式与边界，不写过程叙事
- 如果某项变化只影响单个子项目，不要修改另一个子项目的 codebase 文档

## 默认执行模式

若用户没有特别要求“先只分析”，则直接执行文档更新。

若用户要求先分析再动手，则先输出：

1. 候选沉淀项
2. 对应写入文件
3. 不建议写入的内容

获得确认后再修改文档。
