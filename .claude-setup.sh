#!/bin/bash

# Claude Code 会话设置脚本
# 在项目根目录运行此脚本来加载系统指令

INSTRUCTIONS_FILE=".claude-instructions"

if [ -f "$INSTRUCTIONS_FILE" ]; then
    echo "📋 已加载项目系统指令"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    cat "$INSTRUCTIONS_FILE"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    echo "✅ 系统指令已加载，Claude 现在了解项目要求"
else
    echo "⚠️  未找到系统指令文件: $INSTRUCTIONS_FILE"
fi