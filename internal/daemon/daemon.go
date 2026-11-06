package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"frp-tui/internal/model"
)

type service struct {
	dir          string
	mutation     sync.Mutex
	mu           sync.RWMutex
	cfg          model.Config
	processes    map[string]*managedProcess
	shutdown     chan struct{}
	shutdownOnce sync.Once
	closing      bool
}

func Run(dataDir, socketPath string) error {
	dataLock, err := os.Open(dataDir)
	if err != nil {
		return err
	}
	defer dataLock.Close()
	if err = syscall.Flock(int(dataLock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("该程序目录的守护进程已运行或目录锁不可用：%w", err)
	}
	defer syscall.Flock(int(dataLock.Fd()), syscall.LOCK_UN)
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(socketPath), "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("守护进程已运行或锁不可用：%w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	for _, name := range []string{"configs", "logs"} {
		if err = os.MkdirAll(filepath.Join(dataDir, name), 0700); err != nil {
			return err
		}
	}
	logfile, err := NewRotatingWriter(filepath.Join(dataDir, "logs", "daemon.log"))
	if err != nil {
		return err
	}
	defer logfile.Close()
	oldLog := log.Writer()
	log.SetOutput(logfile)
	defer log.SetOutput(oldLog)
	cfg, err := loadConfig(dataDir)
	if err != nil {
		return err
	}
	s := &service{dir: dataDir, cfg: cfg, processes: map[string]*managedProcess{}, shutdown: make(chan struct{})}
	defer func() {
		s.mu.RLock()
		defer s.mu.RUnlock()
		for _, p := range s.processes {
			p.logs.disk.Close()
		}
	}()
	for _, c := range cfg.Connections {
		p, err := s.newProcess(c.ID)
		if err != nil {
			return err
		}
		s.processes[c.ID] = p
	}
	if err = os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	if err = os.Chmod(socketPath, 0600); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]bool{"ok": true}) })
	mux.HandleFunc("GET /v1/state", s.state)
	mux.HandleFunc("POST /v1/connections", s.create)
	mux.HandleFunc("PUT /v1/connections/{id}", s.update)
	mux.HandleFunc("DELETE /v1/connections/{id}", s.remove)
	mux.HandleFunc("POST /v1/connections/{id}/{action}", s.action)
	mux.HandleFunc("GET /v1/connections/{id}/logs", s.logs)
	mux.HandleFunc("PUT /v1/settings", s.settings)
	mux.HandleFunc("POST /v1/shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		s.shutdownOnce.Do(func() { close(s.shutdown) })
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	serving := make(chan error, 1)
	for _, c := range cfg.Connections {
		if c.DesiredRunning {
			s.processes[c.ID].start(dataDir, cfg.FrpcPath, c)
		}
	}
	go func() { serving <- server.Serve(listener) }()
	log.Printf("守护进程已启动，socket=%s", socketPath)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	var serveErr error
	select {
	case <-s.shutdown:
	case <-signals:
	case serveErr = <-serving:
	}
	s.mutation.Lock()
	s.closing = true
	s.mutation.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	var stopped sync.WaitGroup
	s.mu.RLock()
	for _, p := range s.processes {
		stopped.Add(1)
		go func(p *managedProcess) { defer stopped.Done(); p.opMu.Lock(); defer p.opMu.Unlock(); p.stop() }(p)
	}
	s.mu.RUnlock()
	stopped.Wait()
	log.Print("守护进程已停止")
	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}
func (s *service) newProcess(id string) (*managedProcess, error) {
	disk, err := NewRotatingWriter(filepath.Join(s.dir, "logs", id+".log"))
	if err != nil {
		return nil, err
	}
	return &managedProcess{status: model.Status{ID: id, State: "stopped"}, logs: &connectionLogs{disk: disk}}, nil
}
func (s *service) state(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	snap := model.Snapshot{Config: cloneConfig(s.cfg), Statuses: []model.Status{}}
	for _, c := range s.cfg.Connections {
		snap.Statuses = append(snap.Statuses, s.processes[c.ID].getStatus())
	}
	s.mu.RUnlock()
	respond(w, 200, snap)
}
func (s *service) logs(w http.ResponseWriter, r *http.Request) {
	after, err := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	if r.URL.Query().Get("after") == "" {
		after = 0
		err = nil
	}
	if err != nil {
		fail(w, 400, errors.New("日志序号无效"))
		return
	}
	s.mu.RLock()
	p := s.processes[r.PathValue("id")]
	s.mu.RUnlock()
	if p == nil {
		fail(w, 404, errors.New("连接不存在"))
		return
	}
	respond(w, 200, p.logs.read(after))
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		fail(w, 400, errors.New("请求 JSON 无效"))
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		fail(w, 400, errors.New("请求必须包含单个 JSON 对象"))
		return false
	}
	return true
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("发送响应失败：%v", err)
	}
}
func fail(w http.ResponseWriter, status int, err error) {
	respond(w, status, model.APIError{Error: err.Error()})
}
func (s *service) begin(w http.ResponseWriter, r *http.Request) bool {
	s.mutation.Lock()
	if err := r.Context().Err(); err != nil {
		s.mutation.Unlock()
		fail(w, 408, err)
		return false
	}
	if s.closing {
		s.mutation.Unlock()
		fail(w, 503, errors.New("守护进程正在停止"))
		return false
	}
	return true
}
func (s *service) config() model.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfig(s.cfg)
}
func (s *service) commit(cfg model.Config) error {
	cfg.Revision++
	if err := saveConfig(s.dir, cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return nil
}
func findConnection(cfg model.Config, id string) int {
	for i, c := range cfg.Connections {
		if c.ID == id {
			return i
		}
	}
	return -1
}
func (s *service) check(w http.ResponseWriter, cfg model.Config, id string, revision int64) int {
	i := findConnection(cfg, id)
	if i < 0 {
		fail(w, 404, errors.New("连接不存在"))
		return -1
	}
	if cfg.Connections[i].Revision != revision {
		fail(w, 409, errors.New("连接已被修改，请重新加载后操作"))
		return -1
	}
	return i
}
