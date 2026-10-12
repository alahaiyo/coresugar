package core

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func fakeDebugger(t *testing.T, mode string) *Debugger {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestDebuggerProcess$")
	command.Env = append(os.Environ(), "CORESUGAR_FAKE_CRASH="+mode)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	debugger, err := startDebugger(ctx, command)
	if err != nil {
		t.Errorf("start fake crash: %v", err)
		return nil
	}
	t.Cleanup(debugger.Close)
	return debugger
}

// TestDebuggerProcess 是可控的交互式子进程入口：模拟分段提示符、输出中的提示符、
// 普通错误、进程退出、无限等待和大输出；未设置环境变量时不启动子进程模式。
func TestDebuggerProcess(t *testing.T) {
	mode := os.Getenv("CORESUGAR_FAKE_CRASH")
	if mode == "" {
		return
	}
	if mode == "startup-exit" {
		os.Exit(1)
	}
	fmt.Print("cr")
	time.Sleep(time.Millisecond)
	fmt.Print("ash> ")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		command := scanner.Text()
		if strings.HasPrefix(command, "gdb printf ") {
			start := strings.Index(command, "CORESUGAR_")
			end := strings.Index(command[start:], "\\n")
			fmt.Printf("\n%s\ncrash> ", command[start:start+end])
			continue
		}
		switch command {
		case "sys":
			if mode == "large" {
				fmt.Println(strings.Repeat("kernel-data", 4096))
				fmt.Print("crash> ")
				continue
			}
			if mode == "command-exit" {
				os.Exit(1)
			}
			if mode == "hang" {
				for {
					time.Sleep(time.Second)
				}
			}
			fmt.Print("KERNEL: fixture\ntext contains crash> ")
			time.Sleep(5 * time.Millisecond)
			fmt.Print("within output\nCPUS: 96\n")
		case "log":
			fmt.Println("PANIC: divide error")
		case "bt":
			fmt.Println("bt: invalid task address")
		default:
			fmt.Println("accepted")
		}
		fmt.Print("crash> ")
	}
	os.Exit(0)
}

// TestDebugger 验证单一进程连续执行、提示符混在输出中的边界、普通错误不损坏会话、
// 未允许命令的拒绝，以及重复关闭后进程退出。
func TestDebugger(t *testing.T) {
	debugger := fakeDebugger(t, "normal")
	if debugger == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, command := range []string{"sys", "log", "bt", "sys"} {
		output, err := debugger.Execute(ctx, command)
		if (err != nil) != (command == "bt") {
			t.Errorf("%s: %v", command, err)
		}
		if strings.Contains(output, "CORESUGAR_") {
			t.Errorf("transport marker leaked: %q", output)
		}
		if command == "sys" && !strings.Contains(output, "CPUS: 96") {
			t.Errorf("incomplete sys: %q", output)
		}
		if command == "log" && strings.Contains(output, "CPUS") {
			t.Errorf("outputs mixed: %q", output)
		}
	}
	if _, err := debugger.Execute(ctx, "!touch /tmp/forbidden"); err == nil {
		t.Errorf("external command accepted")
	}
	if !debugger.Alive() {
		t.Errorf("ordinary error/rejection destroyed session")
	}
	debugger.Close()
	debugger.Close()
	if debugger.Alive() {
		t.Errorf("closed process still alive")
	}
}

// TestDebuggerFailure 验证启动失败、执行时进程退出和命令超时均返回错误并清理进程。
func TestDebuggerFailure(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestDebuggerProcess$")
	command.Env = append(os.Environ(), "CORESUGAR_FAKE_CRASH=startup-exit")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if debugger, err := startDebugger(ctx, command); err == nil {
		debugger.Close()
		t.Errorf("startup failure accepted")
	}
	for _, mode := range []string{"command-exit", "hang"} {
		t.Run(mode, func(t *testing.T) {
			debugger := fakeDebugger(t, mode)
			if debugger == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if _, err := debugger.Execute(ctx, "sys"); err == nil {
				t.Errorf("command failure accepted")
			}
			if debugger.Alive() {
				t.Errorf("failed process not cleaned up")
			}
		})
	}
}

// TestDebuggerCaptureLimit 验证无效采集限额被拒绝，大输出达到上限时保留部分输出并清理进程。
func TestDebuggerCaptureLimit(t *testing.T) {
	debugger := fakeDebugger(t, "large")
	if debugger == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := debugger.ExecuteLimited(ctx, "sys", 0); err == nil {
		t.Errorf("invalid capture limit accepted")
	}
	output, err := debugger.ExecuteLimited(ctx, "sys", 1024)
	if err == nil || len(output) == 0 || len(output) > 1024 {
		t.Errorf("capture limit: length=%d error=%v", len(output), err)
	}
	if debugger.Alive() {
		t.Errorf("capture overflow left process running")
	}
}
