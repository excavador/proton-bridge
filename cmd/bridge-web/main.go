package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/excavador/proton-bridge/internal/version"
)

type config struct {
	addr     string
	stateDir string
	bridge   string
	username string
}

// marker is written only by this process, and only after a login that Bridge
// reported as successful. The sibling container waits for it before starting
// the daemon, so it is the single source of truth for "is there an account"
// and has exactly one writer.
func (c config) marker() string { return filepath.Join(c.stateDir, ".state") }

// logDir is where Bridge keeps its own structured logs. Note it is
// .local/share and NOT .cache -- the cache path exists too and holds other
// things, which cost an afternoon to discover.
func (c config) logDir() string {
	return filepath.Join(c.stateDir, ".local/share/protonmail/bridge-v3/logs")
}

func main() {
	var c config
	flag.StringVar(&c.addr, "addr", ":8080", "listen address")
	flag.StringVar(&c.stateDir, "state-dir", "/root", "Bridge's HOME, the shared volume")
	flag.StringVar(&c.bridge, "bridge", "/usr/local/bin/proton-bridge", "path to the Bridge binary")
	flag.StringVar(&c.username, "username", os.Getenv("PROTON_USERNAME"), "the Proton address to log in as")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := &server{cfg: c, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.index)
	mux.HandleFunc("/login", srv.login)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	log.Info("listening", "addr", c.addr, "version", version.Version,
		"logged_in", srv.loggedIn(), "username", c.username)

	// Timeouts on everything: this is reachable through the gateway, and a
	// handler that drives a CLI must not be able to pin a connection open.
	hs := &http.Server{
		Addr:              c.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

type server struct {
	cfg config
	log *slog.Logger
}

func (s *server) loggedIn() bool {
	st, err := os.Stat(s.cfg.marker())
	return err == nil && !st.IsDir()
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if s.loggedIn() {
		s.renderDiagnostics(w)
		return
	}
	s.renderForm(w, "")
}

// newlines are the whole attack. The credentials are piped into Bridge's CLI
// as VALUES on their own lines, so a \r or \n inside one would end that line
// early and turn the remainder into a command. Rejecting them outright is
// both the simplest defence and a complete one; there is no legitimate
// password or TOTP code containing a newline.
var hasNewline = regexp.MustCompile(`[\r\n]`)

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.loggedIn() {
		// Not an error worth dressing up: there is an account, so this
		// endpoint has nothing to do and should not be usable.
		http.Error(w, "already logged in", http.StatusConflict)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderForm(w, "could not read the form")
		return
	}
	password, code := r.PostFormValue("password"), r.PostFormValue("code")
	switch {
	case password == "" || code == "":
		s.renderForm(w, "both fields are required")
		return
	case hasNewline.MatchString(password) || hasNewline.MatchString(code):
		s.log.Warn("rejected input containing a newline")
		s.renderForm(w, "that input is not accepted")
		return
	case s.cfg.username == "":
		s.renderForm(w, "no username is configured; set PROTON_USERNAME")
		return
	}

	out, err := s.runLogin(r.Context(), password, code)
	if err != nil {
		s.log.Error("login failed", "err", err)
		s.renderForm(w, "Bridge refused the login. Check the code is current and try again.")
		return
	}
	// Bridge says this on success and nothing else does.
	if !strings.Contains(out, "was added successfully") {
		s.log.Error("login did not report success")
		s.renderForm(w, "Bridge did not confirm the login. Check the password and the code.")
		return
	}
	if err := os.WriteFile(s.cfg.marker(), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		s.log.Error("logged in but could not write the marker", "err", err)
		http.Error(w, "logged in, but the marker could not be written; the daemon will not start", http.StatusInternalServerError)
		return
	}
	s.log.Info("login succeeded, marker written; the daemon container may start")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// runLogin drives Bridge's own CLI. A FIXED command template: the caller's
// input is substituted as values and is never able to add a command.
//
// `-c` is safe here precisely because there is no account, so no daemon holds
// the single-instance lock. Running this while the daemon is up would fail
// with "another instance is already running", which is why the handler
// refuses when the marker exists.
func (s *server) runLogin(ctx context.Context, password, code string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.cfg.bridge, "-c")
	cmd.Stdin = strings.NewReader(fmt.Sprintf(
		"login\n%s\n%s\nno\n%s\nexit\n", s.cfg.username, password, code))
	out, err := cmd.CombinedOutput()
	return string(out), err
}
