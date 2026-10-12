# coresugar

Go + MCP + crash 的离线 Linux 内核崩溃分析工具。

开发方案见 [design.md](design.md)，实施顺序见 [development-plan.md](development-plan.md)。

## 编译与测试

需要 Go 1.23 或更新版本。crash 实际运行需要 Linux，Go SDK 和 PTY 版本固定在 go.mod 中。

```bash
go test ./...
go build -o bin/coresugar ./cmd/coresugar
GOOS=linux GOARCH=amd64 go build -o bin/coresugar-linux-amd64 ./cmd/coresugar
```

## 环境检查

在 Linux 上执行：

```bash
./coresugar check --crash /usr/bin/crash --directory /data/coresugar
```

返回 JSON；退出码 0 表示检查通过，1 表示环境不可用，2 表示参数错误。
macOS 可用于开发和单元测试，crash 的环境检查与实际运行需要 Linux。

## 输入验证

```bash
./coresugar validate \
  --vmcore /path/to/vmcore \
  --vmlinux /path/to/vmlinux \
  --source-dir /path/to/linux-source
```

退出码 0 表示输入基础校验通过；`PARTIAL` 表示精确构建身份、源码或模块符号仍未确认。
退出码 1 表示 `INVALID` 或 `MISMATCH`，拒绝继续加载。相同内核 release 但编译时间不同也会返回 `MISMATCH`。
目前支持 KDUMP/ELF core 和内含 DWARF 的 ELF vmlinux；独立 debuginfo 布局尚不支持。

## 命令行调试

```bash
./coresugar debug \
  --vmcore /path/to/vmcore \
  --vmlinux /path/to/vmlinux \
  --source-dir /path/to/linux-source \
  --command-timeout 120s
```

从 stdin 逐行读取命令，输出 JSON Lines；输入 `quit` 或 EOF 关闭进程。
默认响应上限 64 KiB，采集上限 16 MiB，可通过 `--max-output-bytes`、`--max-capture-bytes` 降低。
超时或采集超限关闭会话；普通 crash 命令错误和命令拒绝不损坏会话。

支持 sys、log、bt、ps、kmem -i、dis、sym、whatis、p、struct、rd 的受限只读形式。
具体参数范围见 [分析 Skill](skills/coresugar-analysis/SKILL.md)。

## MCP 服务

```bash
./coresugar serve --config /path/to/config.json
```

配置参考 [config.example.json](configs/config.example.json)，使用 JSON，不需要 YAML 依赖。
未指定配置时采用默认值；配置文件覆盖默认值，显式 CLI 参数覆盖相应配置。
配置允许目录应明确包含 vmcore、vmlinux 和源码的位置。

七个 Tool：open_dump、crash_exec、get_session、close_dump、get_evidence、read_source、search_source。
默认最多两个活动会话，同会话并发请求返回 BUSY，不同会话可并行。
空闲会话关闭后，其内存状态会过期；证据保存在磁盘，可在服务重启后继续查询。

MCP stdout 只输出协议，诊断写入 stderr。SSH 客户端示例见 [mcp.example.json](configs/mcp.example.json)。
测试机已验证免交互 SSH 登录，密码不写入配置或日志。

### 测试环境配置

复制 `tests/integration/environment.example.json` 为 `environment.local.json`，填入真实路径。
若相同内核 release 的构建时间不同，校验仍会拒绝输入；需要选择与 vmcore 对应的符号文件。
本地环境配置、原始分析输出和运行记录不纳入版本控制。

### 源码与证据限制

源码按相对路径访问；绝对编译路径只接受配置中的 `source_mappings` 映射。
源码和证据读取均禁止通过符号链接跳转。源码单文件最多 8 MiB，单次最多 300 行。
搜索是按行字面量匹配，跳过 .git、二进制及符号链接，最多 100 条结果、10 秒、256 MiB 扫描量。
截断原因会随结果返回，建议先指定子目录。

`get_evidence` 的 offset/limit 按字节计数。响应截断和采集截断分别标记；`complete` 只表示已读完保存的部分。
跨 UTF-8 字符边界的页提供 `output_base64`，精确重组使用该字段；元信息包含完整保存输出的 SHA256。
当前不会自动删除证据，应根据使用量管理目录。

## 真实集成测试

默认 `go test ./...` 只跑本地测试，未配置的真实集成测试明确跳过。
在 Linux 测试机运行完整回归，需要设置绝对配置路径和实际二进制路径：

```bash
CORESUGAR_TEST_CONFIG=/absolute/path/environment.local.json \
CORESUGAR_MCP_BINARY=/absolute/path/coresugar \
go test -count=1 -v ./...
```

从 macOS 经 SSH 验证 MCP 时，额外设置 `CORESUGAR_MCP_SSH=root@linux-test.example.test`，二进制路径使用服务器上的路径。
`TestMCPAnalysis` 可通过显式设置 `CORESUGAR_CASE_ARTIFACT` 保存观察结果，未设置时不写入工作区。

当前样本是一个 divide error 案例，测试覆盖工程行为及真实 Tool 链路；五个已知原因案例评测尚未完成。
详细结果见 [开发进度](development-plan.md) 和 [验证摘要](docs/validation.md)。
