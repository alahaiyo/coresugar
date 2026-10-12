package main

import (
	"bufio"
	"context"
	"coresugar/internal/mcpserver"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"coresugar/internal/core"
)

func run(args []string, input io.Reader, output, diagnostics io.Writer) int {
	if len(args) == 0 || (args[0] != "check" && args[0] != "validate" && args[0] != "debug" && args[0] != "serve") {
		fmt.Fprintln(diagnostics, "usage: coresugar check | validate | debug | serve [options]")
		return 2
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	maxResponse := flags.Int("max-output-bytes", 65536, "maximum command response bytes (1..65536)")
	maxCapture := flags.Int("max-capture-bytes", 16<<20, "maximum captured command bytes (1024..16777216)")
	idleTimeout := flags.Duration("idle-timeout", 30*time.Minute, "session idle timeout")
	maxSessions := flags.Int("max-sessions", 2, "maximum active sessions (1..2)")
	configPath := flags.String("config", "", "server configuration JSON")
	allowedRoot := flags.String("allowed-root", ".", "allowed artifact root")
	evidenceDir := flags.String("evidence-dir", "./evidence", "evidence directory")
	var inputs core.Inputs
	if args[0] != "check" {
		flags.StringVar(&inputs.VMCore, "vmcore", "", "vmcore path")
		flags.StringVar(&inputs.VMLinux, "vmlinux", "", "vmlinux path")
		flags.StringVar(&inputs.DebugInfo, "debuginfo", "", "optional debug symbols")
		flags.StringVar(&inputs.SourceDir, "source-dir", "", "kernel source directory")
	}
	commandTimeout := flags.Duration("command-timeout", 30*time.Second, "crash command timeout, up to 120s")
	startupTimeout := flags.Duration("startup-timeout", 120*time.Second, "crash initialization timeout")
	crash := flags.String("crash", "crash", "crash executable")
	directory := flags.String("directory", ".", "readable test directory")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *maxResponse < 1 || *maxResponse > 65536 || *maxCapture < 1024 || *maxCapture > 16<<20 || *maxCapture < *maxResponse {
		fmt.Fprintln(diagnostics, "output limits out of range")
		return 2
	}
	if *commandTimeout <= 0 || *commandTimeout > 120*time.Second || *startupTimeout <= 0 {
		fmt.Fprintln(diagnostics, "timeouts must be positive; command timeout must not exceed 120s")
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(diagnostics, "unexpected positional arguments")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if args[0] == "serve" {
		config := core.DefaultConfig()
		if *configPath != "" {
			file, err := os.Open(*configPath)
			if err != nil {
				fmt.Fprintln(diagnostics, err)
				return 1
			}
			decoder := json.NewDecoder(file)
			decoder.DisallowUnknownFields()
			err = decoder.Decode(&config)
			if err == nil {
				var trailing any
				if decoder.Decode(&trailing) != io.EOF {
					err = fmt.Errorf("configuration contains trailing data")
				}
			}
			file.Close()
			if err != nil {
				fmt.Fprintln(diagnostics, err)
				return 2
			}
		}
		flags.Visit(func(item *flag.Flag) {
			switch item.Name {
			case "idle-timeout":
				config.IdleTimeoutMS = int(idleTimeout.Milliseconds())
			case "max-sessions":
				config.MaxSessions = *maxSessions
			case "max-output-bytes":
				config.MaxResponseBytes = *maxResponse
			case "max-capture-bytes":
				config.MaxCaptureBytes = *maxCapture
			case "crash":
				config.Crash = *crash
			case "allowed-root":
				config.AllowedRoots = []string{*allowedRoot}
			case "evidence-dir":
				config.EvidenceDir = *evidenceDir
			case "command-timeout":
				config.CommandTimeoutMS = int(commandTimeout.Milliseconds())
			case "startup-timeout":
				config.StartupTimeoutMS = int(startupTimeout.Milliseconds())
			}
		})
		runtime, err := core.NewRuntime(config)
		if err != nil {
			fmt.Fprintln(diagnostics, err)
			return 2
		}
		if err := mcpserver.Run(ctx, runtime); err != nil && ctx.Err() == nil {
			fmt.Fprintln(diagnostics, err)
			return 1
		}
		return 0
	}
	if args[0] == "debug" {
		startupCtx, cancel := context.WithTimeout(ctx, *startupTimeout)
		debugger, err := core.StartDebugger(startupCtx, *crash, inputs)
		cancel()
		if err != nil {
			fmt.Fprintln(diagnostics, err)
			return 1
		}
		defer debugger.Close()
		fmt.Fprintln(diagnostics, debugger.Startup)
		scanner := bufio.NewScanner(input)
		lines := make(chan string)
		scanError := make(chan error, 1)
		go func() {
			defer close(lines)
			for scanner.Scan() {
				select {
				case lines <- scanner.Text():
				case <-ctx.Done():
					scanError <- ctx.Err()
					return
				}
			}
			scanError <- scanner.Err()
		}()
		failed := false
		for {
			var command string
			select {
			case <-ctx.Done():
				fmt.Fprintln(diagnostics, ctx.Err())
				return 1
			case line, ok := <-lines:
				if !ok {
					if err := <-scanError; err != nil {
						fmt.Fprintln(diagnostics, err)
						return 1
					}
					if failed {
						return 1
					}
					return 0
				}
				command = line
			}
			if command == "quit" {
				break
			}
			ctx, cancel := context.WithTimeout(ctx, *commandTimeout)
			text, err := debugger.ExecuteLimited(ctx, command, *maxCapture)
			cancel()
			truncated := len(text) > *maxResponse
			if truncated {
				text = text[:*maxResponse]
			}
			result := map[string]any{"command": command, "output": text, "truncated": truncated}
			if err != nil {
				result["error"] = err.Error()
				failed = true
			}
			if err := json.NewEncoder(output).Encode(result); err != nil {
				fmt.Fprintln(diagnostics, err)
				return 1
			}
			if !debugger.Alive() {
				break
			}
		}
		if failed {
			return 1
		}
		return 0
	}
	if args[0] == "validate" {
		result := core.Validate(inputs)
		if err := json.NewEncoder(output).Encode(result); err != nil {
			fmt.Fprintln(diagnostics, err)
			return 1
		}
		if result.Status == "INVALID" || result.Status == "MISMATCH" {
			return 1
		}
		return 0
	}
	result := core.Check(context.Background(), *crash, *directory)
	if err := json.NewEncoder(output).Encode(result); err != nil {
		fmt.Fprintln(diagnostics, err)
		return 1
	}
	if !result.Ready {
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
