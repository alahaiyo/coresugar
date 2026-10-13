# coresugar

Go + MCP + crash 的离线 Linux 内核崩溃分析工具。

coresugar 为现有 Codex、Claude Code 等 AI Agent 提供调试工具。Agent 负责选择命令、分析证据和撰写报告；coresugar 负责控制 crash、访问源码和保存证据，无需单独配置模型 API Key。

开发方案见 [design.md](design.md)，实施顺序见 [development-plan.md](development-plan.md)。

## 快速开始：Agent 与资料在同一台 Linux

### 1. 安装并编译

准备 Go 1.23 以上版本，以及能够加载目标内核的 crash。执行：

```bash
git clone https://github.com/alahaiyo/coresugar.git
cd coresugar
go build -o bin/coresugar ./cmd/coresugar
```

后续在这个项目目录操作。用 `pwd` 获取项目绝对路径，配置 Agent 时替换下面的 `/home/yourname/coresugar`。

### 2. 校验已有资料

假设文件已经位于：

```text
/data/case-001/vmcore
/data/case-001/vmlinux
/data/case-001/linux-source/
```

这些是示例路径，替换成自己的实际路径即可，不需要移动或重复复制 vmcore。
运行账户需要能读取这些文件；当前 vmlinux 必须内含 DWARF 调试信息。

```bash
./bin/coresugar check --directory /data/case-001

./bin/coresugar validate \
  --vmcore /data/case-001/vmcore \
  --vmlinux /data/case-001/vmlinux \
  --source-dir /data/case-001/linux-source
```

`PARTIAL` 表示基础检查通过，部分匹配关系尚未确认；`MISMATCH`、`INVALID` 必须先解决。
相同内核 release 不一定来自同一次构建，编译时间不同也会被拒绝。

### 3. 创建服务配置

在项目目录创建 `config.json`：

```json
{
  "crash": "/usr/bin/crash",
  "allowed_roots": ["/data/case-001"],
  "evidence_dir": "/data/case-001/evidence",
  "command_timeout_ms": 120000
}
```

其余字段使用默认值，完整配置见 [config.example.json](configs/config.example.json)。
资料分散在不同目录时，将相应目录都加入 allowed_roots；evidence_dir 必须可写。
配置不会绑定某一份 vmcore，实际输入路径由 Agent 调用 open_dump 时传入。

### 4. 接入 Codex 或 Claude Code

任选所用客户端配置。MCP 使用 stdio，由 Agent 自动启动 `coresugar serve`，无需提前运行常驻服务或开放 HTTP 端口。

**Codex**

在现有 `~/.codex/config.toml` 中添加以下条目；如果已存在 coresugar 条目，修改原条目，避免重复定义：

```toml
[mcp_servers.coresugar]
command = "/home/yourname/coresugar/bin/coresugar"
args = ["serve", "--config", "/home/yourname/coresugar/config.json"]
startup_timeout_sec = 30
tool_timeout_sec = 180
```

工具超时设置为 180 秒，给最多 120 秒的 vmcore 初始化和命令执行留出余量。
配置后启动新会话，在 `/mcp` 中确认服务和工具可用。
配置字段见 [OpenAI 官方 MCP 文档](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)。

**Claude Code**

```bash
claude mcp add --transport stdio --scope user coresugar -- \
  /home/yourname/coresugar/bin/coresugar serve \
  --config /home/yourname/coresugar/config.json

claude mcp list
```

配置后启动新的 Claude Code 会话，用 `/mcp` 检查连接。
如果自己设置了客户端工具超时，应给初始化预留至少 120 秒。
命令格式见 [Claude Code 官方 MCP 文档](https://code.claude.com/docs/en/mcp)。

### 5. 让 Agent 开始分析

在项目目录启动 Agent，发送下面的提示，替换实际输入路径：

```text
请先读取 skills/coresugar-analysis/SKILL.md，
通过 coresugar MCP 工具分析下面的内核崩溃：

vmcore：/data/case-001/vmcore
vmlinux：/data/case-001/vmlinux
源码目录：/data/case-001/linux-source

先核对输入，再分析日志、异常调用栈、寄存器、故障指令和相关源码。
关键结论必须引用实际 evidence_id，区分直接原因、根因判断和未确认的信息。
每完成一个调查阶段，调用 record_analysis_step，记录做了什么、发现了什么和未确认的问题。
结束前再记录本轮结果与停止原因，程序会保存 analysis-summary.md。
最终将报告写入 analysis-report.md，并关闭调试会话。
```

首次使用可以先要求 Agent 只完成 `open_dump → crash_exec("sys") → close_dump`。
确认界面中出现真实工具调用和返回值后，再执行完整分析。
可用的 Tool 及输出限制见下方说明。

## 远程使用：Agent 在本机，资料在 Linux 服务器

先在 Linux 服务器完成上述第 1～3 步。假设服务程序和配置位于 `/home/debug/coresugar`，SSH 目标为 `debug@debug-server`。
所有路径均替换成实际值，先确认非交互 SSH 可用：

```bash
ssh -T -o BatchMode=yes debug@debug-server \
  /home/debug/coresugar/bin/coresugar check --directory /data/case-001
```

**本机 Codex** 的 MCP 条目改为：

```toml
[mcp_servers.coresugar]
command = "ssh"
args = [
  "-T", "-o", "BatchMode=yes", "debug@debug-server",
  "/home/debug/coresugar/bin/coresugar", "serve",
  "--config", "/home/debug/coresugar/config.json"
]
startup_timeout_sec = 30
tool_timeout_sec = 180
```

**本机 Claude Code** 注册 SSH 启动命令：

```bash
claude mcp add --transport stdio --scope user coresugar -- \
  ssh -T -o BatchMode=yes debug@debug-server \
  /home/debug/coresugar/bin/coresugar serve \
  --config /home/debug/coresugar/config.json
```

上述远程配置用于替换同名的本地配置，不要重复注册同名服务。
SSH 登录脚本不得向 stdout 输出欢迎信息等内容，以免混入 MCP 协议。

分析说明也需要放在本机 Agent 能读取的位置。例如，在本机工作目录执行：

```bash
scp debug@debug-server:/home/debug/coresugar/skills/coresugar-analysis/SKILL.md \
  ./coresugar-analysis.md
```

提示 Agent 先读取 `./coresugar-analysis.md`，再按前面的分析提示操作。
输入文件路径始终使用 Linux 服务器上的路径，报告可写入 Agent 的本机工作目录；vmcore 无需传到本机，Agent 获取的是调试命令输出。

## 当前验证范围

已验证真实 Linux 资料、八个 MCP Tool、SSH 调用、证据保存、调查总结和会话生命周期。
新增命令范围已进行参数校验与受控进程测试，尚未逐项完成真实 vmcore 验证；
实际命令和选项支持情况取决于安装的 crash 版本、目标架构与内核。
已有分析报告由 Codex 会话读取测试客户端获取的证据并撰写。
独立 Codex/Claude Code 直接接入 MCP、自主完成多轮分析的端到端验收，以及五个已知原因案例评测仍待完成。

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
./bin/coresugar check --crash /usr/bin/crash --directory /data/coresugar
```

返回 JSON；退出码 0 表示检查通过，1 表示环境不可用，2 表示参数错误。
macOS 可用于开发和单元测试，crash 的环境检查与实际运行需要 Linux。

## 输入验证

```bash
./bin/coresugar validate \
  --vmcore /path/to/vmcore \
  --vmlinux /path/to/vmlinux \
  --source-dir /path/to/linux-source
```

退出码 0 表示输入基础校验通过；`PARTIAL` 表示精确构建身份、源码或模块符号仍未确认。
退出码 1 表示 `INVALID` 或 `MISMATCH`，拒绝继续加载。相同内核 release 但编译时间不同也会返回 `MISMATCH`。
目前支持 KDUMP/ELF core 和内含 DWARF 的 ELF vmlinux；独立 debuginfo 布局尚不支持。

## 命令行调试

```bash
./bin/coresugar debug \
  --vmcore /path/to/vmcore \
  --vmlinux /path/to/vmlinux \
  --source-dir /path/to/linux-source \
  --command-timeout 120s
```

从 stdin 逐行读取命令，输出 JSON Lines；输入 `quit` 或 EOF 关闭进程。
默认响应上限 64 KiB，采集上限 16 MiB，可通过 `--max-output-bytes`、`--max-capture-bytes` 降低。
超时或采集超限关闭会话；普通 crash 命令错误和命令拒绝不损坏会话。

开放 43 个 crash 命令入口，涵盖内存/slab/页表、任务与调度、等待队列、IRQ、
文件系统、网络、BPF、链表和树等调查；支持受限 `foreach`、每 CPU 数据、
结构体成员与数组、上下文切换以及明确路径的模块符号加载。
`help 命令名` 可查询安装版本的帮助；只允许分析 Skill 列出的参数。
上下文和符号加载只改变调试器状态，不修改 vmcore；shell、脚本、任意 GDB、
插件加载和文件写入仍被拒绝。
具体参数范围见 [分析 Skill](skills/coresugar-analysis/SKILL.md)。

## MCP 服务

```bash
./bin/coresugar serve --config /path/to/config.json
```

配置参考 [config.example.json](configs/config.example.json)，使用 JSON，不需要 YAML 依赖。
未指定配置时采用默认值；配置文件覆盖默认值，显式 CLI 参数覆盖相应配置。
配置允许目录应明确包含 vmcore、vmlinux 和源码的位置。

八个 Tool：open_dump、crash_exec、get_session、close_dump、get_evidence、read_source、search_source、record_analysis_step。
默认最多两个活动会话，同会话并发请求返回 BUSY，不同会话可并行。
空闲会话关闭后，其内存状态会过期；证据保存在磁盘，可在服务重启后继续查询。

MCP stdout 只输出协议，诊断写入 stderr。SSH 客户端示例见 [mcp.example.json](configs/mcp.example.json)。
测试机已验证免交互 SSH 登录，密码不写入配置或日志。

### 调查过程总结

每次打开 vmcore，程序在 `<evidence_dir>/<session_id>/analysis-summary.md` 创建调查总结。
Agent 每完成一个阶段调用 `record_analysis_step`，提交标题、通俗说明和可选证据编号，
程序自动按编号追加成 Markdown。例如：

1. **查看调用栈和寄存器**

   执行 bt，定位到 transfer_surpluses+878，发现 RCX 为 0。调用路径涉及 io.cost 定时器。

2. **检查实际执行的指令**

   执行 dis，确认故障指令是 div %rcx，可以确认直接原因是除零；零值形成原因尚未查清。

最后一条总结应说明本轮结果、停止原因和未执行的调查，方便判断是否需要调整程序。
分析说明和上面的提示要求 Agent 逐阶段提交；如果 Agent 未调用此接口，文件只有初始说明。
服务端负责保存和排版，并校验证据编号，具体调查发现由使用工具的 Agent 总结。
标题最多 512 字节、单条正文最多 16 KiB、证据编号最多 32 个，总文件上限 1 MiB。
会话关闭或服务重启后仍可追加；文件保存在 Linux 服务器，可下载到本机阅读。

### 原始操作日志

`serve` 默认记录所有 MCP 工具调用，无需 Agent 手工写日志或增加配置：

- 总日志：`<evidence_dir>/operations.jsonl`，包含打开会话失败、参数错误和未知工具请求。
- 每次分析的会话日志：`<evidence_dir>/<session_id>/operations.jsonl`。

每行是一条 JSON 记录，包含 UTC 时间、调用编号 `call_id`、客户端名称、工具名、
会话编号和参数。一次调用分为 `started` 与 `finished` 两条记录；结束记录还包含
耗时、成功/失败、错误及结果元信息（证据编号、会话状态、输出截断等）。
可按 `call_id` 配对，并通过证据编号查看完整命令输出；大段输出不重复写入操作日志。

`open_dump` 开始时尚无会话编号，所以开始记录只写入总日志，成功结果写入会话日志。
日志追加保存，关闭会话、服务重启后仍可读取；重启后的证据查询也会继续追加。
如果进程中断，可能只留下 `started`，这不能直接证明操作未执行。
日志写入失败会向 stderr 输出 `OPERATION_LOG_WRITE_FAILED`，原工具响应保持原样。

例如，在保存证据的 Linux 服务器查看某次分析：

```bash
cat /data/coresugar-evidence/cs-实际会话编号/operations.jsonl
```

日志记录通过 coresugar MCP 实际执行的操作。Agent 自身的判断、未调用的操作，
以及在其他终端直接执行的命令，服务端无法观察；它不会自动判断根因或决定停止分析。

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
