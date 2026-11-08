package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"modpack-launcher/internal/app"
	"modpack-launcher/internal/config"
	"modpack-launcher/internal/game"
	"modpack-launcher/internal/pack"
)

const maxLogLines = 200

// PackInfo is one row in the pack window.
type PackInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Archive     string `json:"archive"`
}

// Runner is the launch work behind the window. Tests substitute their own.
type Runner interface {
	List(ctx context.Context, cfg config.Config) ([]pack.Published, error)
	Launch(cfg config.Config, id, version string, log func(string)) error
	Restore() error
	Update(cfg config.Config, version string, log func(string)) (exit bool, err error)
	Recover(log func(string)) error
}

// DefaultRunner uses the real GitHub, swap, and launch commands.
type DefaultRunner struct{}

func (DefaultRunner) List(ctx context.Context, cfg config.Config) ([]pack.Published, error) {
	return app.List(ctx, cfg)
}

func (DefaultRunner) Launch(cfg config.Config, id, version string, log func(string)) error {
	return app.Launch(cfg, id, version, "", log)
}

func (DefaultRunner) Restore() error { return app.Restore() }

func (DefaultRunner) Update(cfg config.Config, version string, log func(string)) (bool, error) {
	return app.Update(cfg, version, false, log)
}

func (DefaultRunner) Recover(log func(string)) error { return app.Recover(log) }

// Server is the local window backend.
type Server struct {
	Version    string
	ConfigPath string
	Runner     Runner

	mu       sync.Mutex
	cfg      config.Config
	packs    []PackInfo
	packsErr string
	logs     []string
	busy     string
	seen     time.Time
	seenOnce bool
	quit     chan struct{}
	quitOnce sync.Once
}

// New serves the pack window for an already loaded config.
func New(version, configPath string, cfg config.Config, runner Runner) *Server {
	if runner == nil {
		runner = DefaultRunner{}
	}
	return &Server{
		Version:    version,
		ConfigPath: configPath,
		Runner:     runner,
		cfg:        cfg,
		quit:       make(chan struct{}),
	}
}

// Handler is the HTTP handler for the window.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.page)
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("POST /api/config", s.saveConfig)
	mux.HandleFunc("POST /api/refresh", s.refreshHTTP)
	mux.HandleFunc("POST /api/launch", s.launch)
	mux.HandleFunc("POST /api/restore", s.restore)
	mux.HandleFunc("POST /api/update", s.update)
	mux.HandleFunc("POST /api/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /api/quit", s.quitHTTP)
	return mux
}

// Quit asks the process to exit.
func (s *Server) Quit() {
	s.quitOnce.Do(func() { close(s.quit) })
}

// Done fires when Quit is called or the window stops sending heartbeats.
func (s *Server) Done() <-chan struct{} {
	return s.quit
}

// Watch closes the launcher after the window has gone away.
func (s *Server) Watch(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Quit()
			return
		case <-s.quit:
			return
		case <-ticker.C:
			s.mu.Lock()
			once := s.seenOnce
			seen := s.seen
			s.mu.Unlock()
			if once && time.Since(seen) > 12*time.Second {
				s.Quit()
				return
			}
		}
	}
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.touch()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	s.touch()
	body := s.snapshot()
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) saveConfig(w http.ResponseWriter, r *http.Request) {
	var next config.Config
	if err := readJSON(r, &next); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if next.LauncherRepo != "" {
		if _, _, err := config.ParseRepo(next.LauncherRepo); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if next.PackRepo != "" {
		if _, _, err := config.ParseRepo(next.PackRepo); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := config.Save(s.ConfigPath, next); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	s.cfg = next
	s.mu.Unlock()
	go s.refresh()
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

func (s *Server) refreshHTTP(w http.ResponseWriter, r *http.Request) {
	s.refresh()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) launch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	if s.busy != "" {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a launch is already running"})
		return
	}
	s.busy = "playing"
	cfg := s.cfg
	s.logs = append(s.logs, "Play "+req.ID)
	s.mu.Unlock()
	go func() {
		err := s.Runner.Launch(cfg, req.ID, req.Version, s.log)
		s.mu.Lock()
		s.busy = ""
		if err != nil {
			s.logs = appendLine(s.logs, "Error: "+err.Error())
		}
		s.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	if err := s.Runner.Restore(); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	s.log("Restored Standard Mods and the game Bin")
	writeJSON(w, http.StatusOK, map[string]bool{"restored": true})
}

func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.busy != "" {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the launcher is busy"})
		return
	}
	s.busy = "updating"
	cfg := s.cfg
	s.mu.Unlock()
	exit, err := s.Runner.Update(cfg, s.Version, s.log)
	s.mu.Lock()
	s.busy = ""
	s.mu.Unlock()
	if err != nil {
		s.log("Error: " + err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "exit": exit})
	if exit {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go func() {
			time.Sleep(300 * time.Millisecond)
			s.Quit()
		}()
	}
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) quitHTTP(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"bye": true})
	go s.Quit()
}

// RefreshPacks reloads the pack list. The window calls this after startup too.
func (s *Server) RefreshPacks() { s.refresh() }

func (s *Server) refresh() {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	var packs []PackInfo
	errMsg := ""
	if cfg.PackRepo != "" {
		listed, err := s.Runner.List(context.Background(), cfg)
		if err != nil {
			errMsg = err.Error()
		} else {
			for _, item := range listed {
				name := item.Manifest.Name
				if name == "" {
					name = item.Manifest.ID
				}
				packs = append(packs, PackInfo{
					ID:          item.Manifest.ID,
					Name:        name,
					Version:     item.Manifest.Version,
					Description: item.Manifest.Description,
					Archive:     item.Manifest.Archive,
				})
			}
			if len(packs) == 0 && errMsg == "" {
				errMsg = "No packs found. A release needs manifest.json and the archive it names."
			}
		}
	} else if cfg.LocalPackDir == "" {
		errMsg = "Set a pack repository in Options."
	}
	if cfg.LocalPackDir != "" {
		local := PackInfo{
			ID:          app.LocalPackID,
			Name:        "Local pack",
			Version:     "folder",
			Description: cfg.LocalPackDir,
		}
		packs = append([]PackInfo{local}, packs...)
	}
	s.mu.Lock()
	s.packs = packs
	s.packsErr = errMsg
	s.mu.Unlock()
}

func (s *Server) snapshot() map[string]any {
	s.mu.Lock()
	cfg := s.cfg
	logs := append([]string(nil), s.logs...)
	packs := append([]PackInfo(nil), s.packs...)
	busy := s.busy
	packsErr := s.packsErr
	s.mu.Unlock()
	gameRoot := ""
	gameErr := ""
	install, err := game.Resolve(cfg.GameRoot)
	if err != nil {
		gameErr = err.Error()
	} else {
		gameRoot = install.Root
	}
	return map[string]any{
		"version":     s.Version,
		"config":      cfg,
		"tokenSet":    config.Token() != "",
		"gameRoot":    gameRoot,
		"gameError":   gameErr,
		"gameRunning": game.Running(),
		"busy":        busy,
		"logs":        logs,
		"packs":       packs,
		"packsError":  packsErr,
	}
}

func (s *Server) log(msg string) {
	s.mu.Lock()
	s.logs = appendLine(s.logs, msg)
	s.mu.Unlock()
}

func (s *Server) touch() {
	s.mu.Lock()
	s.seen = time.Now()
	s.seenOnce = true
	s.mu.Unlock()
}

func appendLine(lines []string, msg string) []string {
	lines = append(lines, msg)
	if len(lines) > maxLogLines {
		lines = lines[len(lines)-maxLogLines:]
	}
	return lines
}

func readJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return errors.New("empty body")
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
