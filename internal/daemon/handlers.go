package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"frp-tui/internal/model"
)

func (s *service) process(w http.ResponseWriter, id string) *managedProcess {
	s.mu.RLock()
	p := s.processes[id]
	s.mu.RUnlock()
	if p == nil {
		fail(w, 404, errors.New("连接不存在"))
	}
	return p
}
func sameBinary(w http.ResponseWriter, cfg model.Config, binary string) bool {
	if cfg.FrpcPath != binary {
		fail(w, 409, errors.New("frpc 路径已修改，请重新加载后操作"))
		return false
	}
	return true
}
func (s *service) saveConnection(w http.ResponseWriter, r *http.Request, c model.Connection, revision int64, binary string) bool {
	if !s.begin(w, r) {
		return false
	}
	defer s.mutation.Unlock()
	cfg := s.config()
	i := s.check(w, cfg, c.ID, revision)
	if i < 0 || !sameBinary(w, cfg, binary) {
		return false
	}
	cfg.Connections[i] = c
	if err := s.commit(cfg); err != nil {
		fail(w, 500, err)
		return false
	}
	return true
}
func (s *service) create(w http.ResponseWriter, r *http.Request) {
	var c model.Connection
	if !decode(w, r, &c) {
		return
	}
	binary := s.config().FrpcPath
	if err := verify(r.Context(), s.dir, binary, c); err != nil {
		fail(w, 400, err)
		return
	}
	if !s.begin(w, r) {
		return
	}
	defer s.mutation.Unlock()
	cfg := s.config()
	if !sameBinary(w, cfg, binary) {
		return
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		fail(w, 500, err)
		return
	}
	c.ID = hex.EncodeToString(id)
	c.Revision = 1
	c.DesiredRunning = false
	p, err := s.newProcess(c.ID)
	if err != nil {
		fail(w, 500, err)
		return
	}
	s.mu.Lock()
	s.processes[c.ID] = p
	s.mu.Unlock()
	cfg.Connections = append(cfg.Connections, c)
	if err = s.commit(cfg); err != nil {
		s.mu.Lock()
		delete(s.processes, c.ID)
		s.mu.Unlock()
		p.logs.disk.Close()
		fail(w, 500, err)
		return
	}
	respond(w, 201, c)
}
func (s *service) update(w http.ResponseWriter, r *http.Request) {
	var c model.Connection
	if !decode(w, r, &c) {
		return
	}
	id := r.PathValue("id")
	p := s.process(w, id)
	if p == nil {
		return
	}
	p.opMu.Lock()
	defer p.opMu.Unlock()
	cfg := s.config()
	revision := c.Revision
	i := s.check(w, cfg, id, revision)
	if i < 0 {
		return
	}
	c.ID = id
	c.DesiredRunning = cfg.Connections[i].DesiredRunning
	c.Revision++
	if err := verify(r.Context(), s.dir, cfg.FrpcPath, c); err != nil {
		fail(w, 400, err)
		return
	}
	if !s.saveConnection(w, r, c, revision, cfg.FrpcPath) {
		return
	}
	p.stop()
	if c.DesiredRunning {
		p.start(s.dir, cfg.FrpcPath, c)
	}
	respond(w, 200, c)
}
func (s *service) remove(w http.ResponseWriter, r *http.Request) {
	var a model.Action
	if !decode(w, r, &a) {
		return
	}
	id := r.PathValue("id")
	p := s.process(w, id)
	if p == nil {
		return
	}
	p.opMu.Lock()
	defer p.opMu.Unlock()
	committed := func() bool {
		if !s.begin(w, r) {
			return false
		}
		defer s.mutation.Unlock()
		cfg := s.config()
		i := s.check(w, cfg, id, a.Revision)
		if i < 0 {
			return false
		}
		cfg.Connections = append(cfg.Connections[:i], cfg.Connections[i+1:]...)
		if err := s.commit(cfg); err != nil {
			fail(w, 500, err)
			return false
		}
		return true
	}()
	if !committed {
		return
	}
	p.stop()
	s.mu.Lock()
	delete(s.processes, id)
	s.mu.Unlock()
	p.logs.disk.Close()
	if err := os.Remove(filepath.Join(s.dir, "configs", id+".toml")); err != nil && !os.IsNotExist(err) {
		fail(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *service) action(w http.ResponseWriter, r *http.Request) {
	var a model.Action
	if !decode(w, r, &a) {
		return
	}
	action := r.PathValue("action")
	if action != "start" && action != "stop" && action != "restart" {
		fail(w, 404, errors.New("未知操作"))
		return
	}
	id := r.PathValue("id")
	p := s.process(w, id)
	if p == nil {
		return
	}
	p.opMu.Lock()
	defer p.opMu.Unlock()
	cfg := s.config()
	i := s.check(w, cfg, id, a.Revision)
	if i < 0 {
		return
	}
	c := cfg.Connections[i]
	if action == "start" && c.DesiredRunning {
		state := p.getStatus().State
		if state == "running" || state == "starting" || state == "retrying" {
			respond(w, 200, c)
			return
		}
	}
	if action != "stop" {
		if err := verify(r.Context(), s.dir, cfg.FrpcPath, c); err != nil {
			fail(w, 400, err)
			return
		}
	}
	c.DesiredRunning = action != "stop"
	c.Revision++
	if !s.saveConnection(w, r, c, a.Revision, cfg.FrpcPath) {
		return
	}
	p.stop()
	if c.DesiredRunning {
		p.start(s.dir, cfg.FrpcPath, c)
	}
	respond(w, 200, c)
}
func (s *service) settings(w http.ResponseWriter, r *http.Request) {
	var settings model.Settings
	if !decode(w, r, &settings) {
		return
	}
	cfg := s.config()
	if cfg.Revision != settings.Revision {
		fail(w, 409, errors.New("设置已被修改，请重新加载后操作"))
		return
	}
	if err := validBinary(settings.FrpcPath); err != nil {
		fail(w, 400, err)
		return
	}
	// All checks share a single deadline, rather than five seconds per connection.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	for _, c := range cfg.Connections {
		if err := verify(ctx, s.dir, settings.FrpcPath, c); err != nil {
			fail(w, 400, err)
			return
		}
	}
	if err := ctx.Err(); err != nil {
		fail(w, 408, err)
		return
	}
	if !s.begin(w, r.WithContext(ctx)) {
		return
	}
	defer s.mutation.Unlock()
	cfg = s.config()
	if cfg.Revision != settings.Revision {
		fail(w, 409, errors.New("设置已被修改，请重新加载后操作"))
		return
	}
	cfg.FrpcPath = settings.FrpcPath
	if err := s.commit(cfg); err != nil {
		fail(w, 500, err)
		return
	}
	respond(w, 200, s.config())
}
