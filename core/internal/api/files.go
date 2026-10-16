package api

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/sftp"
)

// fileEntry is one row in an SFTP directory listing. Paths are POSIX (the
// remote is assumed to be a Unix host, like the rest of Termward's checks).
type fileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	Mtime   int64  `json:"mtime"` // unix seconds
	Mode    string `json:"mode"`  // e.g. "-rw-r--r--"
	Symlink bool   `json:"symlink"`
}

// sftp opens an SFTP subsystem over the host's pooled SSH connection. The
// caller must Close the returned client. The underlying ssh.Client is shared
// and kept alive by the pool; only the SFTP channel is per-request.
func (s *Server) sftp(r *http.Request, hostID string) (*sftp.Client, error) {
	cl, err := s.pool.Client(r.Context(), hostID)
	if err != nil {
		return nil, err
	}
	return sftp.NewClient(cl)
}

// GET /api/hosts/{id}/files?path=  — list a directory (defaults to the home dir).
func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	sc, err := s.sftp(r, r.PathValue("id"))
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	defer sc.Close()

	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		if wd, err := sc.Getwd(); err == nil && wd != "" {
			p = wd
		} else {
			p = "/"
		}
	}
	p = path.Clean(p)

	infos, err := sc.ReadDir(p)
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	entries := make([]fileEntry, 0, len(infos))
	for _, fi := range infos {
		entries = append(entries, entryOf(sc, path.Join(p, fi.Name()), fi))
	}
	sortEntries(entries)
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "entries": entries})
}

// GET /api/hosts/{id}/files/content?path=  — stream a file as a download.
func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	p := path.Clean(strings.TrimSpace(r.URL.Query().Get("path")))
	if p == "" || p == "." || p == "/" {
		writeError(w, http.StatusBadRequest, "invalid", "a file path is required", nil)
		return
	}
	sc, err := s.sftp(r, r.PathValue("id"))
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	defer sc.Close()

	st, err := sc.Stat(p)
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	if st.IsDir() {
		writeError(w, http.StatusBadRequest, "invalid", "path is a directory", nil)
		return
	}
	f, err := sc.Open(p)
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	w.Header().Set("Content-Disposition", contentDisposition(path.Base(p)))
	_, _ = io.Copy(w, f)
}

// POST /api/hosts/{id}/files/upload?path=  — stream the request body into a
// remote file (created or overwritten). The body is NOT size-capped here, so
// large files work; the request context cancels a stalled transfer.
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	p := path.Clean(strings.TrimSpace(r.URL.Query().Get("path")))
	if p == "" || p == "." || p == "/" {
		writeError(w, http.StatusBadRequest, "invalid", "a destination path is required", nil)
		return
	}
	sc, err := s.sftp(r, r.PathValue("id"))
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	defer sc.Close()

	f, err := sc.Create(p) // truncates if it already exists (the UI confirms overwrite)
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	n, err := io.Copy(f, r.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": p, "size": n})
}

// POST /api/hosts/{id}/files/mkdir  {path}
func (s *Server) mkdirFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &in) {
		return
	}
	p := path.Clean(strings.TrimSpace(in.Path))
	if p == "" || p == "." || p == "/" {
		writeError(w, http.StatusBadRequest, "invalid", "a folder path is required", nil)
		return
	}
	sc, err := s.sftp(r, r.PathValue("id"))
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	defer sc.Close()
	if err := sc.MkdirAll(p); err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": p})
}

// POST /api/hosts/{id}/files/rename  {from, to}
func (s *Server) renameFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decode(w, r, &in) {
		return
	}
	from := path.Clean(strings.TrimSpace(in.From))
	to := path.Clean(strings.TrimSpace(in.To))
	if from == "" || from == "." || from == "/" || to == "" || to == "." || to == "/" {
		writeError(w, http.StatusBadRequest, "invalid", "both from and to paths are required", nil)
		return
	}
	sc, err := s.sftp(r, r.PathValue("id"))
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	defer sc.Close()
	if _, err := sc.Lstat(to); err == nil {
		writeError(w, http.StatusConflict, "exists", "a file with that name already exists", nil)
		return
	}
	if err := sc.Rename(from, to); err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": to})
}

// POST /api/hosts/{id}/files/delete  {path, recursive}
func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if !decode(w, r, &in) {
		return
	}
	p := path.Clean(strings.TrimSpace(in.Path))
	if p == "" || p == "." || p == "/" {
		writeError(w, http.StatusBadRequest, "invalid", "a path is required", nil)
		return
	}
	sc, err := s.sftp(r, r.PathValue("id"))
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	defer sc.Close()
	if err := removeAll(sc, p, in.Recursive); err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ------------------------------------------------------------ helpers

// entryOf builds a listing row. A symlink is reported as such, but its target
// is resolved (Stat) so the UI knows whether the link leads to a directory and
// can be entered.
func entryOf(sc *sftp.Client, full string, fi os.FileInfo) fileEntry {
	e := fileEntry{
		Name:  fi.Name(),
		Path:  full,
		IsDir: fi.IsDir(),
		Size:  fi.Size(),
		Mtime: fi.ModTime().Unix(),
		Mode:  fi.Mode().String(),
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		e.Symlink = true
		if st, err := sc.Stat(full); err == nil {
			e.IsDir = st.IsDir()
			e.Size = st.Size()
		}
	}
	return e
}

// sortEntries orders directories first, then by case-insensitive name.
func sortEntries(entries []fileEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}

// removeAll deletes a file, a symlink (the link itself, not its target), or a
// directory. A non-empty directory needs recursive=true.
func removeAll(sc *sftp.Client, p string, recursive bool) error {
	li, err := sc.Lstat(p)
	if err != nil {
		return err
	}
	if li.IsDir() {
		if recursive {
			children, err := sc.ReadDir(p)
			if err != nil {
				return err
			}
			for _, c := range children {
				if err := removeAll(sc, path.Join(p, c.Name()), true); err != nil {
					return err
				}
			}
		}
		return sc.RemoveDirectory(p)
	}
	return sc.Remove(p)
}

// contentDisposition builds an attachment header that survives non-ASCII names
// (e.g. Vietnamese filenames): an ASCII fallback plus an RFC 5987 UTF-8 copy.
func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(name))
}
