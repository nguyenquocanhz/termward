package cloud

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Request timeouts. The server limits the fan-out of one /v1/alerts request
// to about 20 s and asks clients to wait at least 30 s (disconnecting earlier
// cancels the delivery on the server).
const (
	defaultTimeout = 15 * time.Second
	alertsTimeout  = 30 * time.Second
	maxResponse    = 1 << 20
)

// ResolveBaseURL returns the server URL: DefaultBaseURL, or the value of
// TERMWARD_CLOUD_URL when it is set and acceptable (https anywhere; plain
// http only to this machine, for a local `wrangler dev`). The URL must be an
// origin without a path, since the server signs the path it sees.
// The value is not repeated in errors (they are logged): it may carry
// credentials or a token.
func ResolveBaseURL(env string) (string, error) {
	env = strings.TrimSpace(env)
	if env == "" {
		return DefaultBaseURL, nil
	}
	u, err := url.Parse(env)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return DefaultBaseURL, errNotOrigin
	}
	switch u.Scheme {
	case "https":
	case "http":
		if h := u.Hostname(); h != "127.0.0.1" && h != "localhost" && h != "::1" {
			return DefaultBaseURL, fmt.Errorf("TERMWARD_CLOUD_URL may use http only for 127.0.0.1 or localhost; using %s", DefaultBaseURL)
		}
	default:
		return DefaultBaseURL, errNotOrigin
	}
	return u.Scheme + "://" + u.Host, nil
}

var errNotOrigin = fmt.Errorf("TERMWARD_CLOUD_URL is not an http(s) origin (no user, path, query or fragment); using %s", DefaultBaseURL)

// newHTTPClient verifies TLS certificates (it never sets InsecureSkipVerify)
// and does not follow redirects: a signed request is only valid for the path
// it was signed for, and nothing should send it elsewhere.
func newHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// creds sign requests for one registered device.
type creds struct {
	deviceID string
	machine  string
	key      ed25519.PrivateKey
}

// client speaks the wire protocol; it knows nothing about the account state.
type client struct {
	base string
	http *http.Client
	now  func() time.Time

	mu     sync.Mutex
	offset time.Duration // server clock minus ours, learned from clock_skew
	nonce  func() []byte // tests only
}

func (c *client) serverNow() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now().Add(c.offset)
}

func (c *client) newNonce() []byte {
	if c.nonce != nil {
		return c.nonce()
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b
}

// do sends one request. in (when not nil) is sent as JSON; out receives a
// 2xx JSON answer. A signed request answered with 401 clock_skew is retried
// once with the server's clock; one answered with replay, once with a fresh
// nonce (the server keeps nonces per key for 10 minutes, so only a
// reused random nonce or a retried request can hit it).
func (c *client) do(ctx context.Context, method, path string, in any, cr *creds, out any, timeout time.Duration) error {
	body, err := jsonBody(in)
	if err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	skewRetried, replayRetried := false, false
	for {
		err := c.once(ctx, method, path, body, cr, out, timeout)
		var ce *Error
		if cr == nil || !errors.As(err, &ce) || ce.Status != http.StatusUnauthorized {
			return err
		}
		switch {
		case ce.Code == "clock_skew" && ce.ServerTime > 0 && !skewRetried:
			skewRetried = true
			c.mu.Lock()
			c.offset = time.Unix(ce.ServerTime, 0).Sub(c.now())
			c.mu.Unlock()
		case ce.Code == "replay" && !replayRetried:
			replayRetried = true
		default:
			return err
		}
	}
}

// jsonBody is the exact request body (nil when there is none): these bytes
// are both hashed for the signature and sent.
func jsonBody(in any) ([]byte, error) {
	if in == nil {
		return nil, nil
	}
	return json.Marshal(in)
}

func (c *client) once(ctx context.Context, method, path string, body []byte, cr *creds, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if cr != nil {
		s := Sign(cr.key, method, req.URL.RequestURI(), cr.deviceID, cr.machine, body, c.serverNow(), c.newNonce())
		req.Header.Set(HeaderDevice, s.Device)
		req.Header.Set(HeaderMachine, s.Machine)
		req.Header.Set(HeaderTime, s.Time)
		req.Header.Set(HeaderNonce, s.Nonce)
		req.Header.Set(HeaderSignature, s.Signature)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return unreachable(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	if err != nil {
		return unreachable(err)
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		if out != nil && len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return &Error{Status: http.StatusBadGateway, Code: "bad_response", Message: "unexpected answer from Termward Cloud"}
			}
		}
		return nil
	}
	e := &Error{}
	if json.Unmarshal(raw, e) != nil || e.Code == "" {
		e = &Error{Code: statusCode(res.StatusCode), Message: fmt.Sprintf("Termward Cloud answered HTTP %d", res.StatusCode)}
	}
	e.Status = res.StatusCode
	e.Field = "" // never trust a server-provided field name for local UI hints
	if e.RetryAfter == 0 {
		if v, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && v > 0 {
			e.RetryAfter = v
		}
	}
	// The longest wait the contract gives is until UTC midnight; more would
	// also overflow time.Duration and turn the send loop into a busy loop.
	e.RetryAfter = min(max(e.RetryAfter, 0), maxRetryAfter)
	return e
}

// maxRetryAfter caps a server-provided retryAfter, in seconds.
const maxRetryAfter = 24 * 60 * 60

func statusCode(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return "rate_limited"
	case status >= 500:
		return "internal"
	case status == http.StatusNotFound:
		return "not_found"
	}
	return "error"
}

func unreachable(err error) *Error {
	msg := "cannot reach Termward Cloud"
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		msg = "Termward Cloud did not answer in time"
	}
	return &Error{Status: http.StatusBadGateway, Code: "cloud_unreachable", Message: msg}
}

// retryable reports whether a failure may succeed later without any change:
// network problems, timeouts and server errors.
func retryable(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return true
	}
	return e.Code == "cloud_unreachable" || e.Status >= 500
}
