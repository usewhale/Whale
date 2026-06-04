---
name: agents-map
description: 根据一个或多个子项目的 `.planning/codebase/` 事实文档，生成或刷新根级 `AGENTS.md`、根级 `ARCHITECTURE.md` 与系统级补充文档。适用于 monorepo、多应用工作区、前后端分仓合并目录、服务拆分仓等场景。当用户提到“创建 AGENTS.md 地图”“根据 A/.planning/codebase B/.planning/codebase 生成 AGENTS.md”“汇总多个子项目导航规则”时使用。本技能先读取子项目事实层，再沉淀根级系统架构图与系统级补充说明，最后生成简洁的导航型 `AGENTS.md`，避免复制实现细节。
---

# AGENTS / ARCHITECTURE 地图生成器

根据多个子项目的 `.planning/codebase/` 目录，按当前结构生成文档：

1. 根级 `AGENTS.md`：全局索引地图
2. 根级 `ARCHITECTURE.md`：系统级架构图
3. 根级系统级补充文档：集成说明文档
4. 子项目 `.planning/codebase/`：事实文档

当仓库包含 3 个以上子系统、已经存在 `AGENTS.md`、或者有额外的系统级导航文档时，先读 `references/agents_principles.md` 再起草。

## 期望输入

接受类似下面的请求：

- `创建 AGENTS.md 地图，根据 front/.planning/codebase back/.planning/codebase`
- `根据 A/.planning/codebase B/.planning/codebase C/.planning/codebase 生成 AGENTS.md`
- `刷新根 AGENTS.md，参考 app/.planning/codebase admin/.planning/codebase api/.planning/codebase`

将用户给出的每个 `.planning/codebase/` 路径视为一个子系统，除非用户明确说明不是。

如果用户还提供了以下材料，也一并读取：

- 现有 `AGENTS.md`
- 现有 `ARCHITECTURE.md`
- 其他跨项目边界文档

## 分层定位

始终按下面的职责理解与生成：

- 根 `AGENTS.md`：只做入口、索引、阅读顺序、维护规则
- 根 `ARCHITECTURE.md`：承接系统边界、子系统职责、推荐理解路径
- 系统级补充文档：承接跨项目关系、接口边界、集成说明
- 子项目 `.planning/codebase/`：保留各自系统的事实描述

不要把这些层混写成一层，也不要让根 `AGENTS.md` 直接承担系统说明书的角色。

## 工作流

1. 读取每个传入的 `.planning/codebase/` 目录，把它们视为事实来源。
2. 优先阅读这些文件（如果存在）：
   - `ARCHITECTURE.md`
   - `STRUCTURE.md`
   - `INTEGRATIONS.md`
   - `CONCERNS.md`
   - `STACK.md`
3. 对每个子系统提炼：
   - 子系统名称
   - 主要职责
   - 责任边界
   - 是否可独立维护
4. 仅基于明确证据推断跨项目关系，不要脑补依赖。
5. 先生成或刷新根级 `ARCHITECTURE.md`，把系统边界与理解路径沉淀为系统级架构图。
6. 再生成或刷新系统级补充文档，把跨项目信息沉淀为集成说明。
7. 系统级补充文档默认至少维护：
   - `INTERFACES.md`：接口边界与调用关系
   - 如证据充分且仓库规模需要，可补充 `DEPLOYMENT.md`、`DECISIONS.md`
8. 最后基于根级 `ARCHITECTURE.md` 与系统级补充文档生成根级 `AGENTS.md`，通常包含：
   - 仓库定位
   - 子系统边界规则
   - 文档分层规则
   - 按任务分类的阅读顺序
   - 维护更新规则
   - 推荐入口
9. 保持简洁、稳定、可导航。
10. 不要把 `.planning/codebase/` 的实现细节复制进根 `AGENTS.md`。
11. 证据不足时，用“当前建议”“初步判断”“需确认”表达，而不是写成硬规则。

## 输出原则

把输出当作“分层地图”，不要当作“单层知识库摘要”。

生成顺序始终是：

1. 子项目 `.planning/codebase/` 提供事实
2. 根级 `ARCHITECTURE.md` 汇总系统级架构
3. 根级系统级补充文档汇总集成说明
4. 根级 `AGENTS.md` 提供访问索引

根 `AGENTS.md` 应优先保留：

- 仓库整体定位
- 子系统拆分与边界
- 文档分层约定
- 阅读顺序
- 维护责任
- 推荐入口

根 `AGENTS.md` 不应堆积：

- 系统集成细节
- 接口明细
- 低层目录说明
- 具体实现模式
- 高频变化路径
- 从 `.planning/codebase/` 复制来的压缩摘要

根 `ARCHITECTURE.md` 应优先保留：

- 系统边界
- 子系统职责
- 系统级理解路径
- 稳定的目录职责
- 系统层面的维护约定

系统级补充文档应优先保留：

- 子系统之间的关系
- 集成边界与协作方式
- 接口与部署等跨项目说明
- 需要跨项目同步维护的约定

`.planning/codebase/` 继续保留：

- 子项目内部架构事实
- 目录与模块组织
- 内部集成与实现模式
- 该子项目特有的约束与风险

## 判断规则

只有同时满足下面条件的信息，才适合进入根 `AGENTS.md`：

- 对全仓库多数任务都有帮助
- 比较稳定，不容易漂移
- 能帮助代理判断“先看哪里”和“边界在哪里”

出现以下情况时，不要放进根 `AGENTS.md`：

- 只对某个子项目成立
- 偏实现细节
- 容易随重构频繁变化
- 已经在 `.planning/codebase/` 里表达得更好

只有同时满足下面条件的信息，才适合进入根 `ARCHITECTURE.md`：

- 对整个仓库的理解路径有帮助
- 比 `AGENTS.md` 更偏系统架构
- 比系统级补充文档更偏稳定总览
- 不属于某个单独子项目的内部实现

只有同时满足下面条件的信息，才适合进入系统级补充文档：

- 需要跨子项目理解或协作
- 比子项目事实更偏接口、集成、协作关系
- 不适合塞进根 `AGENTS.md` 或根 `ARCHITECTURE.md`
- 但又不能只留在某一个子项目里

## 推荐结构

除非仓库本身强烈暗示其他组织方式，否则优先使用下面结构：

### 根 `AGENTS.md`

1. 仓库定位
2. 文档分层规则
3. 阅读方式
4. 按任务分类的阅读顺序
5. 维护规则
6. 当前推荐入口

### 根 `ARCHITECTURE.md`

1. 系统边界
2. 子系统职责
3. 推荐理解方式
4. 推荐目录职责
5. 系统层面的维护建议

### 根 `INTERFACES.md`

1. 已确认接口边界
2. 未证实的跨系统关系
3. 任务排查建议
4. 可扩展的集成文档入口

## 处理歧义

如果子系统关系不清楚：

- 不要发明依赖关系
- 使用“根据现有 `.planning/codebase/` 文档，当前边界看起来是……”这类表述
- 必要时补一个“需确认”提示

如果某个子系统缺少文档：

- 基于已有材料继续
- 明确指出缺了哪些文档
- 避免对该子系统做强结论

## 可选建议

如果仓库很大，或者子系统由不同团队独立维护，可以额外建议：

- 根 `AGENTS.md` 负责索引
- 根 `ARCHITECTURE.md` 负责系统级架构图
- 系统级补充文档负责集成说明
- 各子系统 `.planning/codebase/` 负责事实
- 各子系统可再维护局部 `AGENTS.md`

除非用户明确要求，否则不要自动创建子项目级 `AGENTS.md`。
