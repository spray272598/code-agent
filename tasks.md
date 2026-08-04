---
status: auto-tracked
---

# Tasks

## Phase 1: 基础架构

- [x] task-1: 搭建六边形架构骨架（domain/adapters/infrastructure 三层）
- [x] task-2: 实现 Port 接口定义（LLM、存储、工具、安全等端口）
- [x] task-3: 实现依赖注入和应用装配（bootstrap/app.go）

## Phase 2: Agent 核心

- [x] task-4: 实现 ReAct 主循环（Thought → Action → Observation → Final Answer）
- [x] task-5: 实现 6 大核心工具（ReadFile/WriteFile/EditFile/Bash/Glob/Grep）
- [x] task-6: 实现并行工具执行（只读工具并发、写操作串行）
- [x] task-7: 实现工具执行反思机制（失败后 LLM 分析根因换工具）

## Phase 3: 安全与权限

- [x] task-8: 实现 5 层权限闸门（命令过滤/路径沙箱/工具分级/会话放行/熔断）
- [x] task-9: 实现路径编码绕过防护（URL/Unicode/全角空格）
- [x] task-10: 实现危险操作确认机制（挂起→等待用户批准→恢复执行）

## Phase 4: 上下文管理

- [x] task-11: 实现 Token 预算动态监测和 mid-loop 紧急压缩
- [x] task-12: 实现 L0-L3 四级上下文压缩策略
- [x] task-13: 实现 LLM 摘要生成（保留目标/决策/文件路径/错误/下一步）

## Phase 5: 扩展能力

- [x] task-14: 实现 MCP 协议客户端（stdio 传输、JSON-RPC 2.0、自动重连）
- [x] task-15: 实现 Skill 系统（SKILL.md 加载、工具白名单、依赖组合）
- [x] task-16: 实现跨会话记忆（user/project 双作用域、自动纠错提取）
- [x] task-17: 实现 SubAgent 并行执行（explore/verify/general 角色）
- [x] task-18: 实现 Agent Teams 协同（parallel/review/debate/merge 模式）
- [x] task-19: 实现 Slash Commands（/help /compact /tools /skills /mcp 等）
- [x] task-20: 实现 Hook 生命周期事件（PreToolUse/PostToolUse/PreCompact 等）

## Phase 6: 工程完善

- [x] task-21: 实现 Docker 多阶段构建（distroless 生产镜像 + debian-slim 调试镜像）
- [x] task-22: 实现 Docker Compose 编排（MySQL/Redis/MinIO/Jaeger + 健康检查）
- [x] task-23: 实现 Eino 框架集成（双引擎可配置切换）
- [x] task-24: 实现可观测性（OTLP traces + Prometheus metrics + 结构化日志）
- [x] task-25: 实现 Spec 驱动开发（spec.md/tasks.md/checklist.md 三件套）
