package instance

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"frp-tui/internal/client"
)

type Instance struct {
	DataDir string
	Socket  string
	Client  *client.Client
	mu      sync.Mutex
}

func New(dataDir string) (*Instance, error) {
	var err error
	if dataDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return nil, err
		}
		dataDir = filepath.Dir(exe)
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	dataDir, err = filepath.EvalSymlinks(dataDir)
	if err != nil {
		return nil, err
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		standardDir := fmt.Sprintf("/run/user/%d", os.Getuid())
		if info, err := os.Lstat(standardDir); err == nil && info.IsDir() && info.Sys().(*syscall.Stat_t).Uid == uint32(os.Getuid()) {
			runtimeDir = standardDir
		}
	}
	if runtimeDir == "" {
		runtimeDir = filepath.Join(os.TempDir(), fmt.Sprintf("frp-tui-%d", os.Getuid()))
	} else {
		runtimeDir = filepath.Join(runtimeDir, "frp-tui")
	}
	if err := privateDir(runtimeDir); err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(dataDir))
	runtimeDir = filepath.Join(runtimeDir, fmt.Sprintf("%x", hash[:8]))
	if err := privateDir(runtimeDir); err != nil {
		return nil, err
	}
	socket := filepath.Join(runtimeDir, "daemon.sock")
	if len(socket) >= 108 {
		return nil, fmt.Errorf("Unix socket 路径过长：%s", socket)
	}
	return &Instance{DataDir: dataDir, Socket: socket, Client: client.New(socket)}, nil
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return fmt.Errorf("运行时目录必须为当前用户拥有的目录：%s", path)
	}
	return os.Chmod(path, 0700)
}

func (i *Instance) Ensure() error {
	return i.withControlLock(func() error {
		if err := os.Remove(i.shutdownPath()); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("清除退出标记：%w", err)
		}
		return i.ensure()
	})
}

func (i *Instance) ensure() error {
	if i.Client.Health() == nil {
		return nil
	}
	logDir := filepath.Join(i.DataDir, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return fmt.Errorf("程序目录不可写：%w", err)
	}
	logPath := filepath.Join(logDir, "launcher.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() >= 5<<20 {
		if err := os.Rename(logPath, logPath+".1"); err != nil {
			return err
		}
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon", "--data-dir", i.DataDir)
	cmd.Dir = i.DataDir
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if i.Client.Health() == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("守护进程未能在 5 秒内就绪，请查看 %s", logPath)
}
