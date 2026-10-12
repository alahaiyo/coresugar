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
