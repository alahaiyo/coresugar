package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRuntime(t *testing.T) *Runtime {
	t.Helper()
	config := DefaultConfig()
	config.AllowedRoots = []string{t.TempDir()}
	config.EvidenceDir = t.TempDir()
	runtime, err := NewRuntime(config)
	if err != nil {
		t.Errorf("new runtime: %v", err)
		return nil
	}
	t.Cleanup(runtime.Close)
	return runtime
}

func attachDebugger(t *testing.T, runtime *Runtime, mode, id string) string {
	t.Helper()
	debugger := fakeDebugger(t, mode)
	if debugger == nil {
		return ""
	}
	runtime.mu.Lock()
	runtime.sessions[id] = &session{info: SessionInfo{ID: id, Status: "READY", LastActivity: time.Now()}, debugger: debugger, cancel: func() {}}
	runtime.mu.Unlock()
	return id
}

// TestNewRuntime 验证配置默认值可用、会话数量/超时/输出大小异常被拒绝，
// 以及不存在的允许目录不能作为服务配置。
func TestNewRuntime(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"sessions-out-of-range", func(config *Config) { config.MaxSessions = 3 }},
		{"negative-timeout", func(config *Config) { config.CommandTimeoutMS = -1 }},
		{"response-too-large", func(config *Config) { config.MaxResponseBytes = 1 << 20 }},
		{"capture-smaller-than-response", func(config *Config) { config.MaxCaptureBytes = 1024 }},
		{"root-absent", func(config *Config) {
			config.AllowedRoots = []string{filepath.Join(t.TempDir(), "unavailable-artifacts")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := DefaultConfig()
			tc.change(&config)
			if runtime, err := NewRuntime(config); err == nil {
				runtime.Close()
				t.Errorf("invalid configuration accepted")
			}
		})
	}
}

// TestPathAllowed 验证目录内文件可用、目录外文件及越界符号链接被拒绝；不使用字符串前缀判断。
func TestPathAllowed(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	inside := filepath.Join(runtime.config.AllowedRoots[0], "vmcore")
	writeFixture(t, inside, []byte("fixture"))
	outside := filepath.Join(t.TempDir(), "vmlinux")
	writeFixture(t, outside, []byte("fixture"))
	if err := runtime.pathAllowed(inside); err != nil {
		t.Errorf("inside root: %v", err)
	}
	if err := runtime.pathAllowed(outside); err == nil {
		t.Errorf("outside root accepted")
	}
	link := filepath.Join(runtime.config.AllowedRoots[0], "outside-symbols")
	if err := os.Symlink(outside, link); err != nil {
		t.Errorf("create link: %v", err)
		return
	}
	if err := runtime.pathAllowed(link); err == nil {
		t.Errorf("escaping symlink accepted")
	}
}

// TestRuntimeExec 验证查询与关闭、输出截断、命令错误不会损坏会话、非法命令和执行限额被拒绝。
func TestRuntimeExec(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	id := attachDebugger(t, runtime, "normal", "cs-test-session")
	if id == "" {
		return
	}
	result, err := runtime.Exec(context.Background(), ExecRequest{SessionID: id, Command: "sys", MaxOutputBytes: 8})
	if err != nil || !result.Truncated || len(result.Output) != 8 {
		t.Errorf("bounded result: %+v, %v", result, err)
	}
	result, err = runtime.Exec(context.Background(), ExecRequest{SessionID: id, Command: "bt"})
	if err != nil || result.Status != "ERROR" {
		t.Errorf("debugger error not reported: %+v %v", result, err)
	}
	info, err := runtime.Get(id)
	if err != nil || info.Status != "READY" {
		t.Errorf("ordinary error destroyed session: %+v %v", info, err)
	}
	for _, request := range []ExecRequest{{SessionID: id, Command: "!id"}, {SessionID: id, Command: "sys", TimeoutMS: -1}, {SessionID: id, Command: "sys", MaxOutputBytes: 1 << 20}, {SessionID: "cs-not-found", Command: "sys"}} {
		if _, err := runtime.Exec(context.Background(), request); err == nil {
			t.Errorf("invalid request accepted: %+v", request)
		}
	}
	if _, err := runtime.CloseSession(id); err != nil {
		t.Errorf("close: %v", err)
	}
	if _, err := runtime.CloseSession(id); err != nil {
		t.Errorf("repeat close: %v", err)
	}
	if _, err := runtime.Exec(context.Background(), ExecRequest{SessionID: id, Command: "sys"}); err == nil {
		t.Errorf("closed session accepted command")
	}
}

// TestRuntimeConcurrentClose 验证执行中查询返回 BUSY、同会话并发被拒绝，关闭会话中断执行且状态保持 CLOSED。
func TestRuntimeConcurrentClose(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	id := attachDebugger(t, runtime, "hang", "cs-test-session")
	if id == "" {
		return
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runtime.Exec(context.Background(), ExecRequest{SessionID: id, Command: "sys"})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		info, _ := runtime.Get(id)
		if info.Status == "BUSY" {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("execution never started")
			return
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := runtime.Exec(context.Background(), ExecRequest{SessionID: id, Command: "log"}); err == nil || !strings.Contains(err.Error(), "BUSY") {
		t.Errorf("concurrent request: %v", err)
	}
	runtime.CloseSession(id)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Errorf("close did not interrupt execution")
	}
	info, _ := runtime.Get(id)
	if info.Status != "CLOSED" {
		t.Errorf("status = %s", info.Status)
	}
}

// TestRuntimeTimeout 验证请求层的执行超时返回稳定错误码和失败证据，
// 会话转为 FAILED，子进程关闭，后续请求不能误用损坏会话。
func TestRuntimeTimeout(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	id := attachDebugger(t, runtime, "hang", "cs-timeout-session")
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := runtime.Exec(ctx, ExecRequest{SessionID: id, Command: "sys", TimeoutMS: 50})
	if err != nil || result.Status != "ERROR" || result.ErrorCode != "COMMAND_TIMEOUT" || result.EvidenceID == "" {
		t.Errorf("timeout result: %+v %v", result, err)
	}
	info, err := runtime.Get(id)
	if err != nil || info.Status != "FAILED" {
		t.Errorf("failed session: %+v %v", info, err)
	}
	if _, err := runtime.Exec(ctx, ExecRequest{SessionID: id, Command: "log"}); err == nil {
		t.Errorf("failed session accepted command")
	}
}
