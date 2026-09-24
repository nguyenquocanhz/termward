package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nguyenquocanhz/termward/core/internal/sshx"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

var jobIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

type execEvent struct {
	JobID  string       `json:"jobId"`
	HostID string       `json:"hostId"`
	State  string       `json:"state"` // running | done | error
	Result *sshx.Result `json:"result,omitempty"`
	Error  string       `json:"error,omitempty"`
}

// startExec runs one command on many hosts in parallel. Progress is streamed
// as "exec" events; the caller may pick the job id so it can match events that
// arrive before this response does.
func (s *Server) startExec(w http.ResponseWriter, r *http.Request) {
	var in struct {
		JobID      string   `json:"jobId"`
		HostIDs    []string `json:"hostIds"`
		Command    string   `json:"command"`
		TimeoutSec int      `json:"timeoutSec"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Command) == "" || len(in.HostIDs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid", "choose at least one host and enter a command", nil)
		return
	}
	for _, id := range in.HostIDs {
		if _, err := s.store.Host(id); err != nil {
			fail(w, err, http.StatusBadRequest)
			return
		}
	}
	if !jobIDPattern.MatchString(in.JobID) {
		in.JobID = store.NewID()
	}
	timeout := time.Duration(min(max(in.TimeoutSec, 5), 3600)) * time.Second
	if in.TimeoutSec == 0 {
		timeout = 60 * time.Second
	}

	ctx, cancel := context.WithCancel(s.ctx)
	s.jobs.Store(in.JobID, cancel)
	go func() {
		defer s.jobs.Delete(in.JobID)
		defer cancel()
		sem := make(chan struct{}, 16)
		var wg sync.WaitGroup
		for _, id := range in.HostIDs {
			wg.Add(1)
			go func(hostID string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				s.hub.Publish("exec", execEvent{JobID: in.JobID, HostID: hostID, State: "running"})
				hctx, hcancel := context.WithTimeout(ctx, timeout)
				defer hcancel()
				res, err := s.pool.Run(hctx, hostID, in.Command, nil)
				ev := execEvent{JobID: in.JobID, HostID: hostID, State: "done", Result: &res}
				if err != nil {
					ev.State, ev.Result, ev.Error = "error", nil, err.Error()
				}
				s.hub.Publish("exec", ev)
			}(id)
		}
		wg.Wait()
		s.hub.Publish("exec_done", map[string]string{"jobId": in.JobID})
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": in.JobID})
}

func (s *Server) cancelExec(w http.ResponseWriter, r *http.Request) {
	if c, ok := s.jobs.Load(r.PathValue("id")); ok {
		c.(context.CancelFunc)()
	}
	w.WriteHeader(http.StatusNoContent)
}
