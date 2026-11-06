package daemon

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"frp-tui/internal/model"
)

// RotatingWriter keeps current and two older files, all private to the user.
type RotatingWriter struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func NewRotatingWriter(path string) (*RotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	w := &RotatingWriter{path: path}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}
func (w *RotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.file = f
	w.size = st.Size()
	return nil
}
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(p)) > 5*1024*1024 {
		if err := w.file.Close(); err != nil {
			return 0, err
		}
		if err := os.Remove(w.path + ".2"); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		if err := os.Rename(w.path+".1", w.path+".2"); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		if err := os.Rename(w.path, w.path+".1"); err != nil {
			return 0, err
		}
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}
func (w *RotatingWriter) Close() error { w.mu.Lock(); defer w.mu.Unlock(); return w.file.Close() }

type connectionLogs struct {
	mu      sync.Mutex
	entries []model.LogEntry
	seq     uint64
	disk    *RotatingWriter
}

func (l *connectionLogs) add(text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	e := model.LogEntry{Seq: l.seq, Time: time.Now().Format(time.RFC3339), Text: text}
	if len(l.entries) == 1000 {
		copy(l.entries, l.entries[1:])
		l.entries = l.entries[:999]
	}
	l.entries = append(l.entries, e)
	if _, err := fmt.Fprintf(l.disk, "%s %s\n", e.Time, e.Text); err != nil {
		log.Printf("写入连接日志失败：%v", err)
	}
}
func (l *connectionLogs) read(after uint64) model.Logs {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := model.Logs{Entries: []model.LogEntry{}, LastSeq: l.seq}
	// A daemon restart resets sequence numbers; return its new ring in that case.
	if after > l.seq {
		after = 0
	}
	for _, e := range l.entries {
		if e.Seq > after {
			r.Entries = append(r.Entries, e)
		}
	}
	return r
}

type lineWriter struct {
	pending []byte
	token   string
	logs    *connectionLogs
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		w.logs.add(redact(string(bytes.TrimSuffix(w.pending[:i], []byte{'\r'})), w.token))
		w.pending = w.pending[i+1:]
	}
	// Bound an unterminated log line without losing the tail used for redaction.
	if len(w.pending) > 1024*1024 {
		w.logs.add("[日志行超过 1 MiB，已截断]")
		w.pending = nil
	}
	return len(p), nil
}
func (w *lineWriter) flush() {
	if len(w.pending) > 0 {
		w.logs.add(redact(string(w.pending), w.token))
		w.pending = nil
	}
}
