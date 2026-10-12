package main

import (
	"bytes"
	"encoding/json"
	"runtime"
	"testing"

	"coresugar/internal/core"
)

// TestRun 验证命令入口：无子命令、未知子命令、非法参数、额外参数及检查失败的退出码。
func TestRun(t *testing.T) {
	for _, args := range [][]string{nil, {"unavailable"}, {"check", "--unknown"}, {"check", "unexpected"}} {
		var output, diagnostics bytes.Buffer
		if code := run(args, nil, &output, &diagnostics); code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if output.Len() != 0 {
			t.Errorf("invalid arguments wrote result: %s", &output)
		}
	}
	var output, diagnostics bytes.Buffer
	code := run([]string{"check", "--crash", "/not-installed/crash", "--directory", t.TempDir()}, nil, &output, &diagnostics)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	var result core.CheckResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Errorf("result JSON: %v", err)
	}
	if result.OS != runtime.GOOS || result.Ready {
		t.Errorf("unexpected result: %+v", result)
	}
}
