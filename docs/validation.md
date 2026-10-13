# 验证摘要

验证日期：2026-10-12。

- macOS 构建及 Linux amd64 交叉编译通过。
- Linux 全量回归：28 个顶层测试通过，0 失败，0 跳过。
- 本地与 Linux 的 race 检查、go vet 通过。
- 真实案例覆盖七个 MCP Tool、连续命令、输出限制、证据分页及重启查询、源码访问、双会话、空闲回收和客户端断连清理。
- 现有案例确认了故障指令的除数为零；更深层根因没有独立对照。

分析由 Codex 会话读取测试客户端获取的证据并撰写报告。独立 AI Agent 直接接入 MCP、自主完成多轮分析的验收尚未完成。
五个已知原因案例的对照评测也尚未完成，因此不宣称最终案例验收全部通过。

原始环境配置、分析输出、证据编号和逐步验收记录保留在本地，不发布到公开仓库。

## 自动操作日志（2026-10-13）

MCP 服务默认保存全部工具调用的开始、结束、参数、客户端名称、结果元信息、
错误及耗时；总日志覆盖未能建立会话的请求，会话日志随证据保留。

- 本地全量测试、race 检查、go vet、macOS 构建和 Linux amd64 交叉编译通过。
- Linux 全量回归：32 个顶层测试通过，0 失败，0 跳过；真实集成包约 195 秒。
- Linux 的 core/mcpserver race 检查及全项目 go vet 通过。
- 测试覆盖全部七个 Tool 的错误记录、参数校验错误、调用配对、结果摘要、
  写入失败保留原响应、日志权限、路径限制及并发追加。
- 真实 vmcore 分析调用链自动生成 21 条会话记录，实际调用顺序和开始/结束配对通过验证。

日志样本及本轮完整测试输出保留在本地 `docs/test-results/`。这是重新运行调用链
生成的日志，不是对先前调查过程补造的日志；服务端记录实际操作，无法记录 Agent 未提交的判断。

## 可读调查总结（2026-10-13）

新增 `record_analysis_step`，由 Agent 提交阶段标题、实际操作、发现和未确认的问题，
程序按编号保存到 `analysis-summary.md`。原始 JSONL 操作日志继续保留。
分析说明要求每阶段提交总结，并在结束时明确记录停止原因。

- 本地全量测试、race 检查、go vet、macOS 构建和 Linux amd64 交叉编译通过。
- Linux 全量回归：36 个顶层测试通过，0 失败，0 跳过；真实集成包约 195 秒。
- Linux core/mcpserver race 检查及全项目 go vet 通过。
- 测试覆盖中文多段总结、顺序编号、证据校验、关闭和重启后追加、并发提交、
  文件权限、路径限制及写入失败。
- 真实分析调用链验证总结接口和 Markdown 落盘；通过重启后的 MCP 服务补录
  case-001 的七步可读调查总结，包括未继续追查的原因，相关证据编号均通过校验。

具体发现由 Agent 总结，服务端不根据命令名推断根因；若 Agent 不提交阶段总结，
新会话的 Markdown 只有初始说明。独立 Agent 自主分析的整体验收仍待完成。

## 复现

复制 `tests/integration/environment.example.json` 为 `environment.local.json`，填入测试环境实际路径。
根据样本预期校验状态设置 expected_status；analysis_path 指定异常函数所在的源码搜索子目录。

```bash
go test ./...
go test -race ./internal/core ./internal/mcpserver
go vet ./...
go build -o bin/coresugar ./cmd/coresugar
```

真实 Linux 集成测试按 README 设置 CORESUGAR_TEST_CONFIG 和 CORESUGAR_MCP_BINARY。
默认本地命令会明确跳过未配置的真实测试，跳过不代表通过。
