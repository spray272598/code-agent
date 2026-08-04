---
status: auto-tracked
---

# Acceptance Checklist

## 核心功能

- [x] Agent 能独立完成"读取→编辑→验证"的编码任务
- [x] ReAct 循环正确解析 Thought/Action/Final Answer 格式
- [x] 6 大核心工具（ReadFile/WriteFile/EditFile/Bash/Glob/Grep）全部可用
- [x] EditFile 支持精确替换和正则替换两种模式
- [x] 只读工具并行执行，写操作和 Bash 串行执行

## 安全

- [x] rm -rf / 等破坏性命令被正确拦截
- [x] 路径包含 ../ 逃逸到工作区外被拒绝
- [x] URL 编码、Unicode 编码的路径绕过被防护
- [x] Bash 工具调用需要用户确认
- [x] 连续 5 次拒绝后触发熔断保护

## 上下文

- [x] Token 预算压力检测正确
- [x] L0-L3 四级压缩策略按优先级触发
- [x] 压缩后错误消息和高优先级操作被保留
- [x] LLM 摘要生成保留关键信息

## 扩展

- [x] MCP stdio 客户端可连接外部工具服务
- [x] Skill 系统支持 SKILL.md 加载和工具白名单
- [x] 跨会话记忆支持 user/project 双作用域
- [x] SubAgent 支持 explore/verify/general 三种角色
- [x] Agent Teams 支持 4 种协同模式
- [x] Slash Commands 支持 /help /compact /tools 等
- [x] Hook 系统支持 PreToolUse 拦截

## 工程

- [x] 单元测试覆盖核心逻辑
- [x] Dockerfile 支持 distroless 和 debian-slim 双目标
- [x] Docker Compose 一键启动所有中间件
- [x] 可观测性集成（Traces + Metrics + Logs）
- [x] Spec 三件套（spec.md/tasks.md/checklist.md）支持
