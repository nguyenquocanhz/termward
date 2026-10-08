package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/nguyenquocanhz/termward/core/internal/cloud"
)

// Termward Pro: the UI talks to the core, the core to Termward Cloud. No
// response here carries the device key or a channel's token/webhook URL
// (the core never keeps the latter at all).

func (s *Server) cloudRoutes(mux *http.ServeMux) {
	h := func(pattern string, fn http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if s.cloud == nil {
				writeError(w, http.StatusServiceUnavailable, "cloud_disabled", "Termward Pro is not available in this build", nil)
				return
			}
			fn(w, r)
		})
	}
	h("GET /api/cloud/status", s.cloudStatus)
	h("POST /api/cloud/refresh", s.cloudRefresh)
	h("POST /api/cloud/signin/start", s.cloudSignInStart)
	h("POST /api/cloud/signin/verify", s.cloudSignInVerify)
	h("POST /api/cloud/signin/replace", s.cloudSignInReplace)
	h("POST /api/cloud/signout", s.cloudSignOut)
	h("POST /api/cloud/notice/dismiss", s.cloudDismissNotice)
	h("POST /api/cloud/checkout", s.cloudCheckout)
	h("GET /api/cloud/orders/{code}", s.cloudOrder)
	h("DELETE /api/cloud/order", s.cloudStopWaiting)
	h("GET /api/cloud/devices", s.cloudDevices)
	h("DELETE /api/cloud/devices/{id}", s.cloudRevokeDevice)
	h("GET /api/cloud/channels", s.cloudChannels)
	h("POST /api/cloud/channels", s.cloudAddChannel)
	h("DELETE /api/cloud/channels/{id}", s.cloudDeleteChannel)
	h("POST /api/cloud/channels/{id}/test", s.cloudTestChannel)
	h("GET /api/cloud/forwarding", s.cloudForwarding)
	h("PUT /api/cloud/forwarding", s.cloudSetForwarding)
}

// cloudFail turns a cloud error into the core's error format, keeping the
// server's error code (device_limit, invalid_code, plan_inactive, …) and the
// fields the UI needs.
func cloudFail(w http.ResponseWriter, err error) {
	var ce *cloud.Error
	if !errors.As(err, &ce) {
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	status := ce.Status
	switch {
	case status == http.StatusUnauthorized:
		status = http.StatusForbidden // 401 means "bad core token" to the UI
	case status < 400 || status > 599:
		status = http.StatusBadGateway
	}
	d := map[string]any{}
	if ce.Field != "" {
		d["field"] = ce.Field
	}
	if ce.Max > 0 {
		d["max"] = ce.Max
	}
	if ce.Devices != nil {
		d["devices"] = ce.Devices
	}
	if ce.ReplaceToken != "" {
		d["replaceToken"] = ce.ReplaceToken
	}
	if ce.RetryAfter > 0 {
		d["retryAfter"] = ce.RetryAfter
	}
	if ce.PaidUntil != "" {
		d["paidUntil"] = ce.PaidUntil
	}
	if ce.Limit > 0 {
		d["limit"], d["usedToday"] = ce.Limit, ce.UsedToday
	}
	var details any
	if len(d) > 0 {
		details = d
	}
	writeError(w, status, ce.Code, ce.Message, details)
}

func (s *Server) cloudStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cloud.Status())
}

func (s *Server) cloudRefresh(w http.ResponseWriter, r *http.Request) {
	st, err := s.cloud.Refresh(r.Context())
	if err != nil {
		cloudFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) cloudSignInStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Lang  string `json:"lang"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.cloud.SignInStart(r.Context(), in.Email, in.Lang); err != nil {
		cloudFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) cloudSignInVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	st, err := s.cloud.SignInVerify(r.Context(), in.Email, in.Code)
	if err != nil {
		cloudFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) cloudSignInReplace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ReplaceToken   string `json:"replaceToken"`
		RevokeDeviceID string `json:"revokeDeviceId"`
	}
	if !decode(w, r, &in) {
		return
	}
	st, err := s.cloud.SignInReplace(r.Context(), in.ReplaceToken, in.RevokeDeviceID)
	if err != nil {
		cloudFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) cloudSignOut(w http.ResponseWriter, r *http.Request) {
	revoked, err := s.cloud.SignOut(r.Context())
	if err != nil {
		cloudFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked, "status": s.cloud.Status()})
}

func (s *Server) cloudDismissNotice(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cloud.DismissNotice())
}

func (s *Server) cloudCheckout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Months int `json:"months"`
	}
	if !decode(w, r, &in) {
		return
	}
	o, err := s.cloud.Checkout(r.Context(), in.Months)
	if err != nil {
		cloudFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": o, "status": s.cloud.Status()})
}

func (s *Server) cloudOrder(w http.ResponseWriter, r *http.Request) {
	code, err := strconv.ParseInt(r.PathValue("code"), 10, 64)
	if err != nil || code <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "unknown order", nil)
		return
	}
	o, err := s.cloud.OrderStatus(r.Context(), code)
	if err != nil {
		cloudFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) cloudStopWaiting(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cloud.StopWaiting())
}

func (s *Server) cloudDevices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cloud.Status().Devices)
}

func (s *Server) cloudRevokeDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.cloud.RevokeDevice(r.Context(), r.PathValue("id")); err != nil {
		cloudFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.cloud.Status())
}

func (s *Server) cloudChannels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cloud.Status().Channels)
}

func (s *Server) cloudAddChannel(w http.ResponseWriter, r *http.Request) {
	var in cloud.ChannelInput
	if !decode(w, r, &in) {
		return
	}
	ch, err := s.cloud.AddChannel(r.Context(), in)
	if err != nil {
		cloudFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"channel": ch, "status": s.cloud.Status()})
}

func (s *Server) cloudDeleteChannel(w http.ResponseWriter, r *http.Request) {
	if err := s.cloud.DeleteChannel(r.Context(), r.PathValue("id")); err != nil {
		cloudFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.cloud.Status())
}

func (s *Server) cloudTestChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Lang string `json:"lang"`
	}
	if !decodeOptional(w, r, &in) {
		return
	}
	res, err := s.cloud.TestChannel(r.Context(), r.PathValue("id"), in.Lang)
	if err != nil {
		cloudFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) cloudForwarding(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cloud.Status().Forwarding)
}

func (s *Server) cloudSetForwarding(w http.ResponseWriter, r *http.Request) {
	var in cloud.Forwarding
	if !decode(w, r, &in) {
		return
	}
	writeJSON(w, http.StatusOK, s.cloud.SetForwarding(in))
}
