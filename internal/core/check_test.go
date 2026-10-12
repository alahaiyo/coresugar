package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func versionProgram(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crash-version")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o600); err != nil {
		t.Errorf("create version program: %v", err)
		return ""
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Errorf("make executable: %v", err)
	}
	return path
}

// TestCheck 验证环境检查：Linux 可用环境、缺失工具、工具退出失败、
// 无法识别版本、目录不存在、普通文件误作目录，以及 macOS 不启动 crash。
func TestCheck(t *testing.T) {
	directory := t.TempDir()
	valid := versionProgram(t, "echo 'crash 7.3.0'\n")
	failure := versionProgram(t, "exit 1\n")
	unrecognized := versionProgram(t, "echo 'not a debugger'\n")
	for _, tc := range []struct {
		name, platform, executable, directory, errorText string
		ready                                            bool
	}{
		{"linux-ready", "linux", valid, directory, "", true},
		{"executable-absent", "linux", filepath.Join(directory, "crash-not-installed"), directory, "executable", false},
		{"version-failure", "linux", failure, directory, "version", false},
		{"version-unrecognized", "linux", unrecognized, directory, "unrecognized", false},
		{"directory-absent", "linux", valid, filepath.Join(directory, "unavailable-dumps"), "directory", false},
		{"directory-is-file", "linux", valid, valid, "directory", false},
		{"macos-platform", "darwin", failure, directory, "requires Linux", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := check(context.Background(), tc.platform, "amd64", tc.executable, tc.directory)
			if result.Ready != tc.ready {
				t.Errorf("ready = %v, want %v", result.Ready, tc.ready)
			}
			if tc.errorText != "" && !strings.Contains(strings.Join(result.Errors, " "), tc.errorText) {
				t.Errorf("errors = %v, want %q", result.Errors, tc.errorText)
			}
			if tc.ready && result.Version != "crash 7.3.0" {
				t.Errorf("version = %q", result.Version)
			}
		})
	}
}

// TestCheckCancellation 验证调用已取消时，不报告可运行的 crash 环境。
func TestCheckCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := check(ctx, "linux", "amd64", versionProgram(t, "echo 'crash 7.3.0'\n"), t.TempDir())
	if result.Ready {
		t.Errorf("cancelled check reported ready")
	}
}
