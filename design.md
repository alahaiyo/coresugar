# coresugar — 方案设计文档

**项目名称：coresugar**\
**技术方案：Go + MCP + Linux crash + AI Coding Agent**\
**参考项目：[morluto/flameox](https://github.com/morluto/flameox)**\
**设计版本：v0.1 / MVP**\
**日期：2026-10-12**

---

# 1. 项目概述

## 1.1 项目定位

coresugar 是一个面向 AI Agent 的 Linux 内核崩溃分析工具框架。

通过 Model Context Protocol（MCP），将 Linux `crash` 调试器的分析能力暴露给 Claude Code、Codex 等现有 AI Agent，使 Agent 能够自主执行调试命令、分析内核状态、读取内核源码，并逐步定位 Kernel Panic 原因。

**本项目不开发新的 AI Agent，不重新实现 crash，也不训练专用大模型。**

核心定位是：

> 让现有 AI Agent 像一名 Linux 内核工程师一样使用 crash，通过真实的 vmcore 数据进行可追溯、可验证的内核崩溃分析。

### 典型使用场景

用户提供：

```text
vmcore         内核崩溃时的内存转储
vmlinux        对应的内核 ELF 镜像
debuginfo      对应内核及模块的调试符号
linux-source   对应版本的 Linux 内核源码
```

然后对 AI Agent 说：

> 帮我分析这个 vmcore，找到 Kernel Panic 的原因。必要时分析寄存器、汇编指令、内核数据结构和相关源码，最终生成一份有证据支撑的故障分析报告。

Agent 自动完成：

```text
识别崩溃类型
     ↓
分析 Panic 日志
     ↓
定位异常 CPU / Task
     ↓
分析内核调用栈
     ↓
分析异常寄存器
     ↓
反汇编故障指令
     ↓
检查相关内核数据结构
     ↓
阅读对应内核源码
     ↓
提出并验证根因假设
     ↓
生成故障分析报告
```

其中，Agent 决定如何分析；coresugar MCP 负责可靠、安全、可追溯地执行分析操作。

## 1.2 设计目标

| 目标 | 具体要求 | 优先级 |
|---|---|---|
| Agent 接入 | 支持 Claude Code、Codex 等 MCP 客户端 | P0 |
| crash 集成 | 支持加载 vmcore、vmlinux，执行 crash 命令 | P0 |
| 输入管理 | 接收 vmcore、vmlinux、debuginfo、内核源码 | P0 |
| 会话管理 | 同一个 vmcore 使用持久化 crash 进程 | P0 |
| 命令执行 | 支持多轮交互、超时、异常处理 | P0 |
| 调试证据 | 保留执行的命令和对应输出 | P0 |
| 源码访问 | Agent 可以定位、搜索、读取匹配的内核源码 | P0 |
| 自动分析 | Agent 自主决定下一步调查命令 | P0 |
| 分析报告 | 输出崩溃位置、证据、根因判断和不确定性 | P0 |
| 多会话 | 支持多个 vmcore 独立分析 | P1 |
| 结构化输出 | 自动解析日志、堆栈、寄存器 | P1 |
| 分析策略 | 内置 Panic、死锁、内存损坏等 Skills | P1 |
| drgn 集成 | 支持 Python 内核数据结构分析 | P2 |
| Web UI | 图形化展示分析结果 | 暂不考虑 |

P0 为第一版交付范围，P1、P2 为后续演进方向。

## 1.3 非目标

第一版明确不开发以下能力：

1. 不开发独立的大模型或 Agent 推理框架。
2. 不重新实现 crash、GDB 或内核内存解析器。
3. 不直接修改 Linux 内核源码。
4. 不自动编译、修复或部署内核。
5. 不开发 Web 控制台。
6. 不接入数据库、Redis 或 Kubernetes。
7. 不开发自定义 vmcore 文件格式。
8. 不保证每个 Kernel Panic 都能找到最终根因。
9. 不支持实时调试生产内核，首版仅分析离线 vmcore。
10. 不自动从生产服务器采集或上传 vmcore。

项目第一阶段的核心是实现一个可靠的 **AI 可操作的离线内核调试环境**。

---

# 2. Flameox 架构借鉴

## 2.1 Flameox 的核心设计

根据 Flameox 当前的 README 和架构文档，其定位不是一个独立的 AI Agent，而是面向 Coding Agent 的工具运行与证据管理框架。

Flameox 将底层工具封装为统一操作，主要负责工具调用、执行约束、结果解析和证据管理。Agent 负责提出假设、选择操作并解释结果。

它强调几个设计原则：

- 使用明确的输入路径，而不是隐式搜索工作目录。
- MCP 和 CLI 共用同一个 Runtime。
- 底层工具通过适配器接入。
- 对执行过程设置时间和资源边界。
- 区分观察结果、派生结果和推测性结论。
- 将临时分析结果与持久化证据分开管理。

这些原则非常适合 coresugar。[GitHub](https://github.com/morluto/flameox?utm_source=chatgpt.com)

## 2.2 Flameox 与本项目的对应关系

| Flameox | coresugar | 是否复用设计思路 |
|---|---|---|
| AI Coding Agent | Claude Code / Codex | 是 |
| MCP Server | coresugar MCP Server | 是 |
| Operation Registry | Crash Operation Registry | 是 |
| Analysis Runtime | coresugar Runtime | 是 |
| Profiler Provider | Crash Backend | 是 |
| Subprocess Broker | Crash Process Manager | 是 |
| Output Adapters | Crash Output Parser | 是 |
| Evidence Repository | Crash Evidence Store | 是 |
| Benchmark Comparison | coresugar Analysis | 不直接复用 |
| Profiling Capture | vmcore 调试操作 | 不直接复用 |

这里有一个重要区别：

**Flameox 的 Profiling 任务通常是有边界的采集或分析操作，而 Kernel Panic 分析是多轮、有状态的交互式调试过程。**

例如，Flameox 可以启动一次 `perf` 并读取结果；而 coresugar 可能需要连续执行几十条 crash 命令，期间还要保留已加载的 vmcore、符号表和 GDB 状态。

因此，本项目需要专门设计 **Session Manager**。

这将是相较于 Flameox 的主要架构变化。

## 2.3 决定借鉴和不借鉴的部分

**借鉴：**

- MCP 仅作为薄接口层。
- Runtime 统一执行语义。
- Backend 与 MCP 解耦。
- 明确的数据输入契约。
- 可控的输出大小和执行时间。
- 分析证据与 AI 推理分离。
- 统一的错误模型。
- 明确的操作能力注册表。

**暂不借鉴：**

- 多种 Profiler 自动选择。
- 性能 Benchmark 统计比较。
- 大量可插拔工具适配器。
- 完整的内容寻址对象存储。
- 独立的持久化查询系统。
- 复杂的实验对照和指标体系。

第一版可以保留扩展接口，但不应为了架构完整性提前实现这些功能。

---

# 3. 总体技术架构

## 3.1 开发和运行环境

结合你当前的环境：

- 开发主机：macOS。
- 开发语言：Go。
- Linux 测试服务器：`root@linux-test.example.test`，详细信息见下方测试环境说明。
- AI Agent：Claude Code / Codex。
- 调试工具：crash。
- 接入协议：MCP。
- 远程访问：SSH。
- 运行方式：Linux 上运行 MCP Server + crash。

采用以下架构：

```text
┌────────────────────────────────────────────────────┐
│                     macOS                          │
│                                                    │
│  ┌──────────────────────────────────────────────┐  │
│  │              AI Coding Agent                 │  │
│  │                                              │  │
│  │       Claude Code / Codex / Cursor           │  │
│  │                                              │  │
│  │  · 分析崩溃现象                              │  │
│  │  · 选择下一步调试命令                        │  │
│  │  · 分析结果                                  │  │
│  │  · 形成和验证假设                            │  │
│  │  · 生成最终报告                              │  │
│  └──────────────────┬───────────────────────────┘  │
│                     │                              │
│                MCP over stdio                     │
│                     │                              │
│                     ▼                              │
│                SSH Client                          │
└─────────────────────┼──────────────────────────────┘
                      │
                      │ SSH
                      ▼
┌────────────────────────────────────────────────────┐
│                   Linux Server                     │
│                                                    │
│  ┌──────────────────────────────────────────────┐  │
│  │            coresugar MCP    (Go)             │  │
│  │                                              │  │
│  │  ┌────────────────────────────────────────┐  │  │
│  │  │          MCP Interface Layer           │  │  │
│  │  │                                        │  │  │
│  │  │ open_dump / exec / read_source / close │  │  │
│  │  └───────────────────┬────────────────────┘  │  │
│  │                      │                       │  │
│  │  ┌───────────────────▼────────────────────┐  │  │
│  │  │             Crash Runtime              │  │  │
│  │  │                                        │  │  │
│  │  │ Session Manager                        │  │  │
│  │  │ Command Executor                       │  │  │
│  │  │ Resource Manager                       │  │  │
│  │  │ Source Manager                         │  │  │
│  │  │ Evidence Manager                       │  │  │
│  │  └───────────────┬────────────────────────┘  │  │
│  │                  │                           │  │
│  │  ┌───────────────▼────────────────────────┐  │  │
│  │  │             Crash Backend              │  │  │
│  │  │                                        │  │  │
│  │  │ Process Lifecycle                      │  │  │
│  │  │ PTY / Input & Output                   │  │  │
│  │  │ Command Boundary Detection             │  │  │
│  │  │ Timeout / Recovery                     │  │  │
│  │  └───────────────┬────────────────────────┘  │  │
│  └──────────────────┼───────────────────────────┘  │
│                     │                              │
│                     ▼                              │
│              crash / embedded GDB                  │
│                     │                              │
│       ┌─────────────┼─────────────┐                │
│       ▼             ▼             ▼                │
│     vmcore        vmlinux      debuginfo            │
│                                     │              │
│                              Linux Source          │
└────────────────────────────────────────────────────┘
```

### Linux 测试环境

| 项目 | 配置 |
|---|---|
| 服务器地址 | `linux-test.example.test` |
| SSH 用户 | `root` |
| SSH 认证 | 使用本地 SSH 密钥或私下提供的凭据，不写入仓库 |
| 测试目录 | `/data/coresugar` |
| 已准备资料 | 测试用 vmcore、vmlinux、内核源码等 |

从 macOS 连接测试环境：

```bash
ssh root@linux-test.example.test
```

登录后进入测试目录：

```bash
cd /data/coresugar
```

现有测试目录名称保持不变。集成测试和真实案例验证优先使用该目录中已准备的资料，无需重复准备或复制 vmcore。各输入文件的具体路径及符号、源码匹配情况在测试前确认；本文不预设现有文件的名称和子目录结构。

该环境使用 root 账号进行测试；部署时的低权限运行建议见第 14.1 节。

## 3.2 架构分层

整体划分为六层。

| 层级 | 名称 | 核心职责 |
|---|---|---|
| L1 | AI Agent | 推理、规划、判断和报告生成 |
| L2 | MCP Interface | 向 Agent 暴露标准 Tool |
| L3 | Analysis Runtime | 会话、操作调度、参数校验、证据 |
| L4 | Debugger Backend | 控制 crash 进程、交互和恢复 |
| L5 | Debug Engine | crash/GDB 执行内核调试 |
| L6 | Artifact & Source | vmcore、符号、内核源码 |

主要设计原则：

**L1 不下沉到服务内部，L2 不承担调试器状态管理，L3 不直接解析 vmcore，L4 不负责 AI 推理。**

每一层保持明确职责，降低后续接入 drgn 的成本。

---

# 4. 输入文件设计

首版要求明确提供以下四类输入。

## 4.1 输入清单

| 输入 | 类型 | 是否必需 | 主要用途 |
|---|---|---|---|
| vmcore | 文件 | 必需 | 内核崩溃时的内存状态 |
| vmlinux | ELF 文件 | 必需 | 内核符号、类型和代码定位 |
| debuginfo | 文件或目录 | 条件必需 | DWARF 信息及模块调试符号 |
| kernel source | 目录 | 必需 | 对照内核源码定位原因 |

这里需要注意：**vmlinux 和 debuginfo 不一定是两份独立文件。**

crash 通常需要含有对应调试信息的 vmlinux；如果提供的 vmlinux 已经包含足够的 DWARF 调试信息，那么不需要再提供一份重复的内核 debuginfo。

如果 vmlinux 是 stripped 文件，则需要根据发行版提供的调试符号机制，使用匹配的 debuginfo 文件。

因此，建议用户界面上保留四个输入项，但在内部将符号解析设计成支持多种布局的机制。

crash 官方文档也明确支持使用独立调试文件等方式加载符号，并通过 `--src` 指定内核源码搜索路径。[GitHub](https://github.com/crash-utility/crash/blob/master/crash.8?utm_source=chatgpt.com)

## 4.2 建议的目录结构

```text
/data/coresugar/
│
├── case-001/
│   │
│   ├── vmcore
│   │
│   ├── vmlinux
│   │
│   ├── debuginfo/
│   │   ├── vmlinux.debug
│   │   └── modules/
│   │
│   ├── linux-source/
│   │   ├── kernel/
│   │   ├── mm/
│   │   ├── fs/
│   │   ├── net/
│   │   └── drivers/
│   │
│   └── evidence/
│       ├── manifest.json
│       └── commands.jsonl
│
└── case-002/
```

建议所有路径均使用 Linux 服务器上的绝对路径。

首版不自动上传文件，也不要求把 vmcore 复制进项目目录。可以直接引用已有文件，避免数十 GB 甚至数百 GB 的 vmcore 被重复复制。

## 4.3 输入匹配校验

加载之前需要执行四类检查。

**第一类：基础文件检查**

- 路径存在。
- 文件可读。
- vmcore 是常规文件。
- vmlinux 是可识别的 ELF 文件。
- 内核源码目录存在。
- 符号文件可读。

**第二类：架构检查**

需要确认：

```text
vmcore Architecture
          =
vmlinux Architecture
```

例如：

```text
vmcore:   x86_64
vmlinux:  x86_64
```

**第三类：内核版本与身份校验**

尽可能比较：

- Kernel release。
- vmlinux Build ID。
- vmcore 中的内核版本信息。
- DWARF 调试信息。
- 内核配置。
- 模块 Build ID。

内核版本字符串相同不代表二进制一定相同，尤其是自定义内核、发行版补丁内核或同版本不同构建。

**第四类：源码匹配校验**

源码需要与编译该内核的代码尽可能一致。

对于自定义编译内核，最好提供精确的 Git Commit。

还需要注意，发行版打补丁后的内核源码可能与上游同版本源码存在差异。

## 4.4 输入状态模型

我建议给文件验证定义明确状态：

| 状态 | 含义 | 是否允许分析 |
|---|---|---|
| `VERIFIED` | 已有充分证据证明匹配 | 是 |
| `PARTIAL` | 文件可读，但部分匹配关系未验证 | 是，带警告 |
| `MISMATCH` | 已发现明确不匹配 | 默认拒绝 |
| `INVALID` | 文件损坏、格式不支持或无法读取 | 否 |

对于 `PARTIAL`，Agent 必须知道具体哪些信息尚未验证。

例如：

```json
{
  "artifact_status": "PARTIAL",
  "warnings": [
    "kernel source commit could not be verified",
    "module debuginfo is incomplete"
  ]
}
```

这样可以避免 AI 在使用错误源码或错误符号的情况下，生成看似合理但实际上不可靠的分析结论。

---

# 5. MCP 接口设计

这是第一版最重要的模块之一。

我建议**不要只暴露一个万能的 `crash_exec`，也不要把 crash 的全部命令都封装成独立 MCP Tool**。

最合适的方案是：

**少量通用 Tool + 明确的输入输出契约 + 独立的证据访问接口。**

这样既保留 crash 的完整调试能力，又不会让 MCP 接口变得过于复杂。

## 5.1 MCP Tool 清单

第一版设计 7 个 Tool。

| Tool | 功能 | 说明 |
|---|---|---|
| `open_dump` | 打开分析会话 | 加载四类输入，启动 crash |
| `crash_exec` | 执行 crash 命令 | 核心调试接口 |
| `read_source` | 读取内核源码 | 根据路径和行号读取 |
| `search_source` | 搜索内核源码 | 搜索函数、变量、关键字 |
| `get_evidence` | 查询历史证据 | 查询此前执行过的命令和输出 |
| `get_session` | 查询会话状态 | 获取当前分析对象和会话信息 |
| `close_dump` | 关闭分析会话 | 释放 crash 进程 |

这里的 `search_source` 不一定是 MVP 第一周必须完成的功能，但建议在 v0.1 中提供。

否则，当 Agent 想进一步分析某个函数时，可能需要依赖远程机器上的其他代码搜索方式。

## 5.2 `open_dump`

### 功能

创建一个内核崩溃分析会话。

主要执行：

1. 验证输入参数。
2. 验证四类输入文件。
3. 解析符号布局。
4. 启动 crash。
5. 等待 crash 初始化完成。
6. 获取基本内核信息。
7. 返回 `session_id`。

### 请求

```json
{
  "vmcore": "/data/coresugar/case-001/vmcore",
  "vmlinux": "/data/coresugar/case-001/vmlinux",
  "debuginfo": "/data/coresugar/case-001/debuginfo",
  "source_dir": "/data/coresugar/case-001/linux-source"
}
```

其中 `debuginfo` 可以指向文件或目录。

内部需要由 Symbol Resolver 解析成 crash 实际能够使用的符号文件和模块符号目录。

### 响应

```json
{
  "session_id": "kc-001",
  "status": "READY",
  "debugger": {
    "name": "crash",
    "version": "detected-at-runtime"
  },
  "kernel": {
    "release": "6.1.0-custom",
    "architecture": "x86_64"
  },
  "artifacts": {
    "vmcore": "VERIFIED",
    "vmlinux": "VERIFIED",
    "debuginfo": "PARTIAL",
    "source": "VERIFIED"
  },
  "warnings": [
    "module debuginfo is incomplete"
  ]
}
```

以上为设计示例，不是实际 crash 输出。

### 重要设计

**不要将 `open_dump` 理解为简单执行一条 shell 命令。**

它是一个具有生命周期的操作。

启动成功后，Runtime 内部必须保存：

```text
session_id
    │
    ├── crash process
    ├── PTY connection
    ├── vmcore identity
    ├── vmlinux identity
    ├── source root
    ├── symbol configuration
    ├── command sequence
    ├── session state
    └── evidence location
```

后续所有 `crash_exec` 都使用这个会话。

## 5.3 `crash_exec`

### 功能

在指定会话中执行 crash 命令。

这是 Agent 使用频率最高的接口。

### 请求

```json
{
  "session_id": "kc-001",
  "command": "bt -a",
  "timeout_ms": 30000,
  "max_output_bytes": 65536
}
```

参数说明：

| 参数 | 类型 | 说明 |
|---|---|---|
| `session_id` | string | 分析会话 ID |
| `command` | string | crash 命令 |
| `timeout_ms` | int | 执行超时 |
| `max_output_bytes` | int | 返回给 Agent 的最大输出长度 |

`timeout_ms` 和 `max_output_bytes` 应有服务器级上限，客户端只能在允许范围内调整。

### 响应

```json
{
  "session_id": "kc-001",
  "command_id": "cmd-0007",
  "command": "bt -a",
  "status": "COMPLETED",
  "duration_ms": 128,
  "output": "PID: 0 TASK: ...",
  "truncated": false,
  "evidence_id": "ev-0007"
}
```

其中：

- `command_id` 标识一次调试命令。
- `evidence_id` 标识该次命令产生的证据。
- `output` 是经过边界识别后提取的实际命令输出。
- `truncated` 表示返回给 Agent 的内容是否被截断。

如果原始输出较大，应当允许通过 `get_evidence` 分页读取。

### 命令执行示例

Agent 调用：

```json
{
  "session_id": "kc-001",
  "command": "bt"
}
```

Runtime 执行：

```text
crash> bt
```

假设得到：

```text
PID: 1234
TASK: ffff888012340000
CPU: 2
COMMAND: "kworker/2:1"

#0 machine_kexec
#1 __crash_kexec
#2 panic
#3 oops_end
#4 exc_page_fault
#5 my_driver_function
```

AI Agent 根据结果继续调用：

```json
{
  "session_id": "kc-001",
  "command": "dis -l my_driver_function"
}
```

这里要注意，`bt` 中出现某个函数，不代表该函数已经被证明是最终根因。Agent 后续还需要结合异常指令和状态进一步调查。

## 5.4 `read_source`

### 功能

读取对应内核源码文件的指定行范围。

### 请求

```json
{
  "session_id": "kc-001",
  "file": "drivers/net/example/driver.c",
  "start_line": 120,
  "end_line": 180
}
```

### 响应

```json
{
  "file": "drivers/net/example/driver.c",
  "start_line": 120,
  "end_line": 180,
  "content": "120: ...\n121: ...",
  "source_identity": "sha256:..."
}
```

所有文件路径必须相对于会话中的 `source_dir` 解析。

禁止通过 `../`、符号链接或绝对路径绕过源码根目录限制。

### 为什么需要这个接口？

考虑以下场景：

```text
crash> bt

#5 ext4_writepages+0x30
```

Agent 可能希望继续分析：

```text
fs/ext4/inode.c
```

需要查看：

- 相关函数实现。
- 参数检查。
- 分支条件。
- 数据结构使用方式。
- 对象初始化和释放路径。

如果只提供 crash 命令，Agent 仍然可以利用 `dis` 等方式分析，但源码级推理效率会明显降低。

## 5.5 `search_source`

用于搜索源码中的函数、符号和字符串。

### 请求

```json
{
  "session_id": "kc-001",
  "pattern": "ext4_writepages",
  "path": "fs/ext4",
  "max_results": 20
}
```

### 响应

```json
{
  "matches": [
    {
      "file": "fs/ext4/inode.c",
      "line": 2800,
      "text": "..."
    }
  ],
  "truncated": false
}
```

推荐底层使用 `rg`（ripgrep），但必须通过 Go `exec.CommandContext` 传递独立参数，不经过 shell。

该接口可以采用字面量搜索为默认模式，正则表达式搜索作为显式选项。

同时限制搜索目录、结果数量、运行时间和输出大小。

## 5.6 `get_evidence`

### 功能

读取此前执行过的调试命令及其输出。

### 请求

```json
{
  "session_id": "kc-001",
  "evidence_id": "ev-0007",
  "offset": 0,
  "limit": 65536
}
```

### 响应

```json
{
  "evidence_id": "ev-0007",
  "command": "bt -a",
  "output": "...",
  "offset": 0,
  "next_offset": null,
  "complete": true
}
```

这个接口非常重要。

例如，Agent 执行：

```text
bt -a
```

可能产生数 MB 的输出。

如果全部返回给 Agent：

- 消耗大量 Token。
- 不利于寻找重点。
- 可能超过模型上下文限制。

更合理的设计是：

```text
crash 原始输出
       │
       ▼
Evidence Manager
       │
       ├── 保存受限大小的原始输出
       │
       └── 返回有限长度的输出片段
                    │
                    ▼
                 AI Agent
                    │
                    ▼
            按需读取剩余内容
```

**要区分两个概念：**

- `max_output_bytes`：单次 MCP 响应的输出上限。
- `max_capture_bytes`：本地证据保存的总量上限。

如果采集已超过后者，必须标明证据不完整，而不是让 Agent 误认为拿到了全部输出。

## 5.7 `get_session`

用于读取当前会话信息。

```json
{
  "session_id": "kc-001"
}
```

响应：

```json
{
  "session_id": "kc-001",
  "state": "READY",
  "vmcore": "/data/coresugar/case-001/vmcore",
  "kernel_release": "6.1.0-custom",
  "commands_executed": 12,
  "evidence_count": 12,
  "created_at": "2026-10-12T09:00:00Z"
}
```

这里不应把完整的文件校验信息、所有历史命令和大量证据都返回给 Agent。

只返回会话摘要即可。

## 5.8 `close_dump`

负责：

1. 拒绝新的命令请求。
2. 等待或取消正在执行的命令。
3. 尝试正常退出 crash。
4. 必要时终止 crash 进程。
5. 关闭 PTY。
6. 释放运行时资源。
7. 完成证据记录。

即使 crash 已经意外退出，也应允许调用 `close_dump` 完成资源清理。

---

# 6. Crash Runtime 设计

## 6.1 模块职责

Runtime 是整个项目的核心。

我建议包含五个内部模块：

```text
Crash Runtime
│
├── Session Manager
│
├── Operation Dispatcher
│
├── Resource Manager
│
├── Source Manager
│
└── Evidence Manager
```

具体如下：

| 模块 | 职责 |
|---|---|
| Session Manager | 创建、查询、关闭分析会话 |
| Operation Dispatcher | 参数校验、操作路由、命令执行 |
| Resource Manager | 超时、输出、并发及进程资源控制 |
| Source Manager | 文件校验、符号解析、源码访问 |
| Evidence Manager | 保存命令、结果和输入身份信息 |

## 6.2 Session Manager

### 会话状态机

设计以下状态：

```text
                   open_dump
                      │
                      ▼
                   CREATED
                      │
                      ▼
                  VALIDATING
                      │
                      ▼
                   STARTING
                      │
                      ▼
                    READY
                      │
               crash_exec
                      │
                      ▼
                     BUSY
                      │
              ┌───────┴───────┐
              │               │
           完成执行         执行异常
              │               │
              ▼               ▼
             READY          DEGRADED
              │               │
              │          恢复成功
              │               │
              ◄───────────────┘
              │
           close_dump
              │
              ▼
            CLOSING
              │
              ▼
             CLOSED
```

从任何运行状态都可能因为进程异常、初始化失败或不可恢复的超时进入 `FAILED` 状态。

### 会话对象

Go 结构体建议：

```go
type Session struct {
    ID        string
    State     SessionState
    CreatedAt time.Time

    Artifacts ArtifactSet

    Backend DebuggerBackend

    CommandsExecuted uint64

    Mu sync.Mutex
}
```

其中：

```go
type ArtifactSet struct {
    VMCore      string
    VMLinux     string
    DebugInfo   string
    SourceDir   string
}
```

实际工程中，还需要保存各输入文件的身份、校验状态、打开时的文件元信息等。

### 并发控制

**第一版一个 crash Session 同时只允许执行一条命令。**

这是必要的。

因为同一个 crash 进程只有一个交互式命令通道，如果多个请求同时写入：

```text
bt
ps
kmem -i
```

就可能出现输入交错、输出混合和状态不可预测等问题。

建议：

```text
Session A → 串行执行命令
Session B → 串行执行命令

Session A 与 Session B → 允许并行
```

实现上可以使用 `sync.Mutex`，但长期阻塞的命令等待不宜占用全局锁。

跨 Session 的状态管理使用独立的 `sync.RWMutex`。

### Session 生命周期

第一版建议：

- 一个 MCP Server 进程内支持多个 Session。
- 每个 Session 对应一个 crash 子进程。
- 同一 Session 内串行执行命令。
- MCP Server 退出时清理全部 Session。
- 默认不支持重启后恢复 crash 进程。
- 已保存的证据可以独立于 Session 保留。

考虑到你的运行方式是 SSH stdio，**MCP Server 进程生命周期通常与客户端连接相关**。

因此，如果客户端关闭连接，或者 SSH 中断，crash Session 也可能随之终止。

这是首版可以接受的限制。

后续需要会话跨连接存活时，再设计独立 Daemon 和持久会话管理。

---

# 7. Crash Backend 设计

这是我认为整个项目**工程实现风险最高的模块之一**。

原因不是启动 crash 困难，而是可靠控制交互式程序并判断每条命令的执行结果并不简单。

## 7.1 Backend 抽象

建议定义如下接口：

```go
type DebuggerBackend interface {
    Open(
        ctx context.Context,
        artifacts ArtifactSet,
    ) error

    Execute(
        ctx context.Context,
        command string,
    ) (*CommandResult, error)

    Close(
        ctx context.Context,
    ) error
}
```

`CommandResult`：

```go
type CommandResult struct {
    Output    string
    Duration  time.Duration
    Truncated bool
    Status    CommandStatus
}
```

第一版：

```go
type CrashBackend struct {
    Process *exec.Cmd

    // PTY、reader、command queue、
    // output buffer、state 等内部状态
}
```

后续：

```go
type DrgnBackend struct {
    // Python worker / drgn API
}
```

需要注意，后续 drgn 未必适合沿用纯字符串形式的 `Execute(command)`。

更稳妥的长期设计是让上层 Operation Registry 提供带类型的操作语义，Backend 接口负责实现各自支持的能力。首版可以先使用上述简单接口。

## 7.2 crash 启动方式

正常启动：

```bash
crash --src /data/coresugar/case-001/linux-source \
  /data/coresugar/case-001/vmlinux \
  /data/coresugar/case-001/vmcore
```

如果需要独立调试文件，则由 Symbol Resolver 生成正确的参数。

不能简单假设任意 `debuginfo/` 目录都可以直接作为 crash 的某个通用命令行参数。

某些符号布局需要提供独立的调试文件；模块符号还可能需要使用 crash 的 `mod` 命令处理。

启动参数应由 Backend 根据校验后的输入生成。

### 为什么优先使用 PTY？

crash 是为交互式终端设计的工具，内部有命令提示符、readline 和输出分页等行为。

Go 可以使用：

[creack/pty](https://github.com/creack/pty)

启动：

```go
cmd := exec.Command(
    "crash",
    "--src", sourceDir,
    vmlinux,
    vmcore,
)

ptmx, err := pty.Start(cmd)
```

这只是启动代码示意，生产实现还需要进程组、环境变量、PTY 配置、权限和失败清理。

## 7.3 命令边界识别

这里要重点设计。

一种最简单的方式是：

```text
向 PTY 写入：

bt\n

然后读取数据直到：

crash>
```

但是这种实现不够可靠。

### 可能存在的问题

**问题一：输出中包含 `crash>`。**

如果输出内容本身出现该字符串，就可能误判命令结束。

**问题二：输出分页。**

例如输出中出现：

```text
--More--
```

这时 crash 可能在等待用户输入。

**问题三：命令执行非常慢。**

某些遍历内核数据结构的命令，可能长时间不返回。

**问题四：crash 发生内部异常。**

进程可能退出，或者停留在交互等待状态。

**问题五：命令进入了其他交互模式。**

例如调用嵌入式 GDB 的某些命令后，可能需要额外输入。

### 推荐的实现策略

我建议分阶段处理。

#### 第一版：PTY + 串行命令 + 提示符状态机

通过专用读取协程持续读取 PTY 数据。

执行流程：

```text
Command Executor
       │
       ▼
验证命令
       │
       ▼
写入 PTY
       │
       ▼
持续读取输出
       │
       ▼
检测预期提示符
       │
       ├── 正常 → 完成
       │
       ├── 超时 → 尝试中断
       │
       ├── EOF → 进程异常
       │
       └── 交互提示 → 拒绝或处理
```

最好在行缓冲与字节流两种层面都保存状态，因为提示符未必带有换行。

应当关闭自动分页，禁用交互式历史配置产生的意外行为，并对不同 crash 版本进行测试。

#### 第二版：评估专用命令完成标记

可以研究 crash 输入脚本或其他机制，尝试在命令执行结束后生成唯一标记。

但必须实测具体 crash 版本和命令类型。

不能直接假设所有命令都支持类似 shell 的：

```bash
command; echo UUID
```

crash 不是通用 shell，不能照搬 shell 语法。

因此，第一版先采用提示符状态机更稳妥。

## 7.4 命令超时处理

建议区分三个超时：

| 超时类型 | 建议默认值 | 作用 |
|---|---:|---|
| 初始化超时 | 120 秒 | 加载 vmcore 和调试符号 |
| 普通命令超时 | 30 秒 | 执行单条 crash 命令 |
| 重型命令超时 | 120 秒 | 执行较复杂的内核遍历 |

这些是初始配置建议，需要根据真实 vmcore 的大小调整。

超时处理：

```text
执行超时
    │
    ▼
尝试中断当前命令
    │
    ▼
等待调试器恢复提示符
    │
    ├── 成功 → READY
    │
    └── 失败 → FAILED
                  │
                  ▼
               清理进程
```

重点是：

**不能在命令超时后，立即把 Session 标记为 READY。**

如果前一条命令仍在运行，下一条命令就可能与它混合。

超时恢复必须确认交互通道重新同步。

否则应当将 Session 视为不可恢复。

## 7.5 命令安全机制

crash 支持使用 `!` 执行外部 shell 命令，还可能将未识别的输入传递给内置 GDB。

因此，`crash_exec` 绝对不能直接被视为安全的只读接口。crash 官方帮助文档对此有明确说明。[Crash Utility](https://crash-utility.github.io/help_pages/input.html?utm_source=chatgpt.com)

我建议第一版采用允许列表。

初始允许：

```text
sys
log
bt
ps
kmem
vm
files
mount
mod
dis
rd
p
struct
union
whatis
sym
help
```

但这只是候选命令集合，**不能认为允许命令名就天然安全**。

部分命令支持写入文件、调用扩展程序或其他非预期操作，仍需逐项限制参数和子功能。

推荐使用：

```go
type CommandPolicy interface {
    Validate(command string) error
}
```

校验要求：

1. 默认只允许受支持的命令形式。
2. 禁止 `!` shell 转义。
3. 禁止未知命令自动转发给 GDB。
4. 禁止输出重定向到任意文件。
5. 禁止调用外部脚本。
6. 禁止动态加载未授权的扩展。
7. 禁止任意 GDB Python 或表达式求值能力。
8. 拒绝多行输入和控制字符。
9. 限制单条命令长度。
10. 运行 crash 的用户不能拥有不必要的系统权限。

严格允许列表会影响一些高级调试能力，例如复杂的 GDB 表达式。第一版宁可限制功能，也不建议直接开放所有命令。

未来可以增加经过显式授权的高级调试模式。

---

# 8. 源码与 Debuginfo 管理

## 8.1 为什么单独设计 Source Manager？

在 Kernel Panic 分析过程中，常常需要从三个不同维度理解一个地址：

```text
内核地址
   │
   ├── 符号信息
   │     这个地址属于哪个函数？
   │
   ├── 调试信息
   │     对应哪一行源码？
   │     变量及结构体是什么？
   │
   └── 内核源码
         为什么会访问这个指针？
         这个对象在哪里初始化？
         什么时候释放？
```

因此，不能只把源码目录当作一个普通字符串参数。

应该设计一个专门负责这些关联关系的模块。

## 8.2 Symbol Resolver

建议支持三种常见输入形式。

### 模式 A：vmlinux 内含 DWARF

```text
vmlinux
  ├── .text
  ├── .symtab
  └── .debug_info
```

这是最简单的情况。

直接使用：

```bash
crash vmlinux vmcore
```

### 模式 B：独立 vmlinux.debug

```text
vmlinux
vmlinux.debug
vmcore
```

需要根据实际 ELF、Build ID 和 crash 支持的加载方式处理。

不能仅凭文件名进行匹配。

### 模式 C：发行版 Debuginfo 包

例如：

```text
/usr/lib/debug/
    └── ...
```

可能包含：

- 内核调试符号。
- 内核模块调试符号。
- Build ID 对应的调试文件。

需要由 Resolver 搜索和校验。

首版可以先支持 A 和 B，C 作为增强功能。

## 8.3 内核源码路径映射

DWARF 中记录的源码路径可能是：

```text
/build/linux-6.1.0/kernel/sched/core.c
```

但当前源码实际存放在：

```text
/data/coresugar/case-001/linux-source/kernel/sched/core.c
```

因此需要支持：

```json
{
  "source_dir": "/data/coresugar/case-001/linux-source",
  "source_mapping": [
    {
      "from": "/build/linux-6.1.0",
      "to": "/data/coresugar/case-001/linux-source"
    }
  ]
}
```

如果能够通过 crash 的源码搜索配置解决，就优先使用已有能力。

对于 MCP 的源码读取接口，也必须使用相同的路径映射关系。

**不要让 crash 和 Agent 看到两套不同的源码路径体系。**

## 8.4 内核模块符号

Kernel Panic 不一定发生在 vmlinux 自身。

例如：

```text
RIP: third_party_driver+0x128
```

该函数可能属于独立内核模块。

如果没有模块 debuginfo：

- 可能只能定位到模块名和偏移。
- 无法完整解析对应结构体。
- 无法获得准确源码行号。
- 某些反汇编与符号解析能力受到影响。

所以建议将模块符号状态单独列出：

```json
{
  "symbols": {
    "kernel": "VERIFIED",
    "modules": {
      "total": 128,
      "resolved": 120,
      "missing": 8
    }
  }
}
```

这里的统计需要基于实际模块清单和加载结果，不能只扫描目录后就认为符号已经成功解析。

首版可以按需加载模块符号。

比如 Agent 发现异常函数位于某模块，再由 Runtime 尝试解析匹配的调试符号。

这比启动时加载所有模块调试文件更容易控制。

---

# 9. Evidence Manager 设计

这是我建议重点借鉴 Flameox 的部分。

Flameox 将 Agent 的判断与底层工具产生的证据分开，并强调运行输入、工具版本、证据来源和分析限制。[GitHub](https://github.com/morluto/flameox/blob/main/docs/architecture.md?utm_source=chatgpt.com)

对于 coresugar，这个设计尤其重要。

## 9.1 为什么需要 Evidence Manager？

假设 Agent 得出结论：

> Kernel Panic 是由于 `my_driver_process()` 访问已经释放的对象导致的。

需要追问：

- 这个判断是从哪条 crash 命令得到的？
- 具体是哪一个内存地址？
- 相关寄存器和异常指令是什么？
- 证据是否足以证明对象已经释放？
- 还是仅仅发现了一个无效指针？
- 对应源码是否与崩溃内核一致？

如果没有证据管理，Agent 很容易把合理猜测写成确定性结论。

我建议采用三个层级：

| 层级 | 名称 | 含义 | 示例 |
|---|---|---|---|
| L1 | Observed | 直接观察到的事实 | `RAX=0` |
| L2 | Derived | 基于事实推导的结论 | 指令访问了地址 `0x18` |
| L3 | Hypothesis | 尚待验证的根因假设 | 指针可能因并发释放而失效 |

其中 L1 和确定性转换的 L2 可以由工具记录或计算。

L3 主要由 AI Agent 负责。

## 9.2 证据数据结构

建议使用 JSON。

```json
{
  "evidence_id": "ev-0007",
  "session_id": "kc-001",
  "command_id": "cmd-0007",

  "origin": {
    "tool": "crash",
    "command": "bt",
    "timestamp": "2026-10-12T09:01:00Z"
  },

  "result": {
    "status": "COMPLETED",
    "duration_ms": 128,
    "output_file": "outputs/cmd-0007.txt",
    "truncated": false
  },

  "integrity": {
    "sha256": "..."
  }
}
```

以上属于命令证据。

如果需要记录 Agent 对证据的解释，可以使用另一个结构：

```json
{
  "claim_id": "claim-0003",
  "type": "DERIVED",
  "statement": "The faulting instruction dereferenced address 0x18",
  "evidence": [
    "ev-0007",
    "ev-0010"
  ],
  "verification": "SUPPORTED"
}
```

第一版不需要实现复杂的 Claim 数据库。

可以先要求 Agent 在最终报告中引用 `evidence_id`。

## 9.3 证据目录

建议：

```text
evidence/
│
├── manifest.json
│
├── commands.jsonl
│
├── outputs/
│   ├── cmd-0001.txt
│   ├── cmd-0002.txt
│   └── cmd-0003.txt
│
└── reports/
    └── analysis.md
```

其中：

**manifest.json**

记录分析对象及其身份信息：

```json
{
  "case_id": "case-001",
  "session_id": "kc-001",

  "kernel": {
    "release": "6.1.0-custom",
    "architecture": "x86_64"
  },

  "artifacts": {
    "vmcore": {
      "path": "/data/coresugar/case-001/vmcore",
      "size": 17179869184
    },
    "vmlinux": {
      "path": "/data/coresugar/case-001/vmlinux"
    }
  },

  "debugger": {
    "name": "crash",
    "version": "detected-at-runtime"
  }
}
```

**commands.jsonl**

使用 JSON Lines 追加记录每一次命令：

```json
{"id":"cmd-0001","command":"sys","status":"COMPLETED"}
{"id":"cmd-0002","command":"log","status":"COMPLETED"}
{"id":"cmd-0003","command":"bt","status":"COMPLETED"}
```

实际记录中还应包含时间、耗时、输出路径、错误类型和输出完整性。

使用 JSONL 的优势是：

- 便于追加。
- 不需要数据库。
- 易于人工阅读。
- Go 可以直接解析。
- 后续可以导入其他存储系统。

## 9.4 大文件身份校验

vmcore 可能有几十 GB 或更大。

不建议每次 `open_dump` 都完整计算 vmcore 的 SHA-256。

否则可能增加明显的磁盘读取开销。

建议支持两种模式。

**快速模式：**

- 文件绝对路径。
- 文件大小。
- 修改时间。
- inode / device。
- vmcore 头部关键信息。
- 可获取的内核身份信息。

**严格模式：**

- 完整 SHA-256。
- vmlinux Build ID。
- debuginfo 身份。
- 源码 Git Commit。
- 完整输入清单。

其中快速模式的文件属性只能用于初步识别或变化检测，不能构成密码学意义上的完整性证明。

如果证据需要用于正式故障复盘，可以使用严格模式。

## 9.5 原始输出与结构化结果

我建议采用：

```text
crash 原始输出
      │
      ├── 原始证据存储
      │
      └── Output Adapter
               │
               ▼
          结构化数据
               │
               ▼
            MCP
               │
               ▼
           AI Agent
```

但第一版不必完整实现结构化解析。

建议优先解析几类稳定、常用的信息：

| crash 命令 | 结构化字段 | 优先级 |
|---|---|---|
| `sys` | Kernel、CPU、运行时间、Panic 信息 | P0 |
| `bt` | PID、Task、CPU、调用栈 | P1 |
| `log` | Panic 信息、异常日志 | P1 |
| `ps` | PID、状态、任务名称 | P1 |
| `dis` | 地址、指令、源码位置 | P2 |
| `struct` | 结构体字段 | P2 |

原因是：crash 不同版本和不同内核架构的输出格式可能变化。

过早实现复杂 Parser，会增加维护成本。

**第一版应该保证原始输出完整、执行稳定，再逐步增加结构化解析。**

---

# 10. Agent 如何进行自动分析？

这一部分决定项目是否真正有用。

但是必须明确：

**自动分析流程主要由现成 AI Agent 执行，而不是写死在 Go MCP Server 内部。**

Go Server 负责提供可靠工具。

Agent 根据工具描述、调试知识和当前证据选择操作。

## 10.1 推荐的 Agent 分析流程

```text
用户提出分析请求
         │
         ▼
Agent 调用 open_dump
         │
         ▼
获取内核版本、符号状态
         │
         ▼
调用 crash_exec("sys")
         │
         ▼
调用 crash_exec("log")
         │
         ▼
识别 Panic 类型
         │
         ├── NULL Pointer
         │
         ├── General Protection Fault
         │
         ├── Kernel BUG
         │
         ├── Soft Lockup
         │
         ├── Hard Lockup
         │
         ├── RCU Stall
         │
         ├── OOM / Panic-on-OOM
         │
         └── 其他
         │
         ▼
结合异常类型制定调查策略
         │
         ▼
执行 crash 命令
         │
         ▼
阅读相关内核源码
         │
         ▼
形成可验证的根因假设
         │
         ▼
继续执行调试命令验证假设
         │
         ▼
生成有证据引用的分析报告
```

这里不建议让 Agent 永远执行一套固定命令。

比如 RCU Stall 与 NULL Pointer Dereference 的调查重点明显不同。

应该由 Agent 根据现象选择适当的分析方向。

## 10.2 第一版建议实现一个 Agent Skill

为了保证不同 AI Agent 的分析行为尽可能一致，我建议提供独立的：

```text
skills/coresugar-analysis/SKILL.md
```

这个 Skill 不需要 Go 实现。

本质上是一份指导 Agent 如何调用 MCP 工具、如何推理和如何生成报告的说明。

### Skill 核心要求

**要求一：先收集基本信息。**

至少尝试获取：

```text
sys
log
bt
```

必要时使用：

```text
bt -a
```

**要求二：先判断直接崩溃原因，再寻找最终根因。**

例如：

```text
直接原因：
    NULL Pointer Dereference

最终根因：
    尚未确定
```

不能直接把它们等同起来。

**要求三：不能凭函数名猜测原因。**

例如：

```text
RIP: kmalloc
```

不能直接推断：

> kmalloc 存在 Bug。

可能是调用者传入了错误参数，也可能是内存已经被其他代码破坏。

**要求四：每条重要结论都要引用证据。**

例如：

```text
结论：
    CPU 2 访问了地址 0x18。

证据：
    ev-0007：异常寄存器
    ev-0010：故障指令反汇编
```

**要求五：必须说明尚未证实的内容。**

例如：

```text
当前证据可以确认发生了非法地址访问。

但还不能确认这是：
1. 对象未初始化；
2. Use After Free；
3. 其他线程错误修改指针；
4. 更早发生的内存破坏。
```

**要求六：禁止伪造工具结果。**

如果调试信息不完整，就应明确说明。

## 10.3 一个完整分析示例

假设某内核发生：

```text
BUG: kernel NULL pointer dereference
```

### Step 1：加载 vmcore

Agent：

```json
{
  "tool": "open_dump",
  "arguments": {
    "vmcore": "/data/case-001/vmcore",
    "vmlinux": "/data/case-001/vmlinux",
    "debuginfo": "/data/case-001/debuginfo",
    "source_dir": "/data/case-001/linux-source"
  }
}
```

返回：

```text
session_id = kc-001
state = READY
```

### Step 2：读取系统信息

```text
crash> sys
```

获取：

- Kernel release。
- CPU 数量。
- 系统运行时间。
- Panic 摘要。
- Dump 类型。

### Step 3：读取 Panic 日志

```text
crash> log
```

假设关键内容为：

```text
BUG: kernel NULL pointer dereference,
address: 0000000000000018

RIP: my_driver_process+0x28/0x90

RAX: 0000000000000000
```

Agent 初步判断：

> 可能存在 NULL Pointer Dereference。

此时还不能确认具体哪条指令导致异常。

### Step 4：查看调用栈

```text
crash> bt
```

假设得到：

```text
#0 machine_kexec
#1 __crash_kexec
#2 panic
#3 oops_end
#4 exc_page_fault
#5 my_driver_process
#6 worker_thread
```

Agent 可以知道崩溃发生在 `my_driver_process` 附近。

### Step 5：反汇编故障函数

```text
crash> dis -l my_driver_process
```

假设得到：

```asm
mov 0x18(%rax), %rdx
```

结合：

```text
RAX = 0
```

得到：

```text
访问地址 = RAX + 0x18
         = 0x18
```

这里才具备了证明直接异常访问的证据。

### Step 6：读取内核源码

```json
{
  "tool": "read_source",
  "arguments": {
    "session_id": "kc-001",
    "file": "drivers/example/my_driver.c",
    "start_line": 100,
    "end_line": 180
  }
}
```

假设发现：

```c
static void my_driver_process(struct my_object *obj)
{
    value = obj->data->value;
}
```

进一步结合 DWARF 类型信息，Agent 可能推断出：

```text
obj->data == NULL
```

但必须通过符号、寄存器或对应数据结构的实际内容验证，不能只根据这段源码直接下结论。

### Step 7：追踪对象生命周期

Agent 接下来分析：

```text
my_object 在哪里分配？
          │
          ▼
obj->data 在哪里初始化？
          │
          ▼
哪些路径可能将它置 NULL？
          │
          ▼
是否存在并发访问？
          │
          ▼
是否存在释放后继续访问？
```

这一步主要依赖 Agent 对源码的分析能力。

必要时继续使用 crash 读取相关内存和结构体。

### Step 8：生成报告

示例：

```markdown
# Kernel Panic 分析报告

## 一、崩溃摘要

类型：NULL Pointer Dereference

CPU：2

异常函数：my_driver_process

## 二、已确认事实

1. 异常寄存器 RAX 为 0。
2. 故障指令访问 [RAX + 0x18]。
3. 访问地址为 0x18。
4. 异常发生在 my_driver_process。

## 三、直接原因

内核执行了对无效地址 0x18 的内存访问。

## 四、根因分析

当前怀疑 obj->data 未正确初始化，
或在并发场景中被修改。

尚无充分证据证明 Use After Free。

## 五、证据

- ev-0002：Kernel log
- ev-0003：Kernel backtrace
- ev-0004：反汇编
- ev-0005：对应源码

## 六、后续建议

继续调查 obj->data 的初始化、
清理和并发访问路径。
```

这是我认为第一版应该能够完成的一个基本分析闭环。

---

# 11. Go 代码架构设计

## 11.1 项目目录

建议采用以下结构：

```text
coresugar/
│
├── cmd/
│   └── coresugar/
│       └── main.go
│
├── internal/
│   │
│   ├── mcp/
│   │   ├── server.go
│   │   ├── tools.go
│   │   └── errors.go
│   │
│   ├── runtime/
│   │   ├── runtime.go
│   │   ├── session.go
│   │   ├── manager.go
│   │   └── operation.go
│   │
│   ├── backend/
│   │   ├── backend.go
│   │   └── crash/
│   │       ├── crash.go
│   │       ├── process.go
│   │       ├── pty.go
│   │       ├── protocol.go
│   │       └── policy.go
│   │
│   ├── artifacts/
│   │   ├── artifact.go
│   │   ├── validator.go
│   │   ├── symbols.go
│   │   └── source.go
│   │
│   ├── evidence/
│   │   ├── evidence.go
│   │   ├── store.go
│   │   └── manifest.go
│   │
│   ├── parser/
│   │   ├── sys.go
│   │   ├── bt.go
│   │   └── log.go
│   │
│   └── config/
│       └── config.go
│
├── skills/
│   └── coresugar-analysis/
│       └── SKILL.md
│
├── tests/
│   ├── fixtures/
│   │   ├── sys.txt
│   │   ├── bt.txt
│   │   └── log.txt
│   │
│   └── integration/
│       └── crash_test.go
│
├── docs/
│   ├── architecture.md
│   ├── protocol.md
│   └── development.md
│
├── configs/
│   └── config.example.yaml
│
├── go.mod
├── Makefile
├── README.md
└── LICENSE
```

其中最核心的是三个包：

```text
internal/mcp
internal/runtime
internal/backend/crash
```

其他功能围绕这三层逐步扩展。

## 11.2 Go 依赖

推荐：

| 功能 | 依赖 |
|---|---|
| MCP | `github.com/modelcontextprotocol/go-sdk` |
| PTY | `github.com/creack/pty` |
| 子进程 | Go 标准库 `os/exec` |
| 并发控制 | Go 标准库 `sync`、`context` |
| JSON | Go 标准库 `encoding/json` |
| ELF 解析 | Go 标准库 `debug/elf` |
| DWARF | Go 标准库 `debug/dwarf` |
| 日志 | Go 标准库 `log/slog` |
| 哈希计算 | Go 标准库 `crypto/sha256` |
| 配置文件 | YAML 解析库，可选 |

Go 官方 MCP SDK 已支持 Server、Tool 等基础能力，适合作为项目的主要 MCP 实现。[GitHub](https://github.com/modelcontextprotocol/go-sdk?utm_source=chatgpt.com)

第一版尽量减少第三方依赖。

尤其不需要引入：

- Gin。
- GORM。
- Redis。
- PostgreSQL。
- 大型 Agent 框架。

## 11.3 Runtime 接口

建议先定义服务内部接口：

```go
type Runtime interface {
    OpenDump(
        ctx context.Context,
        req OpenDumpRequest,
    ) (*OpenDumpResponse, error)

    Execute(
        ctx context.Context,
        req ExecuteRequest,
    ) (*ExecuteResponse, error)

    GetSession(
        ctx context.Context,
        id string,
    ) (*SessionInfo, error)

    CloseDump(
        ctx context.Context,
        id string,
    ) error
}
```

MCP Server 只负责：

```text
MCP 请求
   │
   ▼
参数反序列化
   │
   ▼
调用 Runtime
   │
   ▼
返回 MCP 响应
```

不要在 MCP handler 中直接调用：

```go
exec.Command("crash", ...)
```

否则后续的 Session 管理、CLI 和自动化测试都会与 MCP 耦合。

## 11.4 为什么建议保留 CLI？

虽然项目主要面向 Agent，但我建议保留少量 CLI 能力。

例如：

```bash
coresugar check
```

检查：

- crash 是否安装。
- 版本信息。
- 调试器是否可执行。
- PTY 是否可用。

以及：

```bash
coresugar validate \
  --vmcore /data/case-001/vmcore \
  --vmlinux /data/case-001/vmlinux \
  --source /data/case-001/linux-source \
  --debuginfo /data/case-001/debuginfo
```

这样可以在不启动 AI Agent 的情况下验证输入。

第一版 CLI 不需要复制全部 MCP 功能。

主要服务于环境检查、开发调试和集成测试。

---

# 12. macOS 与 Linux 服务器的部署方案

## 12.1 推荐的启动方式

Linux 服务器部署：

```text
/opt/coresugar/
│
├── bin/
│   └── coresugar
│
└── config.yaml
```

在 macOS 上：

```bash
ssh kernel-debug@linux-server \
  /opt/coresugar/bin/coresugar serve
```

该命令通过 SSH 建立 stdio 通道。

AI Agent 通过 MCP stdio 与远程进程通信。

这意味着：

- 不需要开启额外 HTTP 端口。
- 不需要开发鉴权服务器。
- 可以利用已有 SSH 身份认证。
- 不需要把大体积 vmcore 传输到 macOS。
- crash 与 vmcore 都位于 Linux 服务器。

### 特别注意

SSH 必须避免向 stdout 写入与 MCP 无关的内容。

例如：

```text
Welcome to Ubuntu
Last login: ...
```

这些内容如果进入 MCP stdio 通道，可能破坏协议解析。

因此建议使用非交互式 SSH，并确保登录脚本不输出额外信息。

同时，Go 进程的日志应写到 stderr。

## 12.2 MCP 客户端配置示例

不同 Agent 的 MCP 配置格式有所区别。

概念上可以采用：

```json
{
  "mcpServers": {
    "coresugar": {
      "command": "ssh",
      "args": [
        "-T",
        "-o",
        "BatchMode=yes",
        "kernel-debug@linux-server",
        "/opt/coresugar/bin/coresugar",
        "serve"
      ]
    }
  }
}
```

这是通用 MCP stdio 配置示意，实际配置文件位置及字段需要按使用的客户端适配。

其中：

```bash
-T
```

表示不分配 SSH 终端。

这不会阻碍 MCP Server 自己在 Linux 内部创建用于控制 crash 的 PTY。

两者是不同的终端连接。

## 12.3 配置文件

当前 v0.1 实现使用 JSON 配置，字段和默认值以 [configs/config.example.json](configs/config.example.json) 及 [README.md](README.md) 为准。下方 YAML 是设计阶段的配置示意。

建议使用：

```yaml
server:
  transport: stdio

crash:
  executable: /usr/bin/crash
  startup_timeout: 120s
  command_timeout: 30s
  max_command_timeout: 120s

session:
  max_sessions: 2
  idle_timeout: 30m

output:
  max_response_bytes: 65536
  max_capture_bytes: 16777216

paths:
  allowed_roots:
    - /data/coresugar

evidence:
  enabled: true
  directory: /data/coresugar-evidence
```

这些参数属于初始建议值，需要在测试中根据真实 vmcore 调整。

对于第一版，最多同时开启 1～2 个 crash 进程比较合适。

---

# 13. 错误处理设计

为了让 Agent 在执行失败时知道下一步应该做什么，建议设计统一错误模型。

例如：

```json
{
  "status": "ERROR",
  "error": {
    "code": "SYMBOL_MISMATCH",
    "message": "The supplied vmlinux does not match vmcore",
    "recoverable": false,
    "suggestion": "Provide the matching vmlinux and debuginfo"
  }
}
```

推荐的错误码：

| 错误码 | 含义 | 处理方式 |
|---|---|---|
| `INVALID_ARGUMENT` | 请求参数错误 | 修正参数 |
| `ARTIFACT_NOT_FOUND` | 输入文件不存在 | 检查路径 |
| `ARTIFACT_INVALID` | 文件格式异常 | 更换文件 |
| `SYMBOL_MISMATCH` | 符号不匹配 | 提供正确符号 |
| `SOURCE_MISMATCH` | 源码不匹配 | 提供正确源码 |
| `CRASH_NOT_AVAILABLE` | crash 不可用 | 安装或修复环境 |
| `CRASH_START_FAILED` | 调试器启动失败 | 检查错误输出 |
| `COMMAND_REJECTED` | 命令被安全策略拒绝 | 使用允许的命令 |
| `COMMAND_TIMEOUT` | 命令执行超时 | 调整命令或超时 |
| `OUTPUT_LIMIT_EXCEEDED` | 输出超过采集上限 | 缩小查询范围 |
| `SESSION_NOT_FOUND` | 会话不存在 | 重新打开 |
| `SESSION_BUSY` | 会话正在执行其他命令 | 稍后重试 |
| `SESSION_FAILED` | 会话无法恢复 | 重新打开 |
| `INTERNAL_ERROR` | 服务内部错误 | 查看服务日志 |

需要区分两种失败：

**工具级失败：**

crash 命令被执行，但返回错误。

例如：

```text
invalid task address
```

这不一定意味着 Session 已损坏。

**会话级失败：**

crash 进程异常退出、PTY 连接断开，或发生无法重新同步的超时。

这种情况才需要将 Session 标记为 `FAILED`。

另外，MCP 通信层的错误与业务错误也应分开。

对 Agent 可恢复的业务错误，尽量返回带明确错误码和修复建议的 Tool 结果，而不是让整个 MCP 连接失败。

---

# 14. 安全设计

虽然本项目第一版只在自己的 Linux 服务器上使用，但 vmcore 分析涉及两个重要风险：

1. vmcore 可能包含敏感内存数据。
2. crash/GDB 的某些功能可以执行外部命令。

因此，第一版就应该建立基本的安全边界。

## 14.1 权限隔离

推荐使用专门的 Linux 用户：

```text
kernel-debug
```

其权限：

- 对 vmcore 具有只读权限。
- 对 vmlinux、debuginfo 和源码具有只读权限。
- 对证据目录具有写入权限。
- 没有 sudo 权限。
- 不属于不必要的特权用户组。
- 无权修改生产业务目录。

首版不建议以 root 身份运行 MCP Server。

## 14.2 文件访问限制

设置：

```yaml
paths:
  allowed_roots:
    - /data/coresugar
```

所有 Agent 提供的文件路径必须经过验证。

除了检查 `../`，还需要防范符号链接绕过。

对安全要求更高的环境，应采用基于目录文件描述符的路径解析方式，例如 Linux `openat2()` 的相关解析约束，而不是仅依赖字符串路径前缀判断。

## 14.3 进程资源限制

除了命令超时，还建议逐步支持：

- 最大并发 Session 数量。
- crash 进程内存限制。
- CPU 使用限制。
- 输出大小限制。
- 最大打开文件数。
- 子进程清理。
- 磁盘证据配额。

如果后续要分析不可信来源的 vmcore，建议将 crash 放进独立容器或受限执行环境。

因为 crash、GDB 和相关解析库本身也可能存在文件解析漏洞。

**PTY、命令允许列表和非 root 用户能够降低风险，但不能替代真正的进程隔离。**

## 14.4 AI 数据隐私

vmcore 可能包含：

- 用户数据。
- 程序内存。
- API Key。
- 密钥。
- 网络数据。
- 其他敏感内核内存。

使用云端 AI Agent 时，crash 命令的输出可能会被发送给模型提供商进行处理。

因此，必须明确区分：

```text
vmcore 原始文件
    保留在 Linux Server

crash 分析输出
    经 MCP 发送给 AI Agent
```

**不上传 vmcore 文件本身，并不代表不存在数据泄露风险。**

建议第一版：

- 默认不允许任意范围的内存转储。
- 对 `rd` 等命令设置合理的读取数量限制。
- 不自动传输大块原始内存。
- 支持敏感信息脱敏。
- 保留必要的命令审计记录。

---

# 15. 测试方案

我认为这个项目的测试应分成两部分：

**工程可靠性测试**和**内核崩溃分析能力测试**。

两者不能混为一谈。

## 15.1 单元测试

这些测试可以直接在 macOS 上运行。

| 测试模块 | 测试内容 |
|---|---|
| MCP | Tool 参数解析与返回值 |
| Runtime | Session 创建、查询和关闭 |
| Command Policy | 非法命令拒绝 |
| Output Parser | crash 输出解析 |
| Evidence | 证据写入与读取 |
| Source Manager | 源码读取与路径校验 |
| Error Handling | 错误码及恢复建议 |
| Concurrency | 多 Session 并发、单 Session 串行 |

其中，Crash Backend 应当允许注入 Fake Debugger。

例如：

```go
type FakeBackend struct {
    Responses map[string]string
}
```

这样不用真实 vmcore 也能测试：

```text
open_dump
   ↓
crash_exec("sys")
   ↓
crash_exec("bt")
   ↓
get_evidence
   ↓
close_dump
```

## 15.2 Crash 进程集成测试

这些测试在第 3.1 节记录的 Linux 测试环境 `root@linux-test.example.test` 上运行，使用 `/data/coresugar` 中已准备的 vmcore、vmlinux、内核源码等资料。

测试对象应包括：

| 测试场景 | 预期结果 |
|---|---|
| 正常 vmcore | 可以成功初始化 |
| 错误 vmlinux | 返回明确的符号错误或校验警告 |
| 缺少 debuginfo | 根据实际符号情况处理 |
| 不存在的 vmcore | 拒绝加载 |
| crash 命令错误 | Session 不应自动失效 |
| crash 进程退出 | Session 转为 FAILED |
| 命令长时间执行 | 超时并正确恢复或关闭 |
| 输出超过限制 | 截断响应并保留可用证据 |
| 同时请求多条命令 | 串行或明确拒绝并发 |
| MCP 连接中断 | 正确清理子进程 |

特别建议开发一个测试用的伪交互程序，模拟：

```text
crash>
```

并人为制造：

- 延迟输出。
- 分段输出。
- 没有换行的提示符。
- 输出中包含 `crash>`。
- 命令永不返回。
- 子进程突然退出。

这样可以在没有真实 vmcore 的情况下，对 PTY 控制逻辑进行压力测试。

## 15.3 内核崩溃案例测试

建议第一版准备至少 5 个测试案例。

| 案例 | 崩溃类型 | 测试目标 |
|---|---|---|
| Case 1 | NULL Pointer Dereference | 正确定位故障指令 |
| Case 2 | Kernel BUG | 定位 BUG 触发位置 |
| Case 3 | General Protection Fault | 分析异常寄存器和调用栈 |
| Case 4 | Soft Lockup | 分析 CPU 和任务执行状态 |
| Case 5 | 内存破坏 | 验证 Agent 是否能合理表达不确定性 |

这些测试应该尽量有已知的真实原因。

例如，可以使用测试内核和受控故障注入构造 vmcore。

对于每个案例，分别评价：

1. 能否正确识别崩溃类型。
2. 能否正确定位崩溃现场。
3. 能否解释直接异常原因。
4. 能否找到或合理推断根因。
5. 是否引用有效的调试证据。
6. 是否出现没有依据的确定性判断。

注意，**第一版不应把“成功生成分析报告”作为成功分析根因的标准**。

如果报告没有实质证据，就不能算根因分析成功。

---

# 16. 开发阶段与里程碑

建议将开发拆成四个阶段，而不是一次性实现全部架构。

## Phase 1：Crash Backend

**目标：Go 程序能够可靠地操作 crash。**

实现：

- 启动 crash。
- 加载 vmcore 和 vmlinux。
- 创建 PTY。
- 识别提示符。
- 执行单条命令。
- 获取命令输出。
- 支持超时和退出。

交付形式：

```bash
coresugar debug \
  --vmcore /data/vmcore \
  --vmlinux /data/vmlinux
```

进入一个简单的 Go 命令行调试环境。

这一阶段先不接 MCP。

### 验收标准

能够连续执行：

```text
sys
log
bt
ps
kmem -i
```

每条命令的输出均不会与上一条或下一条混合。

异常情况下能够正确关闭进程。

**这是整个项目最重要的工程基础。**

## Phase 2：MCP 接入

**目标：Claude Code / Codex 可以通过 MCP 使用 crash。**

实现：

- MCP Server。
- `open_dump`。
- `crash_exec`。
- `get_session`。
- `close_dump`。
- SSH stdio 接入。

交付形式：

用户直接对 Agent 说：

> 打开 `/data/case-001/vmcore`，执行 crash，分析异常堆栈。

Agent 能够成功调用 MCP Tool。

### 验收标准

Agent 可以连续执行至少 10 条 crash 命令，服务端正常管理 Session。

## Phase 3：源码与证据

**目标：支持有证据支撑的内核分析。**

实现：

- `read_source`。
- `search_source`。
- `get_evidence`。
- 输入文件校验。
- Source Mapping。
- Evidence Store。
- Agent Skill。

### 验收标准

Agent 可以：

1. 找到异常函数。
2. 反汇编对应指令。
3. 读取相关源码。
4. 根据源码和内核状态形成判断。
5. 输出附带证据编号的报告。

## Phase 4：可靠性与真实案例验证

**目标：从技术 Demo 转为真正可用于排障的工具。**

实现：

- 完善命令安全策略。
- 完善 PTY 状态机。
- 增加超时恢复机制。
- 提升符号与源码匹配检查。
- 完善测试案例。
- 处理大型 vmcore。
- 优化输出分页与证据保存。
- 完善用户文档。

### 验收标准

使用至少 5 个已知原因的 Kernel Panic 案例进行评测。

形成实际测试记录，能够明确说明：

- 哪些故障类型可以较好分析。
- 哪些故障类型分析不充分。
- 哪些判断依赖额外的源码或符号信息。
- 哪些问题无法仅通过 vmcore 得出确定性结论。

---

# 17. 后续技术演进

第一版完成后，再考虑以下能力。

## 17.1 drgn Backend

增加：

```text
AI Agent
    │
    ▼
coresugar MCP
    │
    ▼
Analysis Runtime
    │
    ├── Crash Backend
    │
    └── drgn Backend
```

crash 擅长成熟的 Kernel Panic 调试命令。

drgn 更适合通过 Python 编写复杂的内核对象检查逻辑。

两者可以互补。

不建议第一版就同时接入。

## 17.2 结构化内核分析 Tool

当项目积累一定经验后，可以增加更高层的操作。

例如：

```text
get_panic_summary
get_exception_context
get_cpu_backtraces
inspect_task
inspect_kernel_object
```

这些操作底层仍然可以调用 crash。

但返回给 Agent 的是更稳定的结构化结果。

例如：

```json
{
  "exception": {
    "type": "PAGE_FAULT",
    "cpu": 2,
    "task": "kworker/2:1",
    "instruction_pointer": "0xffffffff81234567",
    "fault_address": "0x18"
  }
}
```

不过，这些字段必须有真实的底层证据来源。

不能让 Parser 根据日志中的几个关键字就武断填充字段。

## 17.3 专项分析 Skills

建议后续按照故障类型增加：

```text
skills/
│
├── kernel-panic-analysis/
├── null-pointer-analysis/
├── memory-corruption-analysis/
├── deadlock-analysis/
├── soft-lockup-analysis/
├── rcu-stall-analysis/
└── oom-analysis/
```

其中最有价值的可能是：

- 内存破坏。
- Use After Free。
- RCU Stall。
- Soft Lockup。
- 死锁。

这些问题往往需要多轮调试，比较适合 Agent 辅助调查。

## 17.4 自动关联内核版本与源码

后续可以支持：

```text
Kernel Release
      │
      ▼
Build ID / 配置 / 模块信息
      │
      ▼
确定内核构建版本
      │
      ▼
定位源码仓库及 Git Commit
      │
      ▼
自动建立源码映射
```

但不能只根据：

```text
uname -r
```

就自动选取上游 Linux 源码。

对于经过发行版补丁或内部定制的内核，这样很容易匹配错误。

因此，源码自动匹配应该是后续能力，第一版仍然建议用户显式提供。

---

# 18. 工程风险与技术取舍

| 风险 | 严重程度 | 我的建议 |
|---|---|---|
| crash 交互控制不稳定 | 高 | 第一阶段集中解决 |
| crash 命令存在执行外部程序能力 | 高 | 严格命令策略、低权限运行 |
| vmcore 和符号不匹配 | 高 | 明确校验并向 Agent 暴露状态 |
| AI 将崩溃位置误判为根因 | 高 | 强制区分事实、推导和假设 |
| 内核源码版本不一致 | 高 | 记录源码身份与校验状态 |
| 大型 vmcore 初始化慢 | 中 | 保留 crash 长连接会话 |
| 输出过大导致 Token 浪费 | 中 | 分页、限额和证据存储 |
| 多 Session 并发 | 中 | 第一版限制并发数量 |
| crash 版本兼容 | 中 | 用集成测试覆盖目标版本 |
| 缺少真实故障测试数据 | 高 | 尽早建立可验证案例集 |
| drgn 集成复杂度 | 低优先级 | 第一版暂不实现 |

我认为其中最值得优先解决的是前三项。

因为它们决定了 MCP 能否稳定、安全地提供可靠的底层分析数据。

如果调试器执行本身不稳定，Agent 再强也无法可靠推理。

---

# 19. 最终方案决策

将第一版的技术决策汇总如下。

| 技术维度 | 最终方案 |
|---|---|
| 项目定位 | AI Agent 的 Linux Kernel Crash 分析工具框架 |
| 参考架构 | Flameox |
| 开发语言 | **Go** |
| AI Agent | **Claude Code / Codex** |
| MCP SDK | **官方 Go MCP SDK** |
| MCP Transport | **stdio over SSH** |
| 开发环境 | macOS |
| 运行环境 | Linux Server |
| 调试器 | **crash** |
| 调试器控制 | Go `os/exec` + PTY |
| 进程模型 | 每个 Session 一个 crash 进程 |
| 并发模型 | 同 Session 串行，不同 Session 可并行 |
| 输入 | vmcore、vmlinux、debuginfo、kernel source |
| 源码访问 | 独立 MCP Tool |
| 数据存储 | 本地文件 + JSONL |
| 数据库 | 不需要 |
| 工具安全 | 只读命令策略 + 低权限执行 |
| 自动分析 | Agent + Skill |
| 分析报告 | Agent 生成，要求引用调试证据 |
| drgn | 后续扩展 |
| Web UI | 暂不考虑 |

---

# 20. 我建议的第一版最小架构

上面是完整方案，但从开发投入来看，**我不建议一开始就把所有模块全部写出来。**

如果由我来开发，第一版会收敛到以下结构：

```text
                    Claude Code / Codex
                            │
                            │ MCP
                            ▼
                  ┌───────────────────┐
                  │  coresugar MCP    │
                  │       (Go)        │
                  └─────────┬─────────┘
                            │
                            ▼
                  ┌───────────────────┐
                  │   Crash Runtime   │
                  │                   │
                  │  Session Manager  │
                  │  Command Executor │
                  │  Evidence Store   │
                  └─────────┬─────────┘
                            │
                            ▼
                  ┌───────────────────┐
                  │   Crash Backend   │
                  │                   │
                  │   os/exec + PTY   │
                  └─────────┬─────────┘
                            │
                            ▼
                    Linux crash/GDB
                            │
                ┌───────────┼──────────┐
                ▼           ▼          ▼
              vmcore     vmlinux    debuginfo
                                       │
                                  Kernel Source
```

再给 Agent 配置一个 `coresugar-analysis` Skill。

这就足以验证项目最关键的价值。

### 最核心的五个模块

| 模块 | 为什么必须有 |
|---|---|
| MCP Server | 让现成 AI Agent 接入 |
| Session Manager | 保持 crash 进程及调试状态 |
| Crash Backend | 真正操作 crash 工具 |
| Source Manager | 让 Agent 能够分析内核实现 |
| Evidence Manager | 让分析结果有依据、可复查 |

### 最重要的三个设计原则

**第一：不要在 Go 服务中重复开发 AI 推理能力。**

崩溃分类、根因假设、下一步调查计划，以及报告撰写，都由 Agent 完成。

**第二：不要重新实现 crash 的调试能力。**

Go 主要负责操作执行、权限约束、会话管理、信息整理和证据保存。

**第三：不要让 Agent 仅凭调用栈就断定根因。**

从第一版开始，就要求所有关键分析结论有实际证据支持。

---

## 我的整体评价

这个项目最适合采用 **Flameox 式的轻量 Runtime + MCP + Backend Adapter + Evidence** 架构。

但有一个地方要与 Flameox 区分开：

**coresugar 的核心对象不是一次性的性能分析任务，而是一个持续存在的内核调试会话。**

因此，在工程优先级上：

**Crash Session Manager > Crash Backend 可靠性 > MCP 接口 > 证据管理 > 自动化分析策略。**

MCP 本身反而是比较简单的部分。

如果第一阶段能够稳定地实现：

```text
open_dump
    ↓
crash_exec("sys")
    ↓
crash_exec("log")
    ↓
crash_exec("bt")
    ↓
crash_exec("dis ...")
    ↓
read_source(...)
    ↓
Agent 分析
```

就已经具备了项目的核心价值。

**建议先按这个设计实现 v0.1，不引入 Python、drgn、数据库、Web UI 或独立 Agent 框架。**

等 crash 交互、文件匹配和证据链经过真实 vmcore 验证后，再决定需要增加哪些高级分析能力。

### 参考资料

- [Flameox — GitHub](https://github.com/morluto/flameox)
- [Flameox — Architecture](https://github.com/morluto/flameox/blob/main/docs/architecture.md)
- [Flameox — Runtime Safety](https://github.com/morluto/flameox/blob/main/docs/runtime-safety.md)
- [Linux crash — GitHub](https://github.com/crash-utility/crash)
- [Linux crash — White Paper](https://crash-utility.github.io/crash_whitepaper.html)
- [Model Context Protocol — Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- [creack/pty — GitHub](https://github.com/creack/pty)
