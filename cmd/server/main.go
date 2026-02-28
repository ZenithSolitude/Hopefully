package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ZenithSolitude/Hopefully/internal/auth"
	"github.com/ZenithSolitude/Hopefully/internal/db"
	"github.com/ZenithSolitude/Hopefully/internal/modules"
	"github.com/ZenithSolitude/Hopefully/internal/system"
)

//go:embed ../../web
var embedded embed.FS

const version = "1.0.0"

type Config struct {
	Port    string
	DataDir string
	Secret  string
}

var cfg Config
var tmpl *template.Template

func initTemplates() {
	fns := template.FuncMap{
		"hasPrefix": strings.HasPrefix,
		"navItems":  modules.Default.NavItems,
		"not":       func(v bool) bool { return !v },
	}
	var err error
	tmpl, err = template.New("").Funcs(fns).ParseFS(embedded, "web/templates/*.html")
	if err != nil { log.Fatalf("templates: %v", err) }
}

func render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	if data == nil { data = map[string]any{} }
	data["CurrentUser"] = auth.CtxGet(r)
	data["CurrentPath"] = r.URL.Path
	data["Version"] = version
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "render error", 500)
	}
}

func htmlf(w http.ResponseWriter, format string, args ...any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, format, args...)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func loginGET(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.ExecuteTemplate(w, "login.html", map[string]any{})
}

func loginPOST(w http.ResponseWriter, r *http.Request) {
	user, hash, err := auth.GetByUsername(r.FormValue("username"))
	if err != nil || !auth.CheckPassword(hash, r.FormValue("password")) || !user.IsActive {
		if r.Header.Get("HX-Request") == "true" {
			htmlf(w, `<div class="alert alert-error">Неверный логин или пароль</div>`)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.ExecuteTemplate(w, "login.html", map[string]any{"Error": "Неверный логин или пароль"})
		return
	}
	db.DB.Exec(`UPDATE users SET last_login=datetime('now') WHERE id=?`, user.ID)
	tok, _ := auth.NewToken(user.ID, 24*time.Hour)
	auth.SetCookie(w, tok)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/dashboard"); w.WriteHeader(200); return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "tok", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func dashboard(w http.ResponseWriter, r *http.Request) {
	render(w, r, "dashboard.html", map[string]any{"Metrics": system.Get()})
}

func metricsSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	fl, ok := w.(http.Flusher)
	if !ok { return }
	for {
		select {
		case <-r.Context().Done(): return
		case <-time.After(2 * time.Second):
			m := system.Get()
			html := fmt.Sprintf(
				`<div id="metrics-data" class="stats-grid-inner">`+
				`<div class="stat-card"><div class="stat-label">CPU</div><div class="stat-value">%s%%</div><div class="stat-bar"><div class="stat-bar-fill" style="width:%s%%"></div></div></div>`+
				`<div class="stat-card"><div class="stat-label">Память</div><div class="stat-value">%s / %s</div><div class="stat-bar"><div class="stat-bar-fill" style="width:%s%%"></div></div></div>`+
				`<div class="stat-card"><div class="stat-label">Диск</div><div class="stat-value">%s / %s</div><div class="stat-bar"><div class="stat-bar-fill" style="width:%s%%"></div></div></div>`+
				`<div class="stat-card"><div class="stat-label">Аптайм</div><div class="stat-value">%s</div><div class="stat-sub">Load: %s</div></div>`+
				`</div>`,
				m.CPUStr(),m.CPUStr(),
				m.MemUsedStr(),m.MemTotalStr(),m.MemPctStr(),
				m.DiskUsedStr(),m.DiskTotalStr(),m.DiskPctStr(),
				m.Uptime,m.LoadAvg,
			)
			fmt.Fprintf(w, "data: %s\n\n", html)
			fl.Flush()
		}
	}
}

func usersPage(w http.ResponseWriter, r *http.Request) {
	type Row struct {
		ID int64; Username,FullName,Email,CreatedAt string; IsAdmin,IsActive bool
	}
	rows, _ := db.DB.Query(`SELECT id,username,full_name,email,is_admin,is_active,created_at FROM users ORDER BY id`)
	defer rows.Close()
	var users []Row
	for rows.Next() {
		var u Row
		rows.Scan(&u.ID,&u.Username,&u.FullName,&u.Email,&u.IsAdmin,&u.IsActive,&u.CreatedAt)
		users = append(users, u)
	}
	render(w, r, "users.html", map[string]any{"Users": users})
}

func userCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.NotFound(w,r); return }
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if username == "" || password == "" {
		htmlf(w, `<div class="alert alert-error">Логин и пароль обязательны</div>`); return
	}
	hash, _ := auth.HashPassword(password)
	isAdmin := r.FormValue("is_admin") == "1"
	_, err := db.DB.Exec(
		`INSERT INTO users (username,password,full_name,email,is_admin) VALUES (?,?,?,?,?)`,
		username,hash,r.FormValue("full_name"),r.FormValue("email"),isAdmin,
	)
	if err != nil { htmlf(w, `<div class="alert alert-error">Такой логин уже существует</div>`); return }
	w.Header().Set("HX-Refresh", "true")
}

func userToggle(w http.ResponseWriter, r *http.Request) {
	id := pathSeg(r.URL.Path, 2)
	if fmt.Sprint(auth.CtxGet(r).ID) == id { http.Error(w,"cannot modify yourself",400); return }
	db.DB.Exec(`UPDATE users SET is_active = NOT is_active WHERE id=?`, id)
	w.Header().Set("HX-Refresh", "true")
}

func userDelete(w http.ResponseWriter, r *http.Request) {
	id := pathSeg(r.URL.Path, 2)
	if fmt.Sprint(auth.CtxGet(r).ID) == id { http.Error(w,"cannot delete yourself",400); return }
	db.DB.Exec(`DELETE FROM users WHERE id=?`, id)
	w.Header().Set("HX-Refresh", "true")
}

func modulesPage(w http.ResponseWriter, r *http.Request) {
	type Row struct {
		ID int64; Name,Version,Description,Author,Status,SourceType,InstalledAt,ErrorLog string
	}
	rows, _ := db.DB.Query(`SELECT id,name,version,description,author,status,source_type,installed_at,error_log FROM modules ORDER BY name`)
	defer rows.Close()
	var mods []Row
	for rows.Next() {
		var m Row
		rows.Scan(&m.ID,&m.Name,&m.Version,&m.Description,&m.Author,&m.Status,&m.SourceType,&m.InstalledAt,&m.ErrorLog)
		mods = append(mods, m)
	}
	render(w, r, "modules.html", map[string]any{"Modules": mods})
}

func moduleInstallGitHub(w http.ResponseWriter, r *http.Request) {
	repoURL := strings.TrimSpace(r.FormValue("url"))
	if repoURL == "" { htmlf(w, `<div class="alert alert-error">URL обязателен</div>`); return }
	task := modules.Default.InstallGitHub(r.Context(), repoURL)
	htmlf(w, `<div class="install-log" hx-ext="sse" sse-connect="/modules/install/%s/stream" sse-swap="message" hx-target="#install-lines" hx-swap="beforeend"><div id="install-lines" style="font-family:monospace;font-size:12px"></div></div>`, task.ID)
}

func moduleInstallZip(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(200 << 20)
	file, hdr, err := r.FormFile("file")
	if err != nil { htmlf(w, `<div class="alert alert-error">Файл не получен</div>`); return }
	defer file.Close()
	if !strings.HasSuffix(hdr.Filename, ".zip") {
		htmlf(w, `<div class="alert alert-error">Только .zip файлы</div>`); return
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("hf_%d.zip", time.Now().UnixNano()))
	f, _ := os.Create(tmp)
	buf := make([]byte, 32*1024)
	for { n,err := file.Read(buf); if n>0{f.Write(buf[:n])}; if err!=nil{break} }
	f.Close()
	task := modules.Default.InstallZip(r.Context(), tmp)
	htmlf(w, `<div class="install-log" hx-ext="sse" sse-connect="/modules/install/%s/stream" sse-swap="message" hx-target="#install-lines" hx-swap="beforeend"><div id="install-lines" style="font-family:monospace;font-size:12px"></div></div>`, task.ID)
}

func moduleInstallStream(w http.ResponseWriter, r *http.Request) {
	taskID := pathSeg(r.URL.Path, 3)
	task, ok := modules.GetTask(taskID)
	if !ok { http.Error(w,"task not found",404); return }
	task.Stream(w, r)
}

func moduleActivate(w http.ResponseWriter, r *http.Request) {
	name := pathSeg(r.URL.Path, 2)
	if err := modules.Default.Activate(name); err != nil { http.Error(w,err.Error(),500); return }
	w.Header().Set("HX-Refresh","true")
}

func moduleDeactivate(w http.ResponseWriter, r *http.Request) {
	modules.Default.Deactivate(pathSeg(r.URL.Path, 2))
	w.Header().Set("HX-Refresh","true")
}

func moduleDelete(w http.ResponseWriter, r *http.Request) {
	modules.Default.Delete(pathSeg(r.URL.Path, 2))
	w.Header().Set("HX-Refresh","true")
}

func moduleView(w http.ResponseWriter, r *http.Request) {
	name := pathSeg(r.URL.Path, 2)
	mod, ok := modules.Default.Get(name)
	if !ok { render(w,r,"module_frame.html",map[string]any{"ModuleName":name,"Error":"Модуль не найден"}); return }
	if mod.Status != "active" {
		render(w,r,"module_frame.html",map[string]any{"ModuleName":name,"Error":"Модуль не активен ("+mod.Status+")"}); return
	}
	render(w,r,"module_frame.html",map[string]any{"ModuleName":name})
}

func moduleProxy(w http.ResponseWriter, r *http.Request) {
	name := pathSeg(r.URL.Path, 2)
	mod, ok := modules.Default.Get(name)
	if !ok || mod.Status != "active" { http.Error(w,"module unavailable",503); return }
	if mod.Manifest == nil || mod.Manifest.Port == 0 { http.Error(w,"module has no HTTP port",502); return }
	subPath := strings.TrimPrefix(r.URL.Path, "/module-proxy/"+name)
	if subPath == "" { subPath = "/" }
	target := fmt.Sprintf("http://127.0.0.1:%d%s", mod.Manifest.Port, subPath)
	if r.URL.RawQuery != "" { target += "?"+r.URL.RawQuery }
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil { http.Error(w,err.Error(),500); return }
	for k,vv := range r.Header { req.Header[k]=vv }
	resp, err := http.DefaultClient.Do(req)
	if err != nil { http.Error(w,"module unreachable",502); return }
	defer resp.Body.Close()
	for k,vv := range resp.Header { for _,v := range vv { w.Header().Add(k,v) } }
	w.WriteHeader(resp.StatusCode)
	buf := make([]byte, 32*1024)
	for { n,err:=resp.Body.Read(buf); if n>0{w.Write(buf[:n])}; if err!=nil{break} }
}

func logsPage(w http.ResponseWriter, r *http.Request) {
	n := 200
	if r.URL.Query().Get("lines") == "500" { n = 500 }
	lines := tailFile(filepath.Join(cfg.DataDir,"logs","app.log"), n)
	render(w, r, "logs.html", map[string]any{"Lines": lines})
}

// ── Router ─────────────────────────────────────────────────────────────────────

func newRouter() http.Handler {
	mux := http.NewServeMux()

	staticFS, _ := fs.Sub(embedded, "web/static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.Handle("/module-static/", http.StripPrefix("/module-static/",
		http.FileServer(http.Dir(filepath.Join(cfg.DataDir,"modules")))))

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method { case http.MethodGet: loginGET(w,r); case http.MethodPost: loginPOST(w,r) }
	})
	mux.HandleFunc("/logout", logout)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type","application/json")
		fmt.Fprintf(w,`{"status":"ok","version":"%s"}`,version)
	})

	a_ := func(h http.HandlerFunc) http.Handler { return auth.Middleware(http.HandlerFunc(h)) }
	ad := func(h http.HandlerFunc) http.Handler { return auth.AdminOnly(http.HandlerFunc(h)) }

	mux.Handle("/", a_(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" { http.Redirect(w,r,"/dashboard",http.StatusFound); return }
		http.NotFound(w,r)
	}))
	mux.Handle("/dashboard", a_(dashboard))
	mux.Handle("/dashboard/metrics", a_(metricsSSE))

	mux.Handle("/users", a_(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost { ad(userCreate).ServeHTTP(w,r) } else { usersPage(w,r) }
	}))
	mux.Handle("/users/", ad(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path,"/toggle"): userToggle(w,r)
		case r.Method==http.MethodDelete||strings.HasSuffix(r.URL.Path,"/delete"): userDelete(w,r)
		default: http.NotFound(w,r)
		}
	}))

	mux.Handle("/modules/install/github", ad(moduleInstallGitHub))
	mux.Handle("/modules/install/zip",    ad(moduleInstallZip))
	mux.Handle("/modules/install/",       a_(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path,"/stream") { moduleInstallStream(w,r) }
	}))
	mux.Handle("/modules", a_(modulesPage))
	mux.Handle("/modules/", a_(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path,"/activate"):   ad(moduleActivate).ServeHTTP(w,r)
		case strings.HasSuffix(path,"/deactivate"): ad(moduleDeactivate).ServeHTTP(w,r)
		case r.Method==http.MethodDelete||strings.HasSuffix(path,"/delete"): ad(moduleDelete).ServeHTTP(w,r)
		default: moduleView(w,r)
		}
	}))
	mux.Handle("/module-proxy/", a_(moduleProxy))
	mux.Handle("/logs", a_(logsPage))

	return mux
}

// ── Seed ──────────────────────────────────────────────────────────────────────

func seed() {
	var n int
	db.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	if n > 0 { return }
	hash, _ := auth.HashPassword("admin")
	res, _ := db.DB.Exec(
		`INSERT INTO users (username,password,full_name,email,is_admin,is_active) VALUES ('admin',?,'Administrator','admin@hopefully.local',1,1)`,
		hash,
	)
	uid, _ := res.LastInsertId()
	var roleID int64
	db.DB.QueryRow(`SELECT id FROM roles WHERE name='admin'`).Scan(&roleID)
	if roleID > 0 { db.DB.Exec(`INSERT OR IGNORE INTO user_roles VALUES (?,?)`,uid,roleID) }
	log.Println("WARNING: default admin/admin created — change password immediately!")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func pathSeg(path string, idx int) string {
	parts := strings.Split(strings.Trim(path,"/"),"/")
	if idx < len(parts) { return parts[idx] }
	return ""
}

func tailFile(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil { return nil }
	lines := strings.Split(strings.TrimRight(string(data),"\n"),"\n")
	if len(lines) > n { lines = lines[len(lines)-n:] }
	return lines
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" { return v }
	return def
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	flag.StringVar(&cfg.Port,    "port",   envOr("PORT","8080"),                  "HTTP port")
	flag.StringVar(&cfg.DataDir, "data",   envOr("DATA_DIR","/var/lib/hopefully"),"Data directory")
	flag.StringVar(&cfg.Secret,  "secret", envOr("SECRET_KEY",""),                "JWT secret (required)")
	flag.Parse()

	if cfg.Secret == "" {
		fmt.Fprintln(os.Stderr, "ERROR: SECRET_KEY is required\nGenerate: openssl rand -hex 32")
		os.Exit(1)
	}

	for _, d := range []string{cfg.DataDir, filepath.Join(cfg.DataDir,"modules"),
		filepath.Join(cfg.DataDir,"logs"), filepath.Join(cfg.DataDir,"module_data")} {
		os.MkdirAll(d, 0755)
	}

	lf, err := os.OpenFile(filepath.Join(cfg.DataDir,"logs","app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil { log.SetOutput(lf) }
	log.SetFlags(log.Ldate|log.Ltime|log.Lmsgprefix)

	auth.Init(cfg.Secret)
	if err := db.Init(cfg.DataDir); err != nil { log.Fatalf("db: %v", err) }
	seed()
	modules.Default.Setup(cfg.DataDir)
	modules.Default.LoadFromDB()
	initTemplates()

	srv := &http.Server{
		Addr:         ":"+cfg.Port,
		Handler:      newRouter(),
		ReadTimeout:  15*time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120*time.Second,
	}

	go func() {
		fmt.Printf("\n  Hopefully v%s\n  http://localhost:%s\n  admin / admin\n\n", version, cfg.Port)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed { log.Fatalf("http: %v", err) }
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Println("stopped")
}
