package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/nguyenquocanhz/diagward/collect"
	"github.com/nguyenquocanhz/diagward/diag"
	"github.com/nguyenquocanhz/diagward/model"
	"github.com/nguyenquocanhz/diagward/report"
	"golang.org/x/crypto/ssh"

	"github.com/nguyenquocanhz/termward/core/internal/sshx"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

// Hardware checks run Diagward's collector on a server over the pooled SSH
// connection and analyse the output here, so nothing is installed on the
// server. The collector is read-only; the opt-in disk and RAM tests are not
// exposed.
const (
	hardwareTimeout   = 4 * time.Minute
	hardwareDetectTO  = 20 * time.Second
	hardwareMaxOutput = 64 << 20 // collector stdout kept in memory
	hardwareMaxStderr = 64 << 10
	diagwardModule    = "github.com/nguyenquocanhz/diagward"
)

// Exit codes of the Linux wrapper script, shared with power.go's convention.
const (
	exitSudoRequired = 77
	exitSudoWrong    = 78
)

// HardwareResult is what a check returns and what is saved per host.
type HardwareResult struct {
	Report  *model.Report `json:"report"`
	RanAs   string        `json:"ranAs"` // root | sudo | user | admin
	SavedAt time.Time     `json:"savedAt"`
	// Partial is set when the collector was cut off (time limit, dropped
	// connection) and the report covers only what was collected so far.
	Partial bool `json:"partial,omitempty"`
}

// hardwareResponse adds views derived from the report (not stored).
type hardwareResponse struct {
	HardwareResult
	Parts []report.PartRow `json:"parts"`
	RMA   model.Text       `json:"rma"` // parts list as text for a warranty email
}

func respondHardware(res *HardwareResult) hardwareResponse {
	parts := report.Parts(res.Report)
	if parts == nil {
		parts = []report.PartRow{}
	}
	return hardwareResponse{
		HardwareResult: *res,
		Parts:          parts,
		RMA:            model.T(report.RMAText(res.Report, "en"), report.RMAText(res.Report, "vi")),
	}
}

var setVersionsOnce sync.Once

// setDiagwardVersions reports the Diagward version this build depends on in
// reports, unless it was set at link time.
func setDiagwardVersions() {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, dep := range bi.Deps {
		if dep.Path != diagwardModule {
			continue
		}
		v := dep.Version
		if dep.Replace != nil && dep.Replace.Version != "" {
			v = dep.Replace.Version
		}
		if v == "" || v == "(devel)" || v == "v0.0.0" {
			return
		}
		v = strings.TrimPrefix(v, "v")
		if diag.Version == "dev" {
			diag.Version = v
		}
		if collect.CollectorVersion == "dev" {
			collect.CollectorVersion = v
		}
	}
}

// ---------------------------------------------------------------- handlers

type hardwareInput struct {
	SudoPassword string `json:"sudoPassword"`
	AllowNoRoot  bool   `json:"allowNoRoot"`
	SinceDays    int    `json:"sinceDays"`
}

func (s *Server) runHardware(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in hardwareInput
	if !decodeOptional(w, r, &in) {
		return
	}
	if in.SinceDays < 0 || in.SinceDays > 365 {
		writeError(w, http.StatusBadRequest, "invalid", "sinceDays must be between 1 and 365", nil)
		return
	}
	h, err := s.store.Host(id)
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	if _, err := hardwarePath(s.dataDir, id); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error(), nil)
		return
	}

	// The check outlives a closed UI request on purpose: the result is saved
	// and the UI can read it later. It stops when the core shuts down.
	ctx, cancel := context.WithTimeout(s.ctx, hardwareTimeout)
	defer cancel()

	res, err := s.runCheck(ctx, h, in, srcManual)
	if err != nil {
		var se *sudoError
		switch {
		case errors.Is(err, errHardwareBusy):
			writeError(w, http.StatusConflict, "hardware_busy", "a hardware check is already running on this server", nil)
		case errors.As(err, &se) && se.wrong:
			writeError(w, http.StatusForbidden, "sudo_wrong", "the sudo password was rejected", nil)
		case errors.As(err, &se):
			writeError(w, http.StatusForbidden, "sudo_required",
				"a full hardware check needs root or sudo: enter the sudo password, or run it without root for limited results", nil)
		case errors.Is(err, errHardware):
			writeError(w, http.StatusBadGateway, "hardware_failed", strings.TrimPrefix(err.Error(), errHardware.Error()+": "), nil)
		default:
			fail(w, err, http.StatusBadGateway)
		}
		return
	}
	writeJSON(w, http.StatusOK, respondHardware(res))
}

// runCheck runs one hardware check end to end, for a person or for the
// scheduler: collect, save, compare with the previous result (raising
// alerts), and tell every UI. Only one check runs per host at a time.
func (s *Server) runCheck(ctx context.Context, h store.Host, in hardwareInput, src hwSource) (*HardwareResult, error) {
	if _, busy := s.hwBusy.LoadOrStore(h.ID, src); busy {
		return nil, errHardwareBusy
	}
	defer s.hwBusy.Delete(h.ID)
	setVersionsOnce.Do(setDiagwardVersions)
	s.hub.Publish("hardware_start", map[string]string{"hostId": h.ID, "source": string(src)})
	if src == srcManual {
		s.hw.publishHost(h.ID)
		defer s.hw.publishHost(h.ID)
	}

	prev, _ := loadHardware(s.dataDir, h.ID)
	res, err := s.collectHardware(ctx, h, in)
	if err == nil && res.Partial && s.ctx.Err() != nil {
		// Cut off because Termward is quitting: keep the last complete
		// result rather than replacing it (and the alert baseline) with this.
		err = context.Canceled
	}
	if err != nil {
		s.hub.Publish("hardware_done", map[string]string{"hostId": h.ID, "source": string(src), "error": hwErrorCode(err)})
		return nil, err
	}
	// The host may have been deleted while the check ran.
	if cur, err := s.store.Host(h.ID); err == nil {
		if err := saveHardware(s.dataDir, h.ID, res); err != nil {
			err = hwFailf("cannot save the result: %v", err)
			s.hub.Publish("hardware_done", map[string]string{"hostId": h.ID, "source": string(src), "error": "hardware_failed"})
			return nil, err
		}
		s.hw.recorded(h.ID, res, src)
		s.alertHardware(cur, prev, res)
	}
	s.hub.Publish("hardware_done", map[string]any{
		"hostId": h.ID, "source": string(src), "verdict": res.Report.Verdict.String(),
		"savedAt": res.SavedAt.Format(time.RFC3339), "summary": summarize(res),
	})
	return res, nil
}

// hwErrorCode is the short code the UI translates for a failed check.
func hwErrorCode(err error) string {
	var (
		se      *sudoError
		unknown *sshx.UnknownHostError
		changed *sshx.HostKeyChangedError
		authReq *sshx.AuthRequiredError
	)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errHardwareBusy):
		return "hardware_busy"
	case errors.As(err, &se) && se.wrong:
		return "sudo_wrong"
	case errors.As(err, &se):
		return "sudo_required"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errHardware):
		return "hardware_failed"
	case errors.As(err, &unknown):
		return "unknown_host"
	case errors.As(err, &changed):
		return "host_key_changed"
	case errors.As(err, &authReq):
		return "auth_required"
	case errors.Is(err, sshx.ErrAuthRejected):
		return "auth_failed"
	case errors.Is(err, store.ErrNotFound):
		return "not_found"
	}
	return "connect_failed"
}

func (s *Server) lastHardware(w http.ResponseWriter, r *http.Request) {
	res, ok := s.loadHardwareFor(w, r.PathValue("id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, respondHardware(res))
}

// hardwareReport downloads the last report rendered as HTML, Markdown or JSON.
func (s *Server) hardwareReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	q := r.URL.Query()
	format := q.Get("format")
	if format == "" {
		format = "html"
	}
	lang := "en"
	if strings.HasPrefix(strings.ToLower(q.Get("lang")), "vi") {
		lang = "vi"
	}
	var ext, ctype string
	switch format {
	case "html":
		ext, ctype = "html", "text/html; charset=utf-8"
	case "md", "markdown":
		ext, ctype = "md", "text/markdown; charset=utf-8"
	case "json":
		ext, ctype = "json", "application/json"
	default:
		writeError(w, http.StatusBadRequest, "invalid", "format must be html, md or json", nil)
		return
	}
	h, err := s.store.Host(id)
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	res, ok := s.loadHardwareFor(w, id)
	if !ok {
		return
	}
	var buf bytes.Buffer
	opts := report.Options{Lang: lang, NoHints: true}
	switch ext {
	case "html":
		err = report.HTML(&buf, res.Report, opts)
	case "md":
		err = report.Markdown(&buf, res.Report, opts)
	default:
		err = report.JSON(&buf, res.Report)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hardware_failed", "cannot render the report: "+err.Error(), nil)
		return
	}
	name := res.Report.Host.Hostname
	if name == "" {
		name = h.Name
	}
	when := res.SavedAt
	if when.IsZero() {
		when = time.Now()
	}
	file := fmt.Sprintf("diagward-%s-%s.%s", safeFileName(name), when.Local().Format("20060102-1504"), ext)
	hd := w.Header()
	hd.Set("Content-Type", ctype)
	hd.Set("Content-Disposition", `attachment; filename="`+file+`"`)
	hd.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) loadHardwareFor(w http.ResponseWriter, id string) (*HardwareResult, bool) {
	if _, err := s.store.Host(id); err != nil {
		fail(w, err, http.StatusBadRequest)
		return nil, false
	}
	res, err := loadHardware(s.dataDir, id)
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "not_found", "no hardware check has been run on this server yet", nil)
		return nil, false
	case err != nil:
		writeError(w, http.StatusInternalServerError, "hardware_failed", "cannot read the last result: "+err.Error(), nil)
		return nil, false
	}
	return res, true
}

// ---------------------------------------------------------------- collection

var errHardware = errors.New("hardware check failed")

type sudoError struct{ wrong bool }

func (e *sudoError) Error() string {
	if e.wrong {
		return "the sudo password was rejected"
	}
	return "root or sudo is required"
}

func hwFailf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errHardware, fmt.Sprintf(format, args...))
}

// collectHardware detects the server's OS, runs the collector and analyses
// what came back.
func (s *Server) collectHardware(ctx context.Context, h store.Host, in hardwareInput) (*HardwareResult, error) {
	osName, err := s.detectOS(ctx, h.ID)
	if err != nil {
		return nil, err
	}
	opts := collect.Options{SinceDays: in.SinceDays}.WithDefaults()
	boundary := collect.NewBoundary()
	script, err := collect.Script(osName, boundary, opts)
	if err != nil {
		return nil, hwFailf("%v", err)
	}
	cmd := collect.RemoteCommand(osName)
	stdin := script
	if osName == collect.OSLinux {
		cmd = "sh -s"
		stdin = linuxWrapper(script, boundary, in.SudoPassword, in.AllowNoRoot)
	}

	b := &collect.Bundle{
		Format:  collect.BundleFormat,
		Tool:    "diagward " + collect.CollectorVersion + " (termward)",
		OS:      osName,
		Host:    h.Name,
		Options: opts,
	}
	progress := func(section string) {
		s.hub.Publish("hardware_progress", map[string]string{"hostId": h.ID, "section": section})
	}
	b.Started = time.Now()
	out, stderr, code, runErr := s.streamRun(ctx, h.ID, cmd, stdin, "==DW:"+boundary+":BEGIN ", progress)
	b.Finished = time.Now()

	ranAs, out := takeRanAs(out, boundary)
	b.Sections, b.Noise = collect.ParseFramed(out, boundary)
	if strings.TrimSpace(stderr) != "" && len(b.Noise) < 16<<10 {
		b.Noise = strings.TrimSpace(b.Noise + "\nstderr:\n" + tail(stderr, 4<<10))
	}

	complete := b.Get("meta.done") != nil
	if !complete && len(b.Sections) == 0 {
		switch {
		case runErr != nil && ctx.Err() != nil:
			return nil, ctx.Err()
		case runErr != nil:
			return nil, runErr
		case osName == collect.OSLinux && code == exitSudoRequired:
			return nil, &sudoError{}
		case osName == collect.OSLinux && code == exitSudoWrong:
			return nil, &sudoError{wrong: true}
		}
		msg := strings.TrimSpace(tail(stderr, 600))
		if msg == "" {
			msg = strings.TrimSpace(tail(b.Noise, 600))
		}
		if msg == "" {
			msg = fmt.Sprintf("the collector exited with status %d and printed nothing", code)
		}
		return nil, hwFailf("%s", msg)
	}
	if !complete && b.Get("meta.ident") == nil {
		// Not even the identity section: the output is not worth analysing.
		if runErr != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, hwFailf("the collector stopped right after it started%s", stderrSuffix(stderr))
	}

	rep := diag.Analyze(b)
	switch {
	case osName == collect.OSWindows && rep.Env.Root:
		ranAs = "admin"
	case osName == collect.OSWindows:
		ranAs = "user"
	case ranAs == "" && rep.Env.Root:
		ranAs = "root"
	case ranAs == "":
		ranAs = "user"
	}
	if !complete {
		whyEN, whyVI := "the connection was lost", "kết nối bị ngắt"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			mins := int(hardwareTimeout / time.Minute)
			whyEN, whyVI = fmt.Sprintf("it took longer than %d minutes", mins), fmt.Sprintf("quá %d phút", mins)
		}
		rep.Notes = append(rep.Notes, model.T(
			fmt.Sprintf("The check stopped early (%s): this report covers only the %d sections collected before that.", whyEN, len(b.Sections)),
			fmt.Sprintf("Quá trình kiểm tra dừng sớm (%s): báo cáo này chỉ gồm %d mục đã thu được trước đó.", whyVI, len(b.Sections)),
		))
	}
	return &HardwareResult{Report: rep, RanAs: ranAs, SavedAt: time.Now().UTC().Truncate(time.Second), Partial: !complete}, nil
}

// detectOS tells Linux from Windows: Windows OpenSSH starts cmd.exe or
// PowerShell, where uname is missing (or comes from MSYS/Cygwin).
func (s *Server) detectOS(ctx context.Context, hostID string) (string, error) {
	dctx, cancel := context.WithTimeout(ctx, hardwareDetectTO)
	defer cancel()
	res, err := s.pool.Run(dctx, hostID, "uname -s", nil)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(res.Stdout)
	upper := strings.ToUpper(name)
	switch {
	case res.ExitCode != 0 || name == "":
		return collect.OSWindows, nil
	case name == "Linux":
		return collect.OSLinux, nil
	case strings.HasPrefix(upper, "MINGW"), strings.HasPrefix(upper, "MSYS"), strings.HasPrefix(upper, "CYGWIN"):
		return collect.OSWindows, nil
	}
	return "", hwFailf("Diagward checks Linux and Windows servers; this one runs %s", firstLine(name))
}

// streamRun runs cmd with stdin on a fresh session, calling progress for each
// line that starts with marker as output streams in.
func (s *Server) streamRun(ctx context.Context, hostID, cmd, stdin, marker string, progress func(string)) (out, stderr string, code int, err error) {
	sess, err := s.pool.NewSession(ctx, hostID)
	if err != nil {
		return "", "", 0, err
	}
	defer sess.Close()
	sw := &markerWriter{marker: []byte(marker), progress: progress, max: hardwareMaxOutput}
	ew := &capWriter{max: hardwareMaxStderr}
	sess.Stdin = strings.NewReader(stdin)
	sess.Stdout = sw
	sess.Stderr = ew
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGTERM)
		_ = sess.Close() // hangs up: the wrapper's trap removes its temporary file
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		return sw.String(), ew.String(), -1, ctx.Err()
	}
	var exitErr *ssh.ExitError
	var missing *ssh.ExitMissingError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code, err = exitErr.ExitStatus(), nil
	case errors.As(err, &missing):
		code, err = -1, nil
	}
	return sw.String(), ew.String(), code, err
}

// linuxWrapper writes the collector to a private temporary file and runs it
// as root: directly, through passwordless sudo, through sudo with the given
// password, or (only when allowed) as the login user. It exits 77 when root
// is needed and 78 when the sudo password is wrong, like powerScript. The
// password never touches the disk.
func linuxWrapper(collector, boundary, sudoPassword string, allowNoRoot bool) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	delim := "TW_HW_" + randomHex(16)
	for strings.Contains(collector, delim) { // cannot happen, but never let the heredoc end early
		delim = "TW_HW_" + randomHex(16)
	}
	noRoot := "0"
	if allowNoRoot {
		noRoot = "1"
	}
	var b strings.Builder
	b.WriteString(`PW=` + q(sudoPassword) + `
NOROOT=` + noRoot + `
M=` + q("==TW:"+boundary+":AS") + `
umask 077
F=$(mktemp "${TMPDIR:-/tmp}/termward-hw.XXXXXXXXXX" 2>/dev/null)
if [ -z "$F" ] || [ ! -f "$F" ]; then
	F="/tmp/termward-hw.$$"
	(set -C; : >"$F") 2>/dev/null || { echo "termward: cannot create a temporary file" >&2; exit 1; }
fi
trap 'rm -f "$F"' EXIT
trap 'rm -f "$F"; exit 130' INT TERM HUP
cat >"$F" <<'` + delim + `'
`)
	b.WriteString(collector)
	if !strings.HasSuffix(collector, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(delim + `
if [ "$(id -u)" = 0 ]; then
	echo "$M root"
	sh "$F" </dev/null
elif command -v sudo >/dev/null 2>&1 && sudo -n true </dev/null >/dev/null 2>&1; then
	echo "$M sudo"
	sudo -n sh "$F" </dev/null
elif [ -n "$PW" ] && command -v sudo >/dev/null 2>&1; then
	printf '%s\n' "$PW" | sudo -S -k -p '' true >/dev/null 2>&1 || exit 78
	echo "$M sudo"
	printf '%s\n' "$PW" | sudo -S -k -p '' sh "$F"
elif [ "$NOROOT" = 1 ]; then
	echo "$M user"
	sh "$F" </dev/null
else
	exit 77
fi
`)
	return b.String()
}

// takeRanAs removes the wrapper's "ran as" marker from the output.
func takeRanAs(out, boundary string) (string, string) {
	pfx := "==TW:" + boundary + ":AS "
	i := strings.Index(out, pfx)
	if i < 0 || (i > 0 && out[i-1] != '\n') {
		return "", out
	}
	end := strings.IndexByte(out[i:], '\n')
	if end < 0 {
		end = len(out) - i
	}
	who := strings.TrimSpace(out[i+len(pfx) : i+end])
	rest := out[:i] + strings.TrimPrefix(out[i+end:], "\n")
	switch who {
	case "root", "sudo", "user":
		return who, rest
	}
	return "", rest
}

// ---------------------------------------------------------------- storage

var hostIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// hardwarePath is where a host's last result is kept. Host IDs come from the
// URL, so anything that is not a plain token is refused.
func hardwarePath(dataDir, hostID string) (string, error) {
	if !hostIDRe.MatchString(hostID) {
		return "", fmt.Errorf("invalid host id")
	}
	return filepath.Join(dataDir, "hardware", hostID+".json"), nil
}

func saveHardware(dataDir, hostID string, res *HardwareResult) error {
	p, err := hardwarePath(dataDir, hostID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

func loadHardware(dataDir, hostID string) (*HardwareResult, error) {
	p, err := hardwarePath(dataDir, hostID)
	if err != nil {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var res HardwareResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	if res.Report == nil {
		return nil, errors.New("the saved result is empty")
	}
	return &res, nil
}

func removeHardware(dataDir, hostID string) {
	if p, err := hardwarePath(dataDir, hostID); err == nil {
		_ = os.Remove(p)
	}
}

// ---------------------------------------------------------------- helpers

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeFileName turns a host name into something every OS accepts in a file
// name (and that cannot break the Content-Disposition header).
func safeFileName(s string) string {
	s = unsafeName.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if len(s) > 64 {
		s = strings.TrimRight(s[:64], "-.")
	}
	if s == "" {
		return "host"
	}
	return s
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func stderrSuffix(stderr string) string {
	if t := strings.TrimSpace(tail(stderr, 400)); t != "" {
		return ": " + t
	}
	return ""
}

// markerWriter keeps the output (capped) and reports each marker line as it
// arrives.
type markerWriter struct {
	marker   []byte
	progress func(string)
	max      int

	buf     bytes.Buffer
	scanned int // bytes of buf already searched for complete lines
}

func (m *markerWriter) Write(p []byte) (int, error) {
	n := len(p)
	if room := m.max - m.buf.Len(); room < len(p) {
		if room <= 0 {
			return n, nil
		}
		p = p[:room]
	}
	m.buf.Write(p)
	data := m.buf.Bytes()
	for {
		i := bytes.IndexByte(data[m.scanned:], '\n')
		if i < 0 {
			break
		}
		line := data[m.scanned : m.scanned+i]
		m.scanned += i + 1
		if m.progress != nil && bytes.HasPrefix(line, m.marker) {
			m.progress(strings.TrimSpace(string(line[len(m.marker):])))
		}
	}
	return n, nil
}

func (m *markerWriter) String() string { return m.buf.String() }

type capWriter struct {
	max int
	buf bytes.Buffer
}

func (c *capWriter) Write(p []byte) (int, error) {
	n := len(p)
	if room := c.max - c.buf.Len(); room < len(p) {
		if room <= 0 {
			return n, nil
		}
		p = p[:room]
	}
	c.buf.Write(p)
	return n, nil
}

func (c *capWriter) String() string { return c.buf.String() }

var _ io.Writer = (*markerWriter)(nil)
