package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	SourceMappings      map[string]string `json:"source_mappings,omitempty"`
	Crash               string            `json:"crash"`
	AllowedRoots        []string          `json:"allowed_roots"`
	EvidenceDir         string            `json:"evidence_dir"`
	MaxSessions         int               `json:"max_sessions"`
	IdleTimeoutMS       int               `json:"idle_timeout_ms"`
	StartupTimeoutMS    int               `json:"startup_timeout_ms"`
	CommandTimeoutMS    int               `json:"command_timeout_ms"`
	MaxCommandTimeoutMS int               `json:"max_command_timeout_ms"`
	MaxResponseBytes    int               `json:"max_response_bytes"`
	MaxCaptureBytes     int               `json:"max_capture_bytes"`
}

func DefaultConfig() Config {
	return Config{Crash: "crash", AllowedRoots: []string{"."}, EvidenceDir: "./evidence", MaxSessions: 2, IdleTimeoutMS: 1800000, StartupTimeoutMS: 120000, CommandTimeoutMS: 30000, MaxCommandTimeoutMS: 120000, MaxResponseBytes: 65536, MaxCaptureBytes: 16777216}
}

type SessionInfo struct {
	ID           string     `json:"session_id"`
	Status       string     `json:"status"`
	Validation   Validation `json:"validation"`
	LastActivity time.Time  `json:"last_activity"`
}

type session struct {
	info       SessionInfo
	debugger   *Debugger
	execute    sync.Mutex
	sourceOps  int
	cancel     context.CancelFunc
	reaperDone chan struct{}
}

type Runtime struct {
	config     Config
	logMu      sync.Mutex
	mu         sync.Mutex
	sessions   map[string]*session
	ctx        context.Context
	cancel     context.CancelFunc
	reaperDone chan struct{}
}

func NewRuntime(config Config) (*Runtime, error) {
	if config.Crash == "" || config.EvidenceDir == "" || len(config.AllowedRoots) == 0 || config.MaxSessions < 1 || config.MaxSessions > 2 || config.IdleTimeoutMS <= 0 || config.StartupTimeoutMS <= 0 || config.CommandTimeoutMS <= 0 || config.MaxCommandTimeoutMS < config.CommandTimeoutMS || config.MaxCommandTimeoutMS > 600000 || config.MaxResponseBytes < 1024 || config.MaxResponseBytes > 65536 || config.MaxCaptureBytes < config.MaxResponseBytes || config.MaxCaptureBytes > 16<<20 {
		return nil, fmt.Errorf("INVALID_ARGUMENT: invalid server configuration")
	}
	roots := append([]string(nil), config.AllowedRoots...)
	config.AllowedRoots = roots
	mappings := make(map[string]string, len(config.SourceMappings))
	for prefix, target := range config.SourceMappings {
		mappings[prefix] = target
	}
	config.SourceMappings = mappings
	for prefix, target := range config.SourceMappings {
		if !filepath.IsAbs(prefix) || filepath.IsAbs(target) {
			return nil, fmt.Errorf("INVALID_ARGUMENT: source mapping requires an absolute prefix and relative target")
		}
		for _, part := range strings.Split(target, string(filepath.Separator)) {
			if part == ".." {
				return nil, fmt.Errorf("INVALID_ARGUMENT: source mapping escapes root")
			}
		}
	}
	for index, root := range config.AllowedRoots {
		path, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("allowed root is not a directory")
		}
		config.AllowedRoots[index] = path
	}
	evidenceDir, err := filepath.Abs(config.EvidenceDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		return nil, fmt.Errorf("evidence directory: %w", err)
	}
	evidenceDir, err = filepath.EvalSymlinks(evidenceDir)
	if err != nil {
		return nil, err
	}
	config.EvidenceDir = evidenceDir
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &Runtime{config: config, sessions: make(map[string]*session), ctx: ctx, cancel: cancel, reaperDone: make(chan struct{})}
	go runtime.reapLoop()
	return runtime, nil
}

func (runtime *Runtime) pathAllowed(path string) error {
	if path == "" {
		return fmt.Errorf("INVALID_ARGUMENT: empty path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return fmt.Errorf("ARTIFACT_NOT_FOUND: %w", err)
	}
	for _, root := range runtime.config.AllowedRoots {
		relative, err := filepath.Rel(root, resolved)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return nil
		}
	}
	return fmt.Errorf("COMMAND_REJECTED: path is outside allowed roots")
}

func (runtime *Runtime) Open(ctx context.Context, inputs Inputs) (SessionInfo, error) {
	for _, path := range []string{inputs.VMCore, inputs.VMLinux, inputs.SourceDir} {
		if err := runtime.pathAllowed(path); err != nil {
			return SessionInfo{}, err
		}
	}
	if inputs.DebugInfo != "" {
		if err := runtime.pathAllowed(inputs.DebugInfo); err != nil {
			return SessionInfo{}, err
		}
	}
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return SessionInfo{}, err
	}
	id := "cs-" + hex.EncodeToString(identifier)
	loadCtx, cancel := context.WithTimeout(ctx, time.Duration(runtime.config.StartupTimeoutMS)*time.Millisecond)
	stop := context.AfterFunc(runtime.ctx, cancel)
	defer stop()
	defer cancel()
	item := &session{info: SessionInfo{ID: id, Status: "LOADING", LastActivity: time.Now().UTC()}, cancel: cancel}
	runtime.mu.Lock()
	if runtime.ctx.Err() != nil {
		runtime.mu.Unlock()
		return SessionInfo{}, fmt.Errorf("SESSION_FAILED: runtime closed")
	}
	active := 0
	for _, existing := range runtime.sessions {
		if existing.info.Status != "CLOSED" && existing.info.Status != "FAILED" {
			active++
		}
	}
	if active >= runtime.config.MaxSessions {
		runtime.mu.Unlock()
		return SessionInfo{}, fmt.Errorf("SESSION_BUSY: session limit reached")
	}
	runtime.sessions[id] = item
	runtime.mu.Unlock()
	debugger, err := StartDebugger(loadCtx, runtime.config.Crash, inputs)
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err != nil {
		delete(runtime.sessions, id)
		return SessionInfo{}, err
	}
	if item.info.Status == "CLOSED" || runtime.ctx.Err() != nil {
		debugger.Close()
		delete(runtime.sessions, id)
		return SessionInfo{}, fmt.Errorf("SESSION_FAILED: closed during initialization")
	}
	item.debugger = debugger
	item.info.Status = "READY"
	item.info.Validation = debugger.Validation
	item.info.LastActivity = time.Now().UTC()
	if err := runtime.prepareEvidence(item.info, debugger.Startup); err != nil {
		debugger.Close()
		delete(runtime.sessions, id)
		return SessionInfo{}, fmt.Errorf("EVIDENCE_WRITE_FAILED: %w", err)
	}
	return item.info, nil
}

func (runtime *Runtime) Get(id string) (SessionInfo, error) {
	runtime.mu.Lock()
	item := runtime.sessions[id]
	if item == nil {
		runtime.mu.Unlock()
		return SessionInfo{}, fmt.Errorf("SESSION_NOT_FOUND: %s", id)
	}
	var failed *Debugger
	if item.info.Status == "READY" && !item.debugger.Alive() {
		item.info.Status = "FAILED"
		failed = item.debugger
		item.debugger = nil
	}
	item.info.LastActivity = time.Now().UTC()
	info := item.info
	runtime.mu.Unlock()
	if failed != nil {
		failed.Close()
	}
	return info, nil
}

type ExecRequest struct {
	SessionID      string `json:"session_id"`
	Command        string `json:"command"`
	TimeoutMS      int    `json:"timeout_ms,omitempty"`
	MaxOutputBytes int    `json:"max_output_bytes,omitempty"`
}

type ExecResult struct {
	SessionID        string `json:"session_id"`
	Command          string `json:"command"`
	Output           string `json:"output"`
	Status           string `json:"status"`
	EvidenceID       string `json:"evidence_id,omitempty"`
	Truncated        bool   `json:"truncated"`
	CaptureTruncated bool   `json:"capture_truncated"`
	Error            string `json:"error,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
}

func (runtime *Runtime) Exec(ctx context.Context, request ExecRequest) (ExecResult, error) {
	if err := ValidateCommand(request.Command); err != nil {
		return ExecResult{}, err
	}
	// Module loading must use an explicit symbol file under allowed_roots;
	// never let crash search arbitrary host directories for a matching module.
	fields := strings.Fields(request.Command)
	if fields[0] == "mod" && len(fields) == 4 {
		path, err := filepath.EvalSymlinks(fields[3])
		if err != nil {
			return ExecResult{}, fmt.Errorf("ARTIFACT_NOT_FOUND: %w", err)
		}
		if err := runtime.pathAllowed(path); err != nil {
			return ExecResult{}, err
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return ExecResult{}, fmt.Errorf("INVALID_ARGUMENT: module symbols must be a regular file")
		}
		fields[3] = path
		request.Command = strings.Join(fields, " ")
		if err := ValidateCommand(request.Command); err != nil {
			return ExecResult{}, err
		}
	}
	timeout := request.TimeoutMS
	if timeout == 0 {
		timeout = runtime.config.CommandTimeoutMS
	}
	limit := request.MaxOutputBytes
	if limit == 0 {
		limit = runtime.config.MaxResponseBytes
	}
	if timeout < 1 || timeout > runtime.config.MaxCommandTimeoutMS || limit < 1 || limit > runtime.config.MaxResponseBytes {
		return ExecResult{}, fmt.Errorf("INVALID_ARGUMENT: execution limits out of range")
	}
	runtime.mu.Lock()
	item := runtime.sessions[request.SessionID]
	if item == nil {
		runtime.mu.Unlock()
		return ExecResult{}, fmt.Errorf("SESSION_NOT_FOUND: %s", request.SessionID)
	}
	if item.info.Status != "READY" {
		status := item.info.Status
		runtime.mu.Unlock()
		return ExecResult{}, fmt.Errorf("SESSION_%s: session is not ready", status)
	}
	if !item.execute.TryLock() {
		runtime.mu.Unlock()
		return ExecResult{}, fmt.Errorf("SESSION_BUSY: another command is executing")
	}
	defer item.execute.Unlock()
	item.info.Status = "BUSY"
	debugger := item.debugger
	runtime.mu.Unlock()
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	stop := context.AfterFunc(runtime.ctx, cancel)
	defer stop()
	started := time.Now()
	output, err := debugger.ExecuteLimited(execCtx, request.Command, runtime.config.MaxCaptureBytes)
	result := ExecResult{SessionID: request.SessionID, Command: request.Command, Output: output, Status: "SUCCESS"}
	if err != nil {
		result.Status = "ERROR"
		result.Error = err.Error()
		result.ErrorCode = "SESSION_FAILED"
		if strings.HasPrefix(err.Error(), "COMMAND_ERROR:") {
			result.ErrorCode = "COMMAND_ERROR"
		} else if errors.Is(err, context.DeadlineExceeded) {
			result.ErrorCode = "COMMAND_TIMEOUT"
		} else if errors.Is(err, context.Canceled) {
			result.ErrorCode = "COMMAND_CANCELLED"
		} else if strings.Contains(err.Error(), "capture limit") {
			result.ErrorCode = "OUTPUT_LIMIT_EXCEEDED"
		}
		result.CaptureTruncated = strings.Contains(err.Error(), "capture limit")

	}
	evidenceID, saveErr := runtime.saveEvidence(request.SessionID, "crash", request.Command, output, result.Status, result.Error, result.CaptureTruncated, started)
	if saveErr != nil {
		result.Status = "ERROR"
		result.Error = "EVIDENCE_WRITE_FAILED: " + saveErr.Error()
		result.ErrorCode = "EVIDENCE_WRITE_FAILED"
	} else {
		result.EvidenceID = evidenceID
	}
	if len(result.Output) > limit {
		result.Output = result.Output[:limit]
		result.Truncated = true
	}
	runtime.mu.Lock()
	if item.info.Status != "CLOSED" {
		item.info.Status = "READY"
		if !debugger.Alive() {
			item.info.Status = "FAILED"
			item.debugger = nil
		}
	}
	item.info.LastActivity = time.Now().UTC()
	runtime.mu.Unlock()
	return result, nil
}

func (runtime *Runtime) CloseSession(id string) (SessionInfo, error) {
	runtime.mu.Lock()
	item := runtime.sessions[id]
	if item == nil {
		runtime.mu.Unlock()
		return SessionInfo{}, fmt.Errorf("SESSION_NOT_FOUND: %s", id)
	}
	item.info.Status = "CLOSED"
	item.cancel()
	debugger := item.debugger
	item.debugger = nil
	item.info.LastActivity = time.Now().UTC()
	info := item.info
	runtime.mu.Unlock()
	if debugger != nil {
		debugger.Close()
	}
	return info, nil
}

func (runtime *Runtime) Close() {
	runtime.cancel()
	<-runtime.reaperDone
	runtime.mu.Lock()
	ids := make([]string, 0, len(runtime.sessions))
	for id := range runtime.sessions {
		ids = append(ids, id)
	}
	runtime.mu.Unlock()
	for _, id := range ids {
		runtime.CloseSession(id)
	}
}
