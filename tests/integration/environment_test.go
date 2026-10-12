package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"coresugar/internal/core"
)

// TestEnvironment 验证显式配置的 Linux 环境及真实输入：crash 就绪、
// 校验状态与盘点结果一致、构建不符时返回错误，以及不存在的 vmcore 被拒绝。
// 未提供 CORESUGAR_TEST_CONFIG 时仅跳过远程测试，不代表验收通过。
func TestEnvironment(t *testing.T) {
	path := os.Getenv("CORESUGAR_TEST_CONFIG")
	if path == "" {
		t.Skip("set CORESUGAR_TEST_CONFIG to run real Linux integration checks")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read configuration: %v", err)
		return
	}
	var config struct {
		core.Inputs
		Crash          string `json:"crash"`
		Directory      string `json:"directory"`
		ExpectedStatus string `json:"expected_status"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Errorf("decode configuration: %v", err)
		return
	}
	if config.ExpectedStatus == "" {
		t.Errorf("expected_status must be explicit")
		return
	}
	check := core.Check(context.Background(), config.Crash, config.Directory)
	if !check.Ready {
		t.Errorf("environment unavailable: %+v", check)
	}
	result := core.Validate(config.Inputs)
	if result.Status != config.ExpectedStatus {
		t.Errorf("status = %s, want %s: %+v", result.Status, config.ExpectedStatus, result)
	}
	if result.Status == "MISMATCH" && len(result.Errors) == 0 {
		t.Errorf("mismatched inputs must report a reason")
	}
	config.Inputs.VMCore = filepath.Join(t.TempDir(), "vmcore-not-provided")
	if result := core.Validate(config.Inputs); result.Status != "INVALID" {
		t.Errorf("absent input accepted: %+v", result)
	}
}
