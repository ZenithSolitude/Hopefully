package modules

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ZenithSolitude/Hopefully/internal/db"
)

type Module struct {
	ID          int64
	Name        string
	Version     string
	Description string
	Author      string
	Status      string
	SourceType  string
	SourceURL   string
	Manifest    *Manifest
	ErrorLog    string
	InstalledAt time.Time
	proc        *exec.Cmd
}

type Registry struct {
	mu      sync.RWMutex
	byName  map[string]*Module
	dataDir string
}

var Default = &Registry{byName: make(map[string]*Module)}

func (r *Registry) Setup(dataDir string) {
	r.dataDir = dataDir
	os.MkdirAll(r.modulesDir(), 0755)
}

func (r *Registry) modulesDir() string { return filepath.Join(r.dataDir, "modules") }
func (r *Registry) moduleDir(n string) string { return filepath.Join(r.modulesDir(), n) }

func (r *Registry) LoadFromDB() {
	rows, err := db.DB.Query(
		`SELECT id,name,version,description,author,status,source_type,source_url,manifest,error_log,installed_at FROM modules ORDER BY name`)
	if err != nil {
		log.Printf("modules load: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		m := &Module{}
		var mj, ia string
		rows.Scan(&m.ID,&m.Name,&m.Version,&m.Description,&m.Author,&m.Status,&m.SourceType,&m.SourceURL,&mj,&m.ErrorLog,&ia)
		t, _ := time.Parse("2006-01-02 15:04:05", ia)
		m.InstalledAt = t
		var mf Manifest
		if json.Unmarshal([]byte(mj), &mf) == nil {
			m.Manifest = &mf
		}
		r.mu.Lock()
		r.byName[m.Name] = m
		r.mu.Unlock()
		if m.Status == "active" {
			if err := r.startProcess(m); err != nil {
				log.Printf("autostart %s: %v", m.Name, err)
				m.Status = "error"
				m.ErrorLog = err.Error()
				db.DB.Exec(`UPDATE modules SET status='error',error_log=? WHERE name=?`, err.Error(), m.Name)
			}
		}
	}
}

func (r *Registry) All() []*Module {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Module, 0, len(r.byName))
	for _, m := range r.byName {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) Get(name string) (*Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.byName[name]
	return m, ok
}

type NavItem struct {
	Name     string
	Label    string
	Icon     string
	Href     string
	Position int
}

func (r *Registry) NavItems() []NavItem {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var items []NavItem
	for _, m := range r.byName {
		if m.Status != "active" || m.Manifest == nil || m.Manifest.Menu.Hidden {
			continue
		}
		label := m.Manifest.Menu.Label
		if label == "" { label = m.Description }
		if label == "" { label = m.Name }
		icon := m.Manifest.Menu.Icon
		if icon == "" { icon = "package" }
		items = append(items, NavItem{
			Name: m.Name, Label: label, Icon: icon,
			Href: "/modules/" + m.Name, Position: m.Manifest.Menu.Position,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Position != items[j].Position {
			return items[i].Position < items[j].Position
		}
		return items[i].Name < items[j].Name
	})
	return items
}

func (r *Registry) Activate(name string) error {
	r.mu.Lock()
	m, ok := r.byName[name]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("module %q not found", name)
	}
	if m.Manifest != nil && m.Manifest.Entrypoint != "" {
		if err := r.startProcess(m); err != nil {
			db.DB.Exec(`UPDATE modules SET status='error',error_log=? WHERE name=?`, err.Error(), name)
			m.Status = "error"; m.ErrorLog = err.Error()
			return err
		}
	}
	m.Status = "active"; m.ErrorLog = ""
	db.DB.Exec(`UPDATE modules SET status='active',error_log='' WHERE name=?`, name)
	return nil
}

func (r *Registry) Deactivate(name string) {
	r.mu.Lock()
	m, ok := r.byName[name]
	r.mu.Unlock()
	if !ok { return }
	r.stopProcess(m)
	m.Status = "inactive"
	db.DB.Exec(`UPDATE modules SET status='inactive' WHERE name=?`, name)
}

func (r *Registry) Delete(name string) error {
	r.Deactivate(name)
	r.mu.Lock()
	delete(r.byName, name)
	r.mu.Unlock()
	db.DB.Exec(`DELETE FROM modules WHERE name=?`, name)
	return os.RemoveAll(r.moduleDir(name))
}

func (r *Registry) startProcess(m *Module) error {
	if m.Manifest == nil || m.Manifest.Entrypoint == "" { return nil }
	dir := r.moduleDir(m.Name)
	entry := filepath.Join(dir, m.Manifest.Entrypoint)
	if _, err := os.Stat(entry); err != nil {
		return fmt.Errorf("entrypoint %q not found", m.Manifest.Entrypoint)
	}
	os.Chmod(entry, 0755)
	args := append([]string{entry}, m.Manifest.Args...)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"MODULE_NAME="+m.Name,
		"MODULE_DIR="+dir,
		"DATA_DIR="+filepath.Join(r.dataDir, "module_data", m.Name),
	)
	if m.Manifest.Port != 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", m.Manifest.Port))
	}
	for k, v := range m.Manifest.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	lp := filepath.Join(r.dataDir, "logs", "module-"+m.Name+".log")
	os.MkdirAll(filepath.Dir(lp), 0755)
	if lf, err := os.OpenFile(lp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		cmd.Stdout = lf; cmd.Stderr = lf
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	m.proc = cmd
	log.Printf("modules: started %s (pid %d)", m.Name, cmd.Process.Pid)
	go func() {
		cmd.Wait()
		log.Printf("modules: %s exited", m.Name)
		r.mu.Lock()
		if mod, ok := r.byName[m.Name]; ok && mod.Status == "active" {
			mod.Status = "error"
			mod.ErrorLog = "process exited unexpectedly"
			db.DB.Exec(`UPDATE modules SET status='error',error_log='process exited unexpectedly' WHERE name=?`, m.Name)
		}
		r.mu.Unlock()
	}()
	time.Sleep(300 * time.Millisecond)
	return nil
}

func (r *Registry) stopProcess(m *Module) {
	if m.proc == nil || m.proc.Process == nil { return }
	m.proc.Process.Kill()
	m.proc.Wait()
	m.proc = nil
}

func (r *Registry) register(m *Module) {
	mj, _ := json.Marshal(m.Manifest)
	db.DB.Exec(`
		INSERT INTO modules (name,version,description,author,status,source_type,source_url,manifest)
		VALUES (?,?,?,?,'inactive',?,?,?)
		ON CONFLICT(name) DO UPDATE SET
			version=excluded.version,description=excluded.description,author=excluded.author,
			source_type=excluded.source_type,source_url=excluded.source_url,
			manifest=excluded.manifest,status='inactive',error_log=''`,
		m.Name,m.Version,m.Description,m.Author,m.SourceType,m.SourceURL,string(mj),
	)
	db.DB.QueryRow(`SELECT id FROM modules WHERE name=?`, m.Name).Scan(&m.ID)
	r.mu.Lock()
	r.byName[m.Name] = m
	r.mu.Unlock()
}
