package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type Debugger struct {
	command    *exec.Cmd
	terminal   *os.File
	chunks     chan []byte
	exited     chan struct{}
	closed     chan struct{}
	closeOnce  sync.Once
	mu         sync.Mutex
	Startup    string
	Validation Validation
}

func StartDebugger(ctx context.Context, executable string, inputs Inputs) (*Debugger, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("crash requires Linux")
	}
	validation := Validate(inputs)
	if validation.Status == "INVALID" || validation.Status == "MISMATCH" {
		return nil, fmt.Errorf("%s: %s", validation.Status, strings.Join(validation.Errors, "; "))
	}
	command := exec.Command(executable, "--no_scroll", "--no_crashrc", "--src", validation.Inputs.SourceDir, validation.Inputs.VMLinux, validation.Inputs.VMCore)
	debugger, err := startDebugger(ctx, command)
	if err != nil {
		return nil, err
	}
	debugger.Validation = validation
	return debugger, nil
}

func startDebugger(ctx context.Context, command *exec.Cmd) (*Debugger, error) {
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 40, Cols: 256})
	if err != nil {
		return nil, err
	}
	debugger := &Debugger{command: command, terminal: terminal, chunks: make(chan []byte, 64), exited: make(chan struct{}), closed: make(chan struct{})}
	go func() { command.Wait(); syscall.Kill(-command.Process.Pid, syscall.SIGKILL); close(debugger.exited) }()
	go func() {
		defer close(debugger.chunks)
		buffer := make([]byte, 8192)
		for {
			count, err := terminal.Read(buffer)
			if count > 0 {
				chunk := append([]byte(nil), buffer[:count]...)
				select {
				case debugger.chunks <- chunk:
				case <-debugger.closed:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	startup, err := debugger.readUntil(ctx, "", 16<<20)
	if err == nil && strings.Contains(startup, "kernel version inconsistency") {
		err = fmt.Errorf("SYMBOL_MISMATCH: crash reports inconsistent kernel build versions")
	}
	if err != nil {
		debugger.Close()
		return nil, fmt.Errorf("crash initialization: %w", err)
	}
	debugger.Startup = startup
	if _, err = debugger.executeRaw(ctx, "set scroll off", 16<<20); err != nil {
		debugger.Close()
		return nil, fmt.Errorf("crash handshake: %w", err)
	}
	return debugger, nil
}

func (debugger *Debugger) readUntil(ctx context.Context, marker string, limit int) (string, error) {
	var result bytes.Buffer
	for {
		select {
		case chunk, ok := <-debugger.chunks:
			if !ok {
				return result.String(), fmt.Errorf("crash process exited")
			}
			result.Write(chunk)
			if result.Len() > limit {
				return result.String()[:limit], fmt.Errorf("capture limit exceeded")
			}
			tail := result.Bytes()
			if len(tail) > 512 {
				tail = tail[len(tail)-512:]
			}
			text := strings.ReplaceAll(string(tail), "\r", "")
			if strings.HasSuffix(text, "crash> ") && (marker == "" || strings.Contains(text, "\n"+marker+"\n")) {
				return strings.ReplaceAll(result.String(), "\r", ""), nil
			}
		case <-ctx.Done():
			return result.String(), ctx.Err()
		case <-debugger.closed:
			return result.String(), fmt.Errorf("crash session closed")
		}
	}
}

func (debugger *Debugger) executeRaw(ctx context.Context, command string, limit int) (string, error) {
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return "", err
	}
	marker := "CORESUGAR_" + hex.EncodeToString(identifier)
	boundary := "gdb printf \"\\n" + marker + "\\n\""
	if _, err := fmt.Fprintln(debugger.terminal, command+"\n"+boundary); err != nil {
		return "", err
	}
	output, err := debugger.readUntil(ctx, marker, limit)
	output = strings.ReplaceAll(output, "\r", "")
	output = strings.SplitN(output, "\n"+marker+"\n", 2)[0]
	// The PTY/readline may echo queued input; these are transport, not command output.
	output = strings.ReplaceAll(output, boundary+"\n", "")
	output = strings.TrimPrefix(output, command+"\n")
	output = strings.TrimSuffix(output, "crash> ")
	return output, err
}

func (debugger *Debugger) Execute(ctx context.Context, command string) (string, error) {
	return debugger.ExecuteLimited(ctx, command, 16<<20)
}

func (debugger *Debugger) ExecuteLimited(ctx context.Context, command string, captureLimit int) (string, error) {
	debugger.mu.Lock()
	defer debugger.mu.Unlock()
	if err := ValidateCommand(command); err != nil {
		return "", err
	}
	if captureLimit < 1024 || captureLimit > 16<<20 {
		return "", fmt.Errorf("INVALID_ARGUMENT: capture limit must be 1024..16777216")
	}
	output, err := debugger.executeRaw(ctx, command, captureLimit)
	if err != nil {
		if ctx.Err() != nil {
			syscall.Kill(-debugger.command.Process.Pid, syscall.SIGINT)
			// Interrupt delivery alone cannot prove GDB state is safe; close and require a fresh session.
		}
		debugger.Close()
	}
	if err == nil && (strings.HasPrefix(strings.TrimSpace(output), strings.Fields(command)[0]+":") || strings.HasPrefix(strings.TrimSpace(output), "No symbol ") || strings.HasPrefix(strings.TrimSpace(output), "Cannot access memory at address ")) {
		return output, fmt.Errorf("COMMAND_ERROR: %s", strings.TrimSpace(output))
	}
	return output, err
}

func (debugger *Debugger) Close() {
	debugger.closeOnce.Do(func() {
		close(debugger.closed)
		// pty.Start gives the child its own session/process group; include descendants.
		if debugger.command.Process != nil {
			select {
			case <-debugger.exited:
			default:
				syscall.Kill(-debugger.command.Process.Pid, syscall.SIGKILL)
			}
		}
		debugger.terminal.Close()
		select {
		case <-debugger.exited:
		case <-time.After(5 * time.Second):
		}
	})
}

func (debugger *Debugger) Alive() bool {
	select {
	case <-debugger.exited:
		return false
	case <-debugger.closed:
		return false
	default:
		return true
	}
}
