package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceRuntime(t *testing.T) (*Runtime, string, string) {
	t.Helper()
	runtime := testRuntime(t)
	if runtime == nil {
		return nil, "", ""
	}
	id := attachDebugger(t, runtime, "normal", "cs-test-session")
	if id == "" {
		return nil, "", ""
	}
	root := runtime.config.AllowedRoots[0]
	path := filepath.Join(root, "kernel/sched/core.c")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Errorf("source directories: %v", err)
		return nil, "", ""
	}
	writeFixture(t, path, []byte("/* 调度测试 -kernel_sched_tick */\nvoid kernel_sched_tick(void)\n{\n    trace_kernel_sched_tick();\n}\n"))
	runtime.mu.Lock()
	runtime.sessions[id].info.Validation.Inputs.SourceDir = root
	runtime.mu.Unlock()
	return runtime, id, root
}

// TestReadSource 验证带行号读取、文件末尾、内容身份、源码状态和证据保存，
// 并拒绝无效行范围、目录外文件、绝对路径、越界符号链接和目录符号链接。
func TestReadSource(t *testing.T) {
	runtime, id, root := sourceRuntime(t)
	if runtime == nil {
		return
	}
	result, err := runtime.ReadSource(SourceRequest{SessionID: id, File: "kernel/sched/core.c", StartLine: 2, EndLine: 100})
	if err != nil || result.EndLine != 5 || !strings.Contains(result.Content, "2: void kernel_sched_tick") || result.SourceStatus != "PARTIAL" || result.EvidenceID == "" {
		t.Errorf("source result: %+v %v", result, err)
	}
	if _, err := runtime.GetEvidence(EvidenceRequest{SessionID: id, EvidenceID: result.EvidenceID}); err != nil {
		t.Errorf("source evidence: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.c")
	writeFixture(t, outside, []byte("outside source\n"))
	if err := os.Symlink(outside, filepath.Join(root, "outside-link.c")); err != nil {
		t.Errorf("file symlink: %v", err)
		return
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "outside-directory")); err != nil {
		t.Errorf("directory symlink: %v", err)
		return
	}
	for _, request := range []SourceRequest{{id, "kernel/sched/core.c", 0, 5}, {id, "kernel/sched/core.c", 3, 2}, {id, "kernel/sched/core.c", 1, 301}, {id, "kernel/sched/core.c", 99, 100}, {id, "../outside.c", 1, 2}, {id, outside, 1, 2}, {id, "outside-link.c", 1, 2}, {id, "outside-directory/outside.c", 1, 2}, {id, "unavailable.c", 1, 2}} {
		if _, err := runtime.ReadSource(request); err == nil {
			t.Errorf("invalid source request accepted: %+v", request)
		}
	}
	runtime.config.SourceMappings = map[string]string{"/build/linux": ""}
	if _, err := runtime.ReadSource(SourceRequest{id, "/build/linux/kernel/sched/core.c", 1, 2}); err != nil {
		t.Errorf("explicit path mapping: %v", err)
	}
	runtime.CloseSession(id)
	if _, err := runtime.ReadSource(SourceRequest{id, "kernel/sched/core.c", 1, 2}); err == nil {
		t.Errorf("closed session source accepted")
	}
}

// TestSearchSource 验证文字搜索、中文及以连字符开头的关键词、无匹配、结果数量截断，
// 拒绝越界目录/非法参数，并验证取消会终止搜索。
func TestSearchSource(t *testing.T) {
	runtime, id, _ := sourceRuntime(t)
	if runtime == nil {
		return
	}
	for _, pattern := range []string{"kernel_sched_tick", "调度测试", "-kernel_sched_tick", "not_in_kernel_source"} {
		result, err := runtime.SearchSource(context.Background(), SearchRequest{SessionID: id, Pattern: pattern, Path: "kernel", MaxResults: 10})
		if err != nil {
			t.Errorf("search %q: %v", pattern, err)
			continue
		}
		if pattern == "not_in_kernel_source" && len(result.Matches) != 0 {
			t.Errorf("unexpected matches: %+v", result)
		}
		if pattern != "not_in_kernel_source" && len(result.Matches) == 0 {
			t.Errorf("no matches for %q", pattern)
		}
		if result.EvidenceID == "" {
			t.Errorf("search evidence not saved")
		}
	}
	result, err := runtime.SearchSource(context.Background(), SearchRequest{SessionID: id, Pattern: "kernel_sched_tick", MaxResults: 1})
	if err != nil || len(result.Matches) != 1 || !result.Truncated {
		t.Errorf("result limit: %+v %v", result, err)
	}
	for _, request := range []SearchRequest{{SessionID: id, Pattern: ""}, {SessionID: id, Pattern: "kernel_sched_tick", Path: ".."}, {SessionID: id, Pattern: "kernel_sched_tick", MaxResults: 101}} {
		if _, err := runtime.SearchSource(context.Background(), request); err == nil {
			t.Errorf("invalid search request accepted: %+v", request)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.SearchSource(ctx, SearchRequest{SessionID: id, Pattern: "kernel_sched_tick"}); err == nil {
		t.Errorf("cancelled search succeeded")
	}
}
