package main

import (
	"bufio"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const page = `<!doctype html>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{--bg:#fff;--fg:#1b1b1f;--mut:#5c5f6b;--line:#e3e4e8;--acc:#2f5bd8;--warn:#8a3a2e}
@media(prefers-color-scheme:dark){:root:not([data-theme=light]){--bg:#15161a;--fg:#e8e9ed;--mut:#9ea2ae;--line:#2b2d35;--acc:#7fa0f5;--warn:#e2846f}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.55 ui-sans-serif,system-ui,sans-serif}
main{max-width:46rem;margin:0 auto;padding:2.5rem 16px}
h1{font-size:1.35rem;margin:0 0 .2rem}
.sub{color:var(--mut);margin:0 0 1.8rem}
.row{display:flex;justify-content:space-between;gap:1rem;padding:.55rem 0;border-bottom:1px solid var(--line)}
.row span:first-child{color:var(--mut)}
.bar{height:6px;background:var(--line);border-radius:3px;overflow:hidden;margin:.5rem 0 1.4rem}
.bar i{display:block;height:100%;background:var(--acc)}
label{display:block;margin:1.1rem 0 .3rem;font-weight:500}
input{width:100%;padding:.6rem .7rem;font:inherit;color:var(--fg);background:var(--bg);
 border:1px solid var(--line);border-radius:6px}
input:focus{outline:2px solid var(--acc);outline-offset:1px}
button{margin-top:1.4rem;padding:.6rem 1.1rem;font:inherit;font-weight:500;color:#fff;
 background:var(--acc);border:0;border-radius:6px;cursor:pointer}
.err{color:var(--warn);border:1px solid var(--warn);border-radius:6px;padding:.6rem .7rem;margin:1rem 0}
pre{background:color-mix(in srgb,var(--fg) 5%,transparent);border:1px solid var(--line);
 border-radius:6px;padding:.7rem;overflow-x:auto;font-size:12.5px;line-height:1.5;margin:0}
footer{color:var(--mut);font-size:12.5px;margin-top:2rem}
</style>
<main>
<h1>{{.Title}}</h1>
<p class="sub">{{.Subtitle}}</p>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
{{if .Form}}
<form method="post" action="/login" autocomplete="off">
  <label for="password">Proton password</label>
  <input id="password" name="password" type="password" required autocomplete="off">
  <label for="code">Two-factor code</label>
  <input id="code" name="code" type="text" inputmode="numeric" pattern="[0-9 ]{6,9}"
         required autocomplete="off" autofocus>
  <button type="submit">Log in</button>
</form>
<footer>The code is used immediately and neither field is stored. Bridge keeps
its own session on the volume; this page exists only until it has one.</footer>
{{else}}
{{range .Rows}}<div class="row"><span>{{.K}}</span><span>{{.V}}</span></div>{{end}}
{{if ge .Progress 0.0}}<div class="bar"><i style="width:{{.ProgressPct}}%"></i></div>{{end}}
<h1 style="font-size:1rem;margin:1.6rem 0 .5rem">Recent log</h1>
<pre>{{.Log}}</pre>
<footer>Read from Bridge's own log files on the shared volume. No credential is
shown here: the IMAP password belongs in OpenBao.</footer>
{{end}}
</main>`

var tpl = template.Must(template.New("p").Parse(page))

type kv struct{ K, V string }

type view struct {
	Title, Subtitle, Error string
	Form                   bool
	Rows                   []kv
	Progress               float64
	ProgressPct            string
	Log                    string
}

func (s *server) renderForm(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	_ = tpl.Execute(w, view{
		Title:    "Proton Bridge — sign in",
		Subtitle: "No account is configured yet. " + s.cfg.username,
		Error:    errMsg,
		Form:     true,
	})
}

var progressRe = regexp.MustCompile(`Progress: ([0-9.]+)`)

func (s *server) renderDiagnostics(w http.ResponseWriter) {
	v := view{
		Title:    "Proton Bridge",
		Subtitle: "Logged in. Nothing here needs you.",
		Progress: -1,
	}
	v.Rows = append(v.Rows, kv{"account", orDash(s.cfg.username)})

	tail, prog := s.readLog(120)
	if prog >= 0 {
		v.Progress = prog
		v.ProgressPct = strconv.FormatFloat(prog*100, 'f', 1, 64)
		v.Rows = append(v.Rows, kv{"initial sync", v.ProgressPct + "%"})
	} else {
		v.Rows = append(v.Rows, kv{"initial sync", "no progress reported — likely complete"})
	}
	if b, err := os.ReadFile(s.cfg.marker()); err == nil {
		v.Rows = append(v.Rows, kv{"logged in at", strings.TrimSpace(string(b))})
	}
	v.Log = tail
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tpl.Execute(w, v)
}

// readLog returns the tail of Bridge's newest log file and the most recent
// sync progress in it. Progress is -1 when the log mentions none, which is
// also what a finished sync looks like.
func (s *server) readLog(lines int) (string, float64) {
	ents, err := os.ReadDir(s.cfg.logDir())
	if err != nil {
		return "(no log directory yet)", -1
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "(no log files yet)", -1
	}
	sort.Strings(names) // the names begin with a timestamp, so this is newest-last
	f, err := os.Open(filepath.Join(s.cfg.logDir(), names[len(names)-1]))
	if err != nil {
		return fmt.Sprintf("(cannot read %s)", names[len(names)-1]), -1
	}
	defer f.Close()

	ring := make([]string, 0, lines)
	prog := -1.0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 4*1024*1024) // Bridge logs long lines
	for sc.Scan() {
		t := sc.Text()
		if m := progressRe.FindStringSubmatch(t); m != nil {
			if p, err := strconv.ParseFloat(m[1], 64); err == nil {
				prog = p
			}
			continue // progress spam is not worth showing as log lines
		}
		if len(ring) == lines {
			ring = ring[1:]
		}
		ring = append(ring, t)
	}
	return strings.Join(ring, "\n"), prog
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
