package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var ErrShutdown = errors.New("守护进程已主动退出")

func (i *Instance) shutdownPath() string {
	return filepath.Join(filepath.Dir(i.Socket), "shutdown")
}

func (i *Instance) withControlLock(action func() error) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(i.Socket), "control.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("打开进程控制锁：%w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("获取进程控制锁：%w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return action()
}

// Reconnect preserves automatic recovery unless a client requested a full exit.
func (i *Instance) Reconnect() error {
	return i.withControlLock(func() error {
		if _, err := os.Stat(i.shutdownPath()); err == nil {
			return ErrShutdown
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("读取退出标记：%w", err)
		}
		return i.ensure()
	})
}

func (i *Instance) Shutdown() error {
	return i.withControlLock(func() error {
		if err := os.WriteFile(i.shutdownPath(), nil, 0600); err != nil {
			return fmt.Errorf("写入退出标记：%w", err)
		}
		requestErr := i.Client.Shutdown()
		if err := i.waitStopped(); err != nil {
			if requestErr != nil {
				return fmt.Errorf("请求守护进程退出失败：%w；%v", requestErr, err)
			}
			return err
		}
		return nil
	})
}

func (i *Instance) waitStopped() error {
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(i.Socket), "daemon.lock"), os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("打开守护进程锁：%w", err)
	}
	defer lock.Close()
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("检查守护进程锁：%w", err)
		}
		if time.Now().After(deadline) {
			return errors.New("等待守护进程停止超时（30 秒）")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
