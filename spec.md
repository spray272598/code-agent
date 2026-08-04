---
id: code-agent-core
title: Code-Agent 核心功能实现
goal: 构建一个完整的 Coding Agent，支持 ReAct 循环、6 大核心工具、权限管理、MCP 协议和多 Agent 协同
constraints:
  - 代码遵循 Go 1.22+ 标准，使用 go.mod 管理依赖
  - 架构采用六边形（Ports & Adapters）模式，domain 层不依赖 infrastructure
  - 所有工具调用必须经过 5 层权限闸门检查
  - 上下文压缩必须保留关键信息（错误消息、高优先级操作）
  - 支持 Linux/macOS/Windows 三平台
acceptance:
  - Agent 能独立完成"读取→编辑→验证"的编码任务
  - 工具调用错误被正确捕获和反馈
  - 会话上下文在 token 预算内自动压缩
  - 权限系统能拦截危险操作并请求用户确认
  - Docker 部署可一键启动所有服务
tech_notes: 使用 CloudWeGo Eino 框架 + 自研安全层 + 双引擎架构
---

# Code-Agent 核心功能实现

## 项目概述

Code-Agent 是一个类 Claude Code 的编码 Agent，支持 CLI 和 HTTP Server 双模式部署。

## 核心架构

- **六边形架构**：domain 层纯业务逻辑，adapters 层实现端口，infrastructure 层提供技术实现
- **双引擎编排**：自研 ReAct Loop + Eino Graph 可配置切换
- **5 层权限闸门**：命令过滤 → 路径沙箱 → 工具分级 → 会话放行 → 熔断保护
- **上下文压缩**：L0 截断 → L1 优先级 → L2 硬截断 → L3 LLM 摘要

## 约束条件

- 代码遵循 Go 1.22+ 标准，使用 go.mod 管理依赖
- 架构采用六边形（Ports & Adapters）模式，domain 层不依赖 infrastructure
- 所有工具调用必须经过 5 层权限闸门检查
- 上下文压缩必须保留关键信息（错误消息、高优先级操作）
- 支持 Linux/macOS/Windows 三平台

## 验收标准

- Agent 能独立完成"读取→编辑→验证"的编码任务
- 工具调用错误被正确捕获和反馈
- 会话上下文在 token 预算内自动压缩
- 权限系统能拦截危险操作并请求用户确认
- Docker 部署可一键启动所有服务

## 技术备注

- LLM 后端：任何兼容 OpenAI API 的模型
- 存储：MySQL + Redis（可选）+ MinIO（可选）
- 可观测性：Jaeger (OTLP) + Prometheus + 结构化日志
