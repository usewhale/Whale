---
name: agents-md-slim
description: 精简 AGENTS.md 内容，移除文件路径引用、冗余信息和过度细节，确保指令预算高效。适用于 AGENTS.md 过大、包含易过时的文件路径、或含有 Agent 可自行发现的冗余信息时。
---

# 精简 AGENTS.md 内容

AGENTS.md 在每次请求时都会被完整加载，因此必须精简。本 skill 遵循三条精简原则。

## 三条精简原则

### 原则一：控制指令预算

AGENTS.md 应尽可能小。每一条信息都要占用 token 并与任务指令竞争注意力。

**应保留**：
- 一句话项目描述
- 包管理器（非 npm 时必须注明）
- 非标准的构建/类型检查命令
- 真正与每个任务都相关的全局约定

**应移除的过度细节**：
- 详细的路径表格、架构清单 → 压缩为一句话能力描述
- 多行展开的组件列表 → 合并为单行概括
- 重复说明的调用链路 → 保留方向，去掉冗余前缀

**判断标准**：如果移除某条信息后 Agent 仍能通过阅读代码自行理解，则该信息属于过度细节。

### 原则二：描述能力，而非路径

文件路径是 AGENTS.md 中最容易过时的内容。路径重命名或文件移动后，AGENTS.md 就会变成误导信息。

**禁止**：
- 列出具体文件路径，如 `app/api/routes/<指标>.py`
- 引用具体函数名，如 `calc_trend() 函数`
- 描述文件组织结构的表格中包含路径列

**应该**：
- 描述能力和职责，如"Routes 解析请求并委派 Service"
- 描述组织模式，如"按指标名分文件组织"
- 描述整体形状，让 Agent 自己探索定位

### 原则三：移除 Agent 可自行发现的信息

不要把 Agent 阅读代码就能知道的信息写进 AGENTS.md。

**应移除的冗余信息**：
- 语言/框架标准模式（如"每层通过 `__init__.py` 统一导出"）
- 代码级实现细节（如"Service 使用静态方法"）
- 配置级细节（如"数据库为 SQLite，启动时通过 `Base.metadata.create_all()` 自动建表"）
- 框架默认行为（如"所有 API 调用均为带类型的函数"）

**应保留的关键信息**：
- 非显而易见的架构约定（如"修改数据结构时前后端类型必须同步更新"）
- 与标准做法的偏离（如"包管理器为 uv 而非 pip"）
- Agent 无法从代码推断的隐式约定（如"CORS 仅配置了 localhost:5173"）

## 执行步骤

### 1. 逐条审查

读取现有 AGENTS.md，对每一条信息判断：
- 是否包含文件路径？→ 原则二
- 是否为 Agent 可自行发现的信息？→ 原则三
- 是否为过度细节？→ 原则一

### 2. 提出修改建议

对标记为问题的内容，用能力描述替换路径引用，用简洁概括替换冗长细节。将修改建议列出，等待用户确认后再修改。

### 3. 验证

确认修改后的 AGENTS.md：
- 不包含具体文件路径
- 不包含 Agent 可自行发现的信息
- 描述简洁，每节尽量用一句话概括

## 示例

### 示例 1：路径表格 → 能力描述

**Before：**
```
| 层级 | 路径 | 职责 |
|------|------|------|
| Routes | `app/api/routes/<指标>.py` | FastAPI 路由，解析请求，委派给 Service |
| DTOs | `app/api/dtos/<指标>.py` | Pydantic 模型，负责请求/响应序列化 |
| Services | `app/services/<指标>.py` | 业务逻辑层，调用 Repo 并转换为 DTO |
| Repos | `app/db/repos/<指标>.py` | 数据访问层，封装 SQLAlchemy 查询 |
| Models | `app/db/models/<指标>.py` | SQLAlchemy ORM 表定义 |
```

**After：**
每项健康指标在各层都遵循相同模式：Routes 解析请求并委派 Service，DTOs 负责序列化，Services 处理业务逻辑，Repos 封装数据查询，Models 定义 ORM 表。按指标名分文件组织。

### 示例 2：文件清单 → 一句话概括

**Before：**
```
- `src/api/client.ts` - 集中式 axios 客户端，所有 API 调用均为带类型的函数
- `src/types/health.ts` - TypeScript 接口，与后端 Pydantic DTO 一一对应
- `src/hooks/useHealthData.ts` - 自定义 Hook，通过 `Promise.all` 并行拉取所有指标数据
- `src/components/DashboardGrid.tsx` - 编排组件，将数据分发给各指标卡片
- `src/components/<指标>Card.tsx` - 每个指标一个卡片组件，通过 props 接收数据
- `src/components/TrendChart.tsx` - 共享的 recharts 折线图组件，被各卡片复用
```

**After：**
集中式 API 客户端（带类型）、与后端 DTO 对应的 TypeScript 类型定义、并行拉取数据的自定义 Hook、按指标拆分的卡片组件和共享折线图。

### 示例 3：移除冗余信息

**Before：**
每层通过 `__init__.py` 统一导出。Service 使用静态方法（无实例状态）。数据库为 SQLite（`health.db` 文件），启动时通过 `Base.metadata.create_all()` 自动建表。

**After：**
（整段移除 — 这些都是 Agent 阅读代码即可发现的信息）

## 输出语言

所有修改建议和生成内容使用中文。
