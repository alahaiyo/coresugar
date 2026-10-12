package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type CheckResult struct {
	OS           string   `json:"os"`
	Architecture string   `json:"architecture"`
	Crash        string   `json:"crash"`
	Version      string   `json:"version,omitempty"`
	Directory    string   `json:"directory"`
	Ready        bool     `json:"ready"`
	Errors       []string `json:"errors,omitempty"`
}

func Check(ctx context.Context, crash, directory string) CheckResult {
	return check(ctx, runtime.GOOS, runtime.GOARCH, crash, directory)
}

func check(ctx context.Context, platform, architecture, crash, directory string) CheckResult {
	result := CheckResult{OS: platform, Architecture: architecture, Crash: crash, Directory: directory}
	if platform != "linux" {
		result.Errors = append(result.Errors, "crash requires Linux; develop and run unit tests locally, run crash checks on Linux")
		return result
	}
	file, err := os.Open(directory)
	if err == nil {
		_, err = file.ReadDir(1)
		file.Close()
		// An empty directory is readable too.
		if err != nil && errors.Is(err, io.EOF) {
			err = nil
		}
	}
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("directory: %v", err))
	}
	path, err := exec.LookPath(crash)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("crash executable: %v", err))
	} else {
		result.Crash = path
		versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		output, err := exec.CommandContext(versionCtx, path, "--version").CombinedOutput()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("crash version: %v", err))
		} else {
			for _, line := range strings.Split(string(output), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "crash ") {
					result.Version = strings.TrimSpace(line)
					break
				}
			}
			if result.Version == "" {
				result.Errors = append(result.Errors, "crash version output is unrecognized")
			}
		}
	}
	result.Ready = len(result.Errors) == 0
	return result
}
