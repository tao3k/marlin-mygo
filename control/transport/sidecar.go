// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package transport

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	ErrSidecarBinaryRequired = errors.New("transport: sidecar binary path is required")
	ErrSidecarStartTimeout   = errors.New("transport: timed out waiting for sidecar READY handshake")
	ErrSidecarNonLoopback    = errors.New("transport: sidecar advertised non-loopback address")
	ErrSidecarExitedEarly    = errors.New("transport: sidecar exited before READY handshake")
)

// SidecarConfig configures spawning and supervising a local Marlin Rust host sidecar.
// Ephemeral Bearer tokens are generated via crypto/rand and delivered strictly via
// process environment variables (never CLI arguments).
type SidecarConfig struct {
	BinaryPath      string
	Args            []string
	BindAddr        string // defaults to "127.0.0.1:0"
	Token           string // defaults to fresh 32-byte random hex token
	PublisherID     string
	PublisherKeyHex string
	ExtraEnv        []string
	StartTimeout    time.Duration // defaults to 5s
	ShutdownTimeout time.Duration // defaults to 2s
	Stderr          io.Writer
	OnExit          func(error)
}

// SidecarSupervisor owns the lifecycle of one local Rust Authority sidecar process.
type SidecarSupervisor struct {
	cmd             *exec.Cmd
	address         string
	token           string
	shutdownTimeout time.Duration

	done    chan struct{}
	mu      sync.RWMutex
	exitErr error
	stopped bool
}

// StartSidecar spawns the local sidecar process, injects its ephemeral one-time
// Bearer token strictly through environment variables, waits for `READY <host:port>`,
// and enforces that the bound address is loopback before returning.
func StartSidecar(ctx context.Context, cfg SidecarConfig) (*SidecarSupervisor, error) {
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		return nil, ErrSidecarBinaryRequired
	}
	bindAddr := cfg.BindAddr
	if bindAddr == "" {
		bindAddr = "127.0.0.1:0"
	}
	token := cfg.Token
	if token == "" {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, fmt.Errorf("transport: generate ephemeral sidecar token: %w", err)
		}
		token = hex.EncodeToString(raw[:])
	}
	startTimeout := cfg.StartTimeout
	if startTimeout <= 0 {
		startTimeout = 5 * time.Second
	}
	shutdownTimeout := cfg.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = 2 * time.Second
	}

	cmd := exec.Command(cfg.BinaryPath, cfg.Args...)
	env := append([]string{}, os.Environ()...)
	env = append(env,
		"MARLIN_BRIDGE_ADDR="+bindAddr,
		"MARLIN_BRIDGE_TOKEN="+token,
	)
	if cfg.PublisherID != "" {
		env = append(env, "MARLIN_BRIDGE_PUBLISHER="+cfg.PublisherID)
	}
	if cfg.PublisherKeyHex != "" {
		env = append(env, "MARLIN_BRIDGE_PUBKEY_HEX="+strings.TrimSpace(cfg.PublisherKeyHex))
	}
	env = append(env, cfg.ExtraEnv...)
	cmd.Env = env
	if cfg.Stderr != nil {
		cmd.Stderr = cfg.Stderr
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("transport: sidecar stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("transport: start sidecar %s: %w", cfg.BinaryPath, err)
	}

	readyCh := make(chan string, 1)
	readErrCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "READY ") {
				addr := strings.TrimSpace(strings.TrimPrefix(line, "READY "))
				select {
				case readyCh <- addr:
				default:
				}
				// Drain remaining stdout so the child never blocks on pipe writes.
				_, _ = io.Copy(io.Discard, stdout)
				return
			}
		}
		if err := scanner.Err(); err != nil {
			readErrCh <- err
			return
		}
		readErrCh <- io.EOF
	}()

	sup := &SidecarSupervisor{
		cmd:             cmd,
		token:           token,
		shutdownTimeout: shutdownTimeout,
		done:            make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		sup.mu.Lock()
		sup.exitErr = err
		wasStopped := sup.stopped
		sup.mu.Unlock()
		close(sup.done)
		if !wasStopped && cfg.OnExit != nil {
			cfg.OnExit(err)
		}
	}()

	timer := time.NewTimer(startTimeout)
	defer timer.Stop()

	select {
	case addr := <-readyCh:
		if !isLoopbackHostPort(addr) {
			_ = sup.Stop(context.Background())
			return nil, fmt.Errorf("%w: %q", ErrSidecarNonLoopback, addr)
		}
		sup.address = addr
		return sup, nil
	case err := <-readErrCh:
		_ = sup.Stop(context.Background())
		return nil, fmt.Errorf("%w: %v", ErrSidecarExitedEarly, err)
	case <-sup.done:
		return nil, fmt.Errorf("%w: %v", ErrSidecarExitedEarly, sup.ExitErr())
	case <-timer.C:
		_ = sup.Stop(context.Background())
		return nil, fmt.Errorf("%w (%s)", ErrSidecarStartTimeout, startTimeout)
	case <-ctx.Done():
		_ = sup.Stop(context.Background())
		return nil, ctx.Err()
	}
}

// Address returns the verified loopback host:port advertised by READY.
func (s *SidecarSupervisor) Address() string { return s.address }

// AuthHeader returns the formatted HTTP/2 Bearer authorization value.
func (s *SidecarSupervisor) AuthHeader() string { return "Bearer " + s.token }

// Token returns the raw ephemeral token passed to the sidecar environment.
func (s *SidecarSupervisor) Token() string { return s.token }

// PID returns the OS process ID of the supervised child.
func (s *SidecarSupervisor) PID() int {
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// Done closes when the sidecar child process has terminated and been reaped.
func (s *SidecarSupervisor) Done() <-chan struct{} { return s.done }

// ExitErr returns the process exit error once Done() has closed.
func (s *SidecarSupervisor) ExitErr() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.exitErr
}

// Stop requests graceful shutdown (os.Interrupt) and escalates to Kill after
// ShutdownTimeout so no orphan sidecar process survives.
func (s *SidecarSupervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()

	select {
	case <-s.done:
		return nil
	default:
	}

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(os.Interrupt)
	}
	waitTimeout := s.shutdownTimeout
	if waitTimeout <= 0 {
		waitTimeout = 2 * time.Second
	}
	timer := time.NewTimer(waitTimeout)
	defer timer.Stop()

	select {
	case <-s.done:
		return nil
	case <-timer.C:
	case <-ctx.Done():
	}

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	<-s.done
	return nil
}

func isLoopbackHostPort(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
