---
name: Code Explore
id: code-explore
description: Explore a codebase with glob/grep/read before editing
triggers:
  - 探索
  - explore
  - 了解代码
  - 项目结构
  - 怎么组织
tools:
  - glob
  - grep
  - read_file
---

# Code Explore Skill

## Goal
Build a quick mental model of the project without changing files.

## Steps
1. `glob` with pattern `**/*` or language-specific globs
2. `grep` for entry points (main, package, export)
3. `read_file` 1–3 key files
4. Summarize architecture in Chinese or English

## Constraints
- Do not write or edit files
- Do not run destructive bash
