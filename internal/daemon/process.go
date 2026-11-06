package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"frp-tui/internal/model"
)

type managedProcess struct {
	opMu   sync.Mutex
	mu     sync.Mutex
	status model.Status
	cancel context.CancelFunc
	done   chan struct{}
	logs   *connectionLogs
}

func (p *managedProcess) getStatus() model.Status { p.mu.Lock(); defer p.mu.Unlock(); return p.status }
func (p *managedProcess) setStatus(state string, pid int, err string, retry time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.State = state
	p.status.PID = pid
	p.status.Error = err
	p.status.RetryAt = ""
	if !retry.IsZero() {
		p.status.RetryAt = retry.Format(time.RFC3339)
	}
}

// Lifecycle methods are serialized by each process's operation lock. Status and logs
// have their own locks so slow process shutdown never blocks reads.
func (p *managedProcess) start(dir, binary string, c model.Connection) {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	p.setStatus("starting", 0, "", time.Time{})
	go p.supervise(ctx, dir, binary, c, p.done)
}
func (p *managedProcess) stop() {
	if p.cancel != nil {
		p.cancel()
		<-p.done
		p.cancel = nil
		p.done = nil
	}
	p.setStatus("stopped", 0, "", time.Time{})
}
func (p *managedProcess) supervise(ctx context.Context, dir, binary string, c model.Connection, done chan struct{}) {
	defer close(done)
	// Linux binds Pdeathsig to the thread that forks the child. Keep that thread
	// alive until the child has been reaped, including while waiting on cancellation.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	config, err := frpcConfig(c)
	if err == nil {
		err = atomicWrite(filepath.Join(dir, "configs", c.ID+".toml"), config)
	}
	if err != nil {
		p.setStatus("error", 0, err.Error(), time.Time{})
		return
	}
	delay := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		p.setStatus("starting", 0, "", time.Time{})
		cmd := exec.Command(binary, "-c", filepath.Join(dir, "configs", c.ID+".toml"))
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
		stdout := &lineWriter{token: c.Token, logs: p.logs}
		stderr := &lineWriter{token: c.Token, logs: p.logs}
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		started := time.Now()
		if err := cmd.Start(); err != nil {
			message := redact(err.Error(), c.Token)
			p.logs.add("启动失败：" + message)
			p.setStatus("error", 0, message, time.Time{})
			return
		}
		p.setStatus("running", cmd.Process.Pid, "", time.Time{})
		p.logs.add(fmt.Sprintf("frpc 已启动，PID %d", cmd.Process.Pid))
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		var exitErr error
		select {
		case exitErr = <-exited:
		case <-ctx.Done():
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-exited
			}
			stdout.flush()
			stderr.flush()
			p.logs.add("frpc 已停止")
			return
		}
		stdout.flush()
		stderr.flush()
		if ctx.Err() != nil {
			return
		}
		message := "frpc 已退出"
		if exitErr != nil {
			message = redact(exitErr.Error(), c.Token)
		}
		if time.Since(started) >= 60*time.Second {
			delay = time.Second
		}
		p.logs.add(fmt.Sprintf("%s，%s 后重试", message, delay))
		p.setStatus("retrying", 0, message, time.Now().Add(delay))
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay *= 2
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
}
