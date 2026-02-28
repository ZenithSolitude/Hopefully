package modules

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type LogLevel string
const (LvlInfo LogLevel="info"; LvlOK LogLevel="ok"; LvlWarn LogLevel="warn"; LvlError LogLevel="error")

type LogLine struct {
	Text  string   `json:"text"`
	Level LogLevel `json:"level"`
	Done  bool     `json:"done"`
	Error bool     `json:"error"`
}

type Task struct {
	ID   string
	done bool
	mu   sync.Mutex
	buf  []LogLine
	ch   chan LogLine
}

var (tasksMu sync.Mutex; taskMap = map[string]*Task{})

func newTask() *Task {
	t := &Task{ID: fmt.Sprintf("%d", time.Now().UnixNano()), ch: make(chan LogLine, 256)}
	tasksMu.Lock(); taskMap[t.ID] = t; tasksMu.Unlock()
	go func() { time.Sleep(10*time.Minute); tasksMu.Lock(); delete(taskMap,t.ID); tasksMu.Unlock() }()
	return t
}

func GetTask(id string) (*Task, bool) {
	tasksMu.Lock(); defer tasksMu.Unlock()
	t, ok := taskMap[id]; return t, ok
}

func (t *Task) log(level LogLevel, text string) {
	line := LogLine{Text: text, Level: level}
	t.mu.Lock(); t.buf = append(t.buf, line); t.mu.Unlock()
	select { case t.ch <- line: default: }
}

func (t *Task) finish(err error) {
	final := LogLine{Done: true}
	if err != nil {
		final.Error = true; final.Text = "ERROR: "+err.Error(); final.Level = LvlError
		t.mu.Lock(); t.buf = append(t.buf, LogLine{Text: final.Text, Level: LvlError}); t.mu.Unlock()
	}
	t.done = true; t.ch <- final; close(t.ch)
}

func (t *Task) Stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type","text/event-stream")
	w.Header().Set("Cache-Control","no-cache")
	w.Header().Set("X-Accel-Buffering","no")
	fl,ok := w.(http.Flusher)
	if !ok { http.Error(w,"streaming not supported",500); return }

	send := func(line LogLine) {
		b, _ := json.Marshal(line)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}

	t.mu.Lock(); buffered := append([]LogLine{}, t.buf...); t.mu.Unlock()
	for _, l := range buffered { send(l) }
	if t.done { send(LogLine{Done: true}); return }

	for {
		select {
		case line, open := <-t.ch:
			send(line)
			if !open || line.Done { return }
		case <-r.Context().Done(): return
		case <-time.After(25*time.Second): fmt.Fprint(w,": ping\n\n"); fl.Flush()
		}
	}
}

func (r *Registry) InstallGitHub(ctx context.Context, repoURL string) *Task {
	t := newTask()
	go func() {
		t.log(LvlInfo, "Клонирование: "+repoURL)
		name := filepath.Base(strings.TrimSuffix(repoURL, ".git"))
		tmp := filepath.Join(os.TempDir(), "hf_clone_"+name)
		defer os.RemoveAll(tmp)
		if err := runLog(ctx, t, "", "git", "clone", "--depth=1", repoURL, tmp); err != nil {
			t.finish(fmt.Errorf("git clone: %w", err)); return
		}
		t.log(LvlOK, "Клонировано")
		if err := r.finalize(ctx, tmp, "github", repoURL, t); err != nil { t.finish(err); return }
		t.finish(nil)
	}()
	return t
}

func (r *Registry) InstallZip(ctx context.Context, zipPath string) *Task {
	t := newTask()
	go func() {
		defer os.Remove(zipPath)
		t.log(LvlInfo, "Распаковка архива...")
		tmp := zipPath+"_dir"
		defer os.RemoveAll(tmp)
		if err := unzip(zipPath, tmp); err != nil { t.finish(fmt.Errorf("unzip: %w", err)); return }
		t.log(LvlOK, "Распаковано")
		src, err := findManifestDir(tmp)
		if err != nil { t.finish(err); return }
		if err := r.finalize(ctx, src, "zip", "", t); err != nil { t.finish(err); return }
		t.finish(nil)
	}()
	return t
}

func (r *Registry) finalize(ctx context.Context, src, srcType, srcURL string, t *Task) error {
	mf, err := loadManifest(src)
	if err != nil { return err }
	t.log(LvlInfo, fmt.Sprintf("Модуль: %s v%s — %s", mf.Name, mf.Version, mf.Description))

	for _, dep := range mf.Requires {
		if _, err := exec.LookPath(dep); err != nil {
			return fmt.Errorf("требуется %q, но не найден в PATH", dep)
		}
		t.log(LvlOK, "OK: "+dep)
	}

	dst := r.moduleDir(mf.Name)
	if old, ok := r.Get(mf.Name); ok {
		t.log(LvlWarn, "Обновление существующего модуля")
		r.stopProcess(old)
		os.RemoveAll(dst)
	}
	t.log(LvlInfo, "Копирование файлов...")
	if err := copyDir(src, dst); err != nil { return fmt.Errorf("copy: %w", err) }

	installSh := filepath.Join(dst, "install.sh")
	if _, err := os.Stat(installSh); err == nil {
		t.log(LvlInfo, "Запуск install.sh...")
		os.Chmod(installSh, 0755)
		if err := runLog(ctx, t, dst, "bash", installSh); err != nil {
			return fmt.Errorf("install.sh: %w", err)
		}
		t.log(LvlOK, "install.sh выполнен")
	}

	m := &Module{Name:mf.Name,Version:mf.Version,Description:mf.Description,Author:mf.Author,
		SourceType:srcType,SourceURL:srcURL,Manifest:mf,InstalledAt:time.Now()}
	r.register(m)
	t.log(LvlOK, fmt.Sprintf("Модуль «%s» установлен! Активируйте его на странице модулей.", mf.Name))
	return nil
}

func runLog(ctx context.Context, t *Task, dir, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	if dir != "" { cmd.Dir = dir }
	pr, pw, _ := os.Pipe()
	cmd.Stdout = pw; cmd.Stderr = pw
	if err := cmd.Start(); err != nil { return err }
	pw.Close()
	go func() {
		buf := make([]byte, 1)
		var line strings.Builder
		for {
			_, err := pr.Read(buf)
			if err != nil { if s := strings.TrimSpace(line.String()); s != "" { t.log(LvlInfo,"  "+s) }; break }
			if buf[0] == '\n' { if s := strings.TrimSpace(line.String()); s != "" { t.log(LvlInfo,"  "+s) }; line.Reset() } else { line.WriteByte(buf[0]) }
		}
	}()
	return cmd.Wait()
}

func unzip(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil { return err }
	defer r.Close()
	os.MkdirAll(dst, 0755)
	for _, f := range r.File {
		path := filepath.Join(dst, filepath.Clean(f.Name))
		if !strings.HasPrefix(path, filepath.Clean(dst)+string(os.PathSeparator)) { continue }
		if f.FileInfo().IsDir() { os.MkdirAll(path, f.Mode()); continue }
		os.MkdirAll(filepath.Dir(path), 0755)
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil { return err }
		rc, _ := f.Open(); io.Copy(out, rc); rc.Close(); out.Close()
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil { return err }
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() { return os.MkdirAll(target, info.Mode()) }
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil { return err }
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil { return err }
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
