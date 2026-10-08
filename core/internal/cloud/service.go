package cloud

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nguyenquocanhz/termward/core/internal/health"
)

const (
	refreshEvery     = 3 * time.Hour
	orderPollFor     = 30 * time.Minute
	alertInbox       = 256
	signOutTimeout   = 10 * time.Second
	refreshRetryBase = time.Minute
	refreshRetryMax  = 30 * time.Minute
	sendRetryMax     = 10 * time.Minute
)

// Variables so tests can run the timers faster.
var (
	orderPollFirst = 3 * time.Second
	orderPollMax   = 30 * time.Second
	sendRetryBase  = 5 * time.Second
	// After alerts were delivered the account is re-read (today's usage,
	// each channel's last result), at most this often.
	sentRefreshGap = time.Minute
)

// Why the app signed itself out (shown once in the UI).
const (
	ReasonRevoked  = "revoked"  // the device was removed from the account
	ReasonMismatch = "mismatch" // the machine fingerprint no longer matches
	ReasonKeyLost  = "key_lost" // the device key vanished from the secret store
)

// Config wires a Service. Zero values pick production defaults.
type Config struct {
	DataDir string
	Secrets Secrets
	Version string
	// MachineID is passed by the mobile apps (ANDROID_ID,
	// identifierForVendor); desktop builds read the OS value.
	MachineID string
	// DeviceName is shown in the device list (default: the host name).
	DeviceName string
	// BaseURL defaults to TERMWARD_CLOUD_URL or DefaultBaseURL.
	BaseURL string
	// HTTPClient replaces the default client (tests).
	HTTPClient *http.Client
	// Host resolves a host id to its address for alert events.
	Host func(hostID string) (address string, ok bool)
	// OnChange is called (outside any lock) whenever the status changes.
	OnChange func(Status)
	Logf     func(format string, args ...any)
	Now      func() time.Time
	// SystemMachineID replaces the OS reader (tests).
	SystemMachineID func() (string, error)
	// Platform overrides the platform reported to the server (tests).
	Platform string
}

// PendingOrder is the checkout the app is waiting for.
type PendingOrder struct {
	Order
	StartedAt time.Time `json:"startedAt"`
	// Waiting is true while the app polls the order (at most 30 minutes).
	Waiting bool `json:"waiting"`
}

// state is cached in <data dir>/cloud/state.json. It holds nothing secret:
// the device key lives in the secret store and channel configs only on the
// server.
type state struct {
	// InstallID names this install's device key in the secret store.
	InstallID string `json:"installId"`

	Email        string    `json:"email,omitempty"`
	AccountID    string    `json:"accountId,omitempty"`
	DeviceID     string    `json:"deviceId,omitempty"`
	DeviceName   string    `json:"deviceName,omitempty"`
	Plan         *Plan     `json:"plan,omitempty"`
	PlanInactive bool      `json:"planInactive,omitempty"`
	Devices      []Device  `json:"devices,omitempty"`
	Channels     []Channel `json:"channels,omitempty"`
	Prices       []Price   `json:"prices,omitempty"`
	Limits       *Limits   `json:"limits,omitempty"`
	RefreshedAt  time.Time `json:"refreshedAt,omitzero"`

	Forwarding Forwarding `json:"forwarding"`
	LangChosen bool       `json:"langChosen,omitempty"`

	Order           *PendingOrder `json:"order,omitempty"`
	SignedOutReason string        `json:"signedOutReason,omitempty"`
	LastSentAt      time.Time     `json:"lastSentAt,omitzero"`
}

// Status is what the UI sees. It never contains key material or channel
// configs.
type Status struct {
	SignedIn        bool          `json:"signedIn"`
	Email           string        `json:"email,omitempty"`
	DeviceID        string        `json:"deviceId,omitempty"`
	DeviceName      string        `json:"deviceName,omitempty"`
	Plan            *Plan         `json:"plan,omitempty"`
	PlanInactive    bool          `json:"planInactive"`
	Devices         []Device      `json:"devices"`
	Channels        []Channel     `json:"channels"`
	Prices          []Price       `json:"prices"`
	Limits          *Limits       `json:"limits,omitempty"`
	RefreshedAt     *time.Time    `json:"refreshedAt,omitempty"`
	LastError       string        `json:"lastError,omitempty"`
	Forwarding      Forwarding    `json:"forwarding"`
	Queue           QueueStatus   `json:"queue"`
	Order           *PendingOrder `json:"order,omitempty"`
	SignedOutReason string        `json:"signedOutReason,omitempty"`
	MachineSource   string        `json:"machineSource,omitempty"`
	Server          string        `json:"server"`
}

type QueueStatus struct {
	Pending    int        `json:"pending"`
	LastSentAt *time.Time `json:"lastSentAt,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
	RetryAt    *time.Time `json:"retryAt,omitempty"`
}

// Service is the Termward Pro client. All methods are safe for concurrent use.
type Service struct {
	c        *client
	sec      Secrets
	dir      string
	version  string
	platform string
	devName  string
	native   string
	sysID    func() (string, error)
	host     func(string) (string, bool)
	onChange func(Status)
	logf     func(string, ...any)
	now      func() time.Time

	mu         sync.Mutex
	st         state
	q          *queue
	key        ed25519.PrivateKey
	machine    string
	machineSrc string
	lastErr    string
	sendErr    string
	retryAt    time.Time
	sendFails  int
	ctx        context.Context
	stopOrder  context.CancelFunc

	in          chan health.Alert
	kickSend    chan struct{}
	kickRefresh chan struct{}
}

// New loads the cached state and queue. It does no network I/O; Run does.
func New(cfg Config) (*Service, error) {
	if cfg.Secrets == nil {
		return nil, errors.New("cloud: secret store is required")
	}
	logf := cfg.Logf
	if logf == nil {
		logf = log.Printf
	}
	base := cfg.BaseURL
	if base == "" {
		b, err := ResolveBaseURL(os.Getenv("TERMWARD_CLOUD_URL"))
		if err != nil {
			logf("termward cloud: %v", err)
		}
		base = b
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = newHTTPClient()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	sysID := cfg.SystemMachineID
	if sysID == nil {
		sysID = systemMachineID
	}
	base = strings.TrimRight(base, "/")
	s := &Service{
		c:           &client{base: base, http: hc, now: now},
		sec:         cfg.Secrets,
		version:     appVersion(cfg.Version),
		platform:    platformName(cfg.Platform),
		devName:     deviceName(cfg.DeviceName),
		native:      cfg.MachineID,
		sysID:       sysID,
		host:        cfg.Host,
		onChange:    cfg.OnChange,
		logf:        logf,
		now:         now,
		in:          make(chan health.Alert, alertInbox),
		kickSend:    make(chan struct{}, 1),
		kickRefresh: make(chan struct{}, 1),
	}
	s.st.Forwarding = DefaultForwarding()
	if cfg.DataDir != "" {
		s.dir = stateDir(cfg.DataDir, base)
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return nil, err
		}
		if b, err := os.ReadFile(s.statePath()); err == nil {
			var st state
			if json.Unmarshal(b, &st) == nil {
				s.st = st
				if s.st.Forwarding.Lang == "" {
					s.st.Forwarding = DefaultForwarding()
				}
			}
		}
		s.q = loadQueue(filepath.Join(s.dir, "queue.json"), now())
	} else {
		s.q = &queue{}
	}
	if s.st.InstallID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		s.st.InstallID = hex.EncodeToString(b)
		s.saveLocked()
	}
	return s, nil
}

// stateDir keeps each server's account apart: the production server uses
// <data dir>/cloud, any other (TERMWARD_CLOUD_URL) a directory of its own, so
// it gets its own install id (hence device key), state and queue. Sharing them
// would send another server signed requests of the production device (the host
// is not signed, so they could be replayed there for 300 s), and that server's
// device_revoked would delete the production key.
func stateDir(dataDir, base string) string {
	dir := filepath.Join(dataDir, "cloud")
	if base == DefaultBaseURL {
		return dir
	}
	sum := sha256.Sum256([]byte(base))
	return filepath.Join(dir, "servers", hex.EncodeToString(sum[:8]))
}

// keyName is the secret store name of this install's device key.
func (s *Service) keyName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return deviceKeyName(s.st.InstallID)
}

func (s *Service) statePath() string { return filepath.Join(s.dir, "state.json") }

func (s *Service) saveLocked() {
	if s.dir == "" {
		return
	}
	b, err := json.MarshalIndent(s.st, "", "  ")
	if err == nil {
		err = writeFileAtomic(s.statePath(), b)
	}
	if err != nil {
		s.logf("termward cloud: save state: %v", err)
	}
}

func (s *Service) saveQueueLocked() {
	if err := s.q.save(); err != nil {
		s.logf("termward cloud: save queue: %v", err)
	}
}

func (s *Service) notify() {
	if s.onChange != nil {
		s.onChange(s.Status())
	}
}

func kick(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Status returns a snapshot for the UI.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	out := Status{
		SignedIn:        st.DeviceID != "",
		Email:           st.Email,
		DeviceID:        st.DeviceID,
		DeviceName:      st.DeviceName,
		PlanInactive:    st.PlanInactive,
		Devices:         append([]Device{}, st.Devices...),
		Channels:        append([]Channel{}, st.Channels...),
		Prices:          append([]Price{}, st.Prices...),
		LastError:       s.lastErr,
		Forwarding:      st.Forwarding,
		SignedOutReason: st.SignedOutReason,
		MachineSource:   s.machineSrc,
		Server:          s.c.base,
		Queue:           QueueStatus{Pending: len(s.q.items), LastError: s.sendErr},
	}
	if st.Plan != nil {
		p := *st.Plan
		out.Plan = &p
	}
	if st.Limits != nil {
		l := *st.Limits
		out.Limits = &l
	}
	if !st.RefreshedAt.IsZero() {
		t := st.RefreshedAt
		out.RefreshedAt = &t
	}
	if !st.LastSentAt.IsZero() {
		t := st.LastSentAt
		out.Queue.LastSentAt = &t
	}
	if s.retryAt.After(s.now()) {
		t := s.retryAt
		out.Queue.RetryAt = &t
	}
	if st.Order != nil {
		o := *st.Order
		out.Order = &o
	}
	return out
}

// ------------------------------------------------------------ identity

func platformName(override string) string {
	if override != "" {
		return override
	}
	switch runtime.GOOS {
	case "windows", "linux", "android", "ios":
		return runtime.GOOS
	case "darwin":
		return "macos"
	}
	return "linux"
}

var versionRE = regexp.MustCompile(`^[0-9A-Za-z.+_-]{1,32}$`)

func appVersion(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if !versionRE.MatchString(v) {
		return "dev"
	}
	return v
}

func deviceName(n string) string {
	if n = cleanName(n, 64); n != "" {
		return n
	}
	h, _ := os.Hostname()
	return cleanName(h, 64)
}

// machineFingerprint resolves (once) the machine fingerprint.
func (s *Service) machineFingerprint() (string, error) {
	s.mu.Lock()
	if s.machine != "" {
		m := s.machine
		s.mu.Unlock()
		return m, nil
	}
	s.mu.Unlock()
	id, src, err := resolveMachineID(s.native, s.sysID, s.sec)
	if err != nil {
		return "", err
	}
	if src == MachineGenerated {
		s.logf("termward cloud: no machine id from the OS; using a generated one kept in the secret store")
	}
	fp := Fingerprint(id)
	s.mu.Lock()
	s.machine, s.machineSrc = fp, src
	s.mu.Unlock()
	return fp, nil
}

func (s *Service) deviceInfo(key ed25519.PrivateKey, machine, subject string) DeviceInfo {
	pub := PublicKeyString(key)
	name := s.devName
	if name == "" {
		name = s.platform
	}
	return DeviceInfo{
		PublicKey: pub, Machine: machine, Name: name, Platform: s.platform,
		AppVersion: s.version, Proof: Proof(key, subject, pub, machine),
	}
}

var errNotSignedIn = &Error{Status: 409, Code: "not_signed_in", Message: "sign in to Termward Pro first"}

// creds returns what signed requests need, or not_signed_in.
func (s *Service) creds() (*creds, error) {
	s.mu.Lock()
	dev, key := s.st.DeviceID, s.key
	s.mu.Unlock()
	if dev == "" {
		return nil, errNotSignedIn
	}
	if key == nil {
		k, err := loadKey(s.sec, s.keyName())
		if err != nil {
			s.logf("termward cloud: the device key is gone from the secret store; signing out")
			s.localSignOut(ReasonKeyLost)
			return nil, errNotSignedIn
		}
		key = k
		s.mu.Lock()
		s.key = k
		s.mu.Unlock()
	}
	m, err := s.machineFingerprint()
	if err != nil {
		return nil, err
	}
	return &creds{deviceID: dev, machine: m, key: key}, nil
}

// signed sends a signed request and reacts to answers that concern the
// device or the plan as a whole.
func (s *Service) signed(ctx context.Context, method, path string, in, out any, timeout time.Duration) error {
	cr, err := s.creds()
	if err != nil {
		return err
	}
	err = s.c.do(ctx, method, path, in, cr, out, timeout)
	s.react(err)
	return err
}

func (s *Service) react(err error) {
	var e *Error
	if !errors.As(err, &e) {
		return
	}
	switch {
	case e.deviceGone():
		reason := ReasonRevoked
		if e.Code == "device_mismatch" {
			reason = ReasonMismatch
		}
		s.localSignOut(reason)
	case e.Status == http.StatusPaymentRequired:
		s.mu.Lock()
		s.st.PlanInactive = true
		if s.st.Plan == nil {
			s.st.Plan = &Plan{}
		}
		s.st.Plan.Active = false
		if e.PaidUntil != "" {
			s.st.Plan.PaidUntil = e.PaidUntil
		}
		s.saveLocked()
		s.mu.Unlock()
		s.notify()
	}
}

// ------------------------------------------------------------ sign-in

var codeRE = regexp.MustCompile(`^\d{6}$`)

// SignInStart asks the server to email a sign-in code. It also makes sure the
// device key can be kept in the secret store before any code is sent.
func (s *Service) SignInStart(ctx context.Context, email, lang string) error {
	e, ok := NormalizeEmail(email)
	if !ok {
		return invalid("invalid_email", "email", "enter a valid email address")
	}
	if _, err := loadOrCreateKey(s.sec, s.keyName()); err != nil {
		return err
	}
	if _, err := s.machineFingerprint(); err != nil {
		return err
	}
	lang = normLang(lang)
	if err := s.c.do(ctx, http.MethodPost, "/v1/auth/start", map[string]string{"email": e, "lang": lang}, nil, nil, 0); err != nil {
		return err
	}
	s.mu.Lock()
	if !s.st.LangChosen {
		s.st.Forwarding.Lang = lang
		s.saveLocked()
	}
	s.mu.Unlock()
	return nil
}

// SignInVerify registers this device with the emailed code. A 409
// device_limit *Error carries the account's devices and the replaceToken
// for SignInReplace.
func (s *Service) SignInVerify(ctx context.Context, email, code string) (Status, error) {
	e, ok := NormalizeEmail(email)
	if !ok {
		return Status{}, invalid("invalid_email", "email", "enter a valid email address")
	}
	code = strings.Join(strings.Fields(code), "")
	if !codeRE.MatchString(code) {
		return Status{}, invalid("invalid_code", "code", "the code has 6 digits")
	}
	if s.Status().SignedIn {
		return Status{}, &Error{Status: 409, Code: "already_signed_in", Message: "this device is already signed in"}
	}
	key, err := loadOrCreateKey(s.sec, s.keyName())
	if err != nil {
		return Status{}, err
	}
	machine, err := s.machineFingerprint()
	if err != nil {
		return Status{}, err
	}
	var reg registered
	body := map[string]any{"email": e, "code": code, "device": s.deviceInfo(key, machine, e)}
	if err := s.c.do(ctx, http.MethodPost, "/v1/auth/verify", body, nil, &reg, 0); err != nil {
		var ce *Error
		if errors.As(err, &ce) && ce.Code == "device_mismatch" {
			// This key was registered from another machine (or with an older
			// fingerprint): use a fresh key from now on.
			deleteKey(s.sec, s.keyName())
			ce.Message = "this device key belongs to another machine; request a new code and sign in again"
		}
		return Status{}, err
	}
	return s.signedIn(ctx, key, reg)
}

// SignInReplace signs out revokeDeviceID and registers this device in its
// place, with the replaceToken from a device_limit answer.
func (s *Service) SignInReplace(ctx context.Context, replaceToken, revokeDeviceID string) (Status, error) {
	// Like SignInVerify: replacing while signed in would move this install
	// (its key and queued alerts) to the account the token belongs to.
	if s.Status().SignedIn {
		return Status{}, &Error{Status: 409, Code: "already_signed_in", Message: "this device is already signed in"}
	}
	if replaceToken == "" || len(replaceToken) > 512 || strings.ContainsAny(replaceToken, "\r\n") {
		return Status{}, invalid("invalid_replace_token", "replaceToken", "the replace token is missing")
	}
	if !validID.MatchString(revokeDeviceID) {
		return Status{}, invalid("invalid_device", "revokeDeviceId", "choose a device to sign out")
	}
	key, err := loadOrCreateKey(s.sec, s.keyName())
	if err != nil {
		return Status{}, err
	}
	machine, err := s.machineFingerprint()
	if err != nil {
		return Status{}, err
	}
	var reg registered
	body := map[string]any{
		"replaceToken": replaceToken, "revokeDeviceId": revokeDeviceID,
		"device": s.deviceInfo(key, machine, replaceToken),
	}
	if err := s.c.do(ctx, http.MethodPost, "/v1/auth/replace", body, nil, &reg, 0); err != nil {
		return Status{}, err
	}
	return s.signedIn(ctx, key, reg)
}

func (s *Service) signedIn(ctx context.Context, key ed25519.PrivateKey, reg registered) (Status, error) {
	if !validID.MatchString(reg.DeviceID) {
		return Status{}, &Error{Status: 502, Code: "bad_response", Message: "unexpected answer from Termward Cloud"}
	}
	s.mu.Lock()
	prices := s.st.Prices
	fw, chosen := s.st.Forwarding, s.st.LangChosen
	s.st = state{
		InstallID: s.st.InstallID,
		Email:     reg.Email, AccountID: reg.AccountID, DeviceID: reg.DeviceID, DeviceName: s.devName,
		Prices: prices, Forwarding: fw, LangChosen: chosen,
	}
	s.key = key
	// Nothing queued before this sign-in belongs to this account.
	s.q.clear()
	s.saveQueueLocked()
	s.lastErr, s.sendErr, s.retryAt, s.sendFails = "", "", time.Time{}, 0
	s.saveLocked()
	s.mu.Unlock()
	if _, err := s.Refresh(ctx); err != nil {
		s.logf("termward cloud: refresh after sign-in: %v", err)
	}
	kick(s.kickSend)
	return s.Status(), nil
}

// SignOut revokes this device on the server (best effort) and deletes the
// local key and account state. revoked is false when the server could not be
// told; the device then still appears in the account's list until removed
// from another device.
func (s *Service) SignOut(ctx context.Context) (revoked bool, err error) {
	s.mu.Lock()
	dev := s.st.DeviceID
	s.mu.Unlock()
	if dev == "" {
		return true, nil
	}
	cr, cerr := s.creds()
	if cerr == nil {
		ctx, cancel := context.WithTimeout(ctx, signOutTimeout)
		err := s.c.do(ctx, http.MethodDelete, "/v1/devices/"+dev, nil, cr, nil, signOutTimeout)
		cancel()
		var ce *Error
		revoked = err == nil || (errors.As(err, &ce) && (ce.deviceGone() || ce.Status == http.StatusNotFound))
	}
	s.localSignOut("")
	return revoked, nil
}

// localSignOut forgets the account on this device: the key, the cached state
// and pending alerts. Forwarding preferences and the last known prices stay.
func (s *Service) localSignOut(reason string) {
	deleteKey(s.sec, s.keyName())
	s.mu.Lock()
	if s.stopOrder != nil {
		s.stopOrder()
		s.stopOrder = nil
	}
	s.st = state{
		InstallID: s.st.InstallID,
		Prices:    s.st.Prices, Forwarding: s.st.Forwarding, LangChosen: s.st.LangChosen,
		SignedOutReason: reason,
	}
	s.key = nil
	s.q.clear()
	s.saveQueueLocked()
	s.lastErr, s.sendErr, s.retryAt, s.sendFails = "", "", time.Time{}, 0
	s.saveLocked()
	s.mu.Unlock()
	s.notify()
}

// DismissNotice clears the "you were signed out because…" notice.
func (s *Service) DismissNotice() Status {
	s.mu.Lock()
	if s.st.SignedOutReason != "" {
		s.st.SignedOutReason = ""
		s.saveLocked()
	}
	s.mu.Unlock()
	return s.Status()
}

// ------------------------------------------------------------ account

// Refresh reloads the account from GET /v1/me.
func (s *Service) Refresh(ctx context.Context) (Status, error) {
	var m me
	err := s.signed(ctx, http.MethodGet, "/v1/me", nil, &m, 0)
	s.mu.Lock()
	if err != nil {
		if s.st.DeviceID != "" {
			s.lastErr = err.Error()
		}
		s.mu.Unlock()
		s.notify()
		return s.Status(), err
	}
	if s.st.DeviceID == "" { // signed out meanwhile
		s.mu.Unlock()
		return s.Status(), nil
	}
	wasInactive := s.st.PlanInactive
	plan := m.Plan
	s.st.Plan = &plan
	s.st.PlanInactive = !plan.Active
	if m.Account.Email != "" {
		s.st.Email = m.Account.Email
	}
	if m.Account.ID != "" {
		s.st.AccountID = m.Account.ID
	}
	if m.Device.Name != "" {
		s.st.DeviceName = m.Device.Name
	}
	s.st.Devices = m.Devices
	s.st.Channels = m.Channels
	if len(m.Prices) > 0 {
		s.st.Prices = m.Prices
	}
	s.st.Limits = m.Limits
	s.st.RefreshedAt = s.now().UTC()
	s.lastErr = ""
	if wasInactive && plan.Active {
		s.sendErr = ""
	}
	s.saveLocked()
	s.mu.Unlock()
	if plan.Active {
		kick(s.kickSend)
	}
	s.notify()
	return s.Status(), nil
}

// RevokeDevice signs a device of the account out. Revoking this device is a
// sign-out.
func (s *Service) RevokeDevice(ctx context.Context, id string) error {
	if !validID.MatchString(id) {
		return invalid("invalid_device", "id", "unknown device")
	}
	s.mu.Lock()
	self := id == s.st.DeviceID
	s.mu.Unlock()
	if err := s.signed(ctx, http.MethodDelete, "/v1/devices/"+id, nil, nil, 0); err != nil {
		return err
	}
	if self {
		s.localSignOut("")
		return nil
	}
	_, _ = s.Refresh(ctx)
	return nil
}

// ------------------------------------------------------------ checkout

var validMonths = map[int]bool{1: true, 3: true, 6: true, 12: true}

// Checkout creates a PayOS payment link for a pack of months. The UI opens
// CheckoutURL in the system browser; the service polls the order until it is
// paid, cancelled or expired (at most 30 minutes) and then refreshes the plan.
func (s *Service) Checkout(ctx context.Context, months int) (Order, error) {
	if !validMonths[months] {
		return Order{}, invalid("invalid_months", "months", "choose 1, 3, 6 or 12 months")
	}
	var o Order
	if err := s.signed(ctx, http.MethodPost, "/v1/checkout", map[string]int{"months": months}, &o, 0); err != nil {
		return Order{}, err
	}
	if o.OrderCode <= 0 || !strings.HasPrefix(o.CheckoutURL, "https://") {
		return Order{}, &Error{Status: 502, Code: "bad_response", Message: "unexpected answer from Termward Cloud"}
	}
	if o.Status == "" {
		o.Status = OrderPending
	}
	if o.Months == 0 {
		o.Months = months
	}
	s.mu.Lock()
	s.st.Order = &PendingOrder{Order: o, StartedAt: s.now().UTC(), Waiting: true}
	s.saveLocked()
	s.mu.Unlock()
	s.watchOrder()
	s.notify()
	return o, nil
}

// OrderStatus asks the server for an order now.
func (s *Service) OrderStatus(ctx context.Context, code int64) (Order, error) {
	if code <= 0 {
		return Order{}, invalid("not_found", "orderCode", "unknown order")
	}
	var o Order
	if err := s.signed(ctx, http.MethodGet, "/v1/orders/"+strconv.FormatInt(code, 10), nil, &o, 0); err != nil {
		return Order{}, err
	}
	s.applyOrder(ctx, o)
	return o, nil
}

// StopWaiting forgets the pending order (the user closed the payment page).
// A payment made later is still credited by the server.
func (s *Service) StopWaiting() Status {
	s.mu.Lock()
	if s.stopOrder != nil {
		s.stopOrder()
		s.stopOrder = nil
	}
	s.st.Order = nil
	s.saveLocked()
	s.mu.Unlock()
	s.notify()
	return s.Status()
}

// applyOrder records a fresh order status; a payment refreshes the account.
func (s *Service) applyOrder(ctx context.Context, o Order) {
	s.mu.Lock()
	po := s.st.Order
	if po == nil || po.OrderCode != o.OrderCode {
		s.mu.Unlock()
		return
	}
	prev := po.Status
	if o.Status != "" {
		po.Status = o.Status
	}
	if o.PaidAt != "" {
		po.PaidAt = o.PaidAt
	}
	if o.Status != "" && o.Status != OrderPending {
		po.Waiting = false
	}
	if o.Plan != nil {
		if s.st.Plan == nil {
			s.st.Plan = &Plan{}
		}
		s.st.Plan.Active, s.st.Plan.PaidUntil = o.Plan.Active, o.Plan.PaidUntil
		s.st.PlanInactive = !o.Plan.Active
	}
	s.saveLocked()
	s.mu.Unlock()
	if o.Status == OrderPaid && prev != OrderPaid {
		_, _ = s.Refresh(ctx)
	}
	s.notify()
}

// watchOrder (re)starts polling the pending order in the background.
func (s *Service) watchOrder() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopOrder != nil {
		s.stopOrder()
		s.stopOrder = nil
	}
	po := s.st.Order
	if s.ctx == nil || po == nil || !po.Waiting {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.stopOrder = cancel
	go s.pollOrder(ctx, po.OrderCode, po.StartedAt)
}

func (s *Service) pollOrder(ctx context.Context, code int64, started time.Time) {
	delay := orderPollFirst
	for {
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if s.now().Sub(started) > orderPollFor {
			s.mu.Lock()
			if po := s.st.Order; po != nil && po.OrderCode == code {
				po.Waiting = false
				s.saveLocked()
			}
			s.mu.Unlock()
			s.notify()
			return
		}
		o, err := s.OrderStatus(ctx, code)
		var ce *Error
		switch {
		case err == nil && o.Status != "" && o.Status != OrderPending:
			return
		case errors.As(err, &ce) && (ce.Status == http.StatusNotFound || ce.Code == "not_signed_in" || ce.deviceGone()):
			return
		}
		delay = min(time.Duration(float64(delay)*1.5), orderPollMax)
	}
}

// ------------------------------------------------------------ channels

// AddChannel validates the input like the server does, then creates the
// channel. The config goes to the server once and is not stored here.
func (s *Service) AddChannel(ctx context.Context, in ChannelInput) (Channel, error) {
	req, verr := validateChannel(in)
	if verr != nil {
		return Channel{}, verr
	}
	var ch Channel
	if err := s.signed(ctx, http.MethodPost, "/v1/channels", req, &ch, 0); err != nil {
		return Channel{}, err
	}
	_, _ = s.Refresh(ctx)
	return ch, nil
}

func (s *Service) DeleteChannel(ctx context.Context, id string) error {
	if !validID.MatchString(id) {
		return invalid("not_found", "id", "unknown channel")
	}
	if err := s.signed(ctx, http.MethodDelete, "/v1/channels/"+id, nil, nil, 0); err != nil {
		return err
	}
	_, _ = s.Refresh(ctx)
	return nil
}

// TestChannel sends a test message through one channel.
func (s *Service) TestChannel(ctx context.Context, id, lang string) (TestResult, error) {
	if !validID.MatchString(id) {
		return TestResult{}, invalid("not_found", "id", "unknown channel")
	}
	var r TestResult
	if err := s.signed(ctx, http.MethodPost, "/v1/channels/"+id+"/test", map[string]string{"lang": normLang(lang)}, &r, alertsTimeout); err != nil {
		return TestResult{}, err
	}
	_, _ = s.Refresh(ctx)
	return r, nil
}

// SetForwarding saves what to forward and in which language.
func (s *Service) SetForwarding(f Forwarding) Status {
	f.Lang = normLang(f.Lang)
	s.mu.Lock()
	s.st.Forwarding = f
	s.st.LangChosen = true
	s.saveLocked()
	s.mu.Unlock()
	s.notify()
	return s.Status()
}

// ------------------------------------------------------------ alerts

// Enqueue hands an alert to the forwarder. It never blocks: when the inbox is
// full (the forwarder is stuck on disk I/O) the alert is dropped for the
// cloud; local notifications are unaffected.
func (s *Service) Enqueue(a health.Alert) {
	select {
	case s.in <- a:
	default:
		s.logf("termward cloud: alert inbox full, not forwarding alert %s", a.ID)
	}
}

// accept converts and queues one alert if the user wants it forwarded.
func (s *Service) accept(a health.Alert) {
	level := eventLevel(a)
	s.mu.Lock()
	dev := s.st.DeviceID
	ok := dev != "" && !s.st.PlanInactive && s.st.Forwarding.allows(level) &&
		(s.st.RefreshedAt.IsZero() || len(s.st.Channels) > 0)
	s.mu.Unlock()
	if !ok {
		return
	}
	addr := ""
	if s.host != nil {
		addr, _ = s.host(a.HostID)
	}
	ev, ok := toEvent(a, addr)
	if !ok {
		return
	}
	s.mu.Lock()
	if s.st.DeviceID != dev {
		// Signed out (or in again) meanwhile: the alert belongs to that
		// account and must not go out under the next one.
		s.mu.Unlock()
		return
	}
	if dropped := s.q.push(ev, s.now()); dropped > 0 {
		s.logf("termward cloud: alert queue full, dropped %d old alerts", dropped)
	}
	s.saveQueueLocked()
	s.mu.Unlock()
	kick(s.kickSend)
	s.notify()
}

// Run refreshes the account, forwards alerts and resumes a pending checkout
// until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	s.watchOrder()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case a := <-s.in:
				s.accept(a)
			}
		}
	}()
	go func() {
		defer wg.Done()
		s.sendLoop(ctx)
	}()
	s.refreshLoop(ctx)
	wg.Wait()
}

func (s *Service) refreshLoop(ctx context.Context) {
	wait := time.Duration(0) // refresh right away on start
	fails := 0
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-s.kickRefresh:
			t.Stop()
		}
		wait = refreshEvery
		if !s.Status().SignedIn {
			continue
		}
		if _, err := s.Refresh(ctx); err != nil && retryable(err) {
			fails++
			wait = backoff(refreshRetryBase, fails-1, refreshRetryMax)
		} else {
			fails = 0
		}
	}
}

func (s *Service) sendLoop(ctx context.Context) {
	for {
		wait := s.sendOnce(ctx)
		if wait < 0 {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		var tc <-chan time.Time
		var t *time.Timer
		if wait > 0 {
			t = time.NewTimer(wait)
			tc = t.C
		}
		select {
		case <-ctx.Done():
		case <-s.kickSend:
		case <-tc:
		}
		if t != nil {
			t.Stop()
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// sendOnce sends one batch. It returns how long to wait before the next try:
// negative for "right away", zero for "until something changes".
func (s *Service) sendOnce(ctx context.Context) time.Duration {
	now := s.now()
	s.mu.Lock()
	if n := s.q.prune(now); n > 0 {
		s.logf("termward cloud: dropped %d alerts older than 24 h", n)
		s.saveQueueLocked()
	}
	if s.st.DeviceID == "" || s.st.PlanInactive || len(s.q.items) == 0 {
		s.mu.Unlock()
		return 0
	}
	if now.Before(s.retryAt) {
		d := s.retryAt.Sub(now)
		s.mu.Unlock()
		return d
	}
	batch := s.q.ready(now, batchMax)
	if len(batch) == 0 {
		d := max(s.q.next().Sub(now), time.Second)
		s.mu.Unlock()
		return d
	}
	lang := s.st.Forwarding.Lang
	s.mu.Unlock()

	var res alertsResponse
	err := s.signed(ctx, http.MethodPost, "/v1/alerts", map[string]any{"lang": lang, "events": batch}, &res, alertsTimeout)
	if ctx.Err() != nil {
		return -1
	}
	now = s.now()
	if err != nil {
		return s.sendFailed(err, batch, now)
	}

	done, again := map[string]bool{}, map[string]bool{}
	answered := map[string]bool{}
	lastErr := ""
	for _, r := range res.Results {
		answered[r.EventID] = true
		if r.Delivered == 0 && len(r.Failed) > 0 && !r.Duplicate {
			again[r.EventID] = true // reached no channel: the server lets us retry
			lastErr = r.Failed[0].Error
		} else {
			done[r.EventID] = true
		}
	}
	for _, e := range batch {
		if !answered[e.ID] {
			again[e.ID] = true
		}
	}
	s.mu.Lock()
	s.q.remove(done)
	if n := s.q.retry(again, now); n > 0 {
		s.logf("termward cloud: gave up on %d alerts that no channel accepted", n)
	}
	s.saveQueueLocked()
	refresh := false
	if len(done) > 0 {
		s.st.LastSentAt = now.UTC()
		s.saveLocked()
		refresh = now.Sub(s.st.RefreshedAt) >= sentRefreshGap
	}
	s.sendErr, s.sendFails, s.retryAt = lastErr, 0, time.Time{}
	s.mu.Unlock()
	s.notify()
	if refresh {
		kick(s.kickRefresh)
	}
	return -1
}

var eventIndex = regexp.MustCompile(`events\[(\d+)\]`)

func (s *Service) sendFailed(err error, batch []Event, now time.Time) time.Duration {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Status: 0, Code: "error", Message: err.Error()}
	}
	s.mu.Lock()
	defer func() {
		s.mu.Unlock()
		s.notify()
	}()
	switch {
	case e.Code == "not_signed_in" || e.deviceGone():
		return 0 // signed out: wait for a new sign-in
	case e.Status == http.StatusPaymentRequired:
		s.sendErr = e.Code
		return 0 // react() marked the plan inactive; wait for a payment
	case e.Status == http.StatusTooManyRequests:
		s.sendErr = e.Code
		wait := time.Duration(max(e.RetryAfter, 60)) * time.Second
		s.retryAt = now.Add(wait)
		return wait
	case e.Status == http.StatusBadRequest || e.Status == http.StatusRequestEntityTooLarge:
		// The server refuses the events themselves: retrying cannot help.
		drop := map[string]bool{}
		if m := eventIndex.FindStringSubmatch(e.Message); m != nil {
			if i, err := strconv.Atoi(m[1]); err == nil && i < len(batch) {
				drop[batch[i].ID] = true
			}
		}
		if len(drop) == 0 {
			for _, ev := range batch {
				drop[ev.ID] = true
			}
		}
		s.logf("termward cloud: server refused %d alerts: %s", len(drop), e.Message)
		s.q.remove(drop)
		s.saveQueueLocked()
		return -1
	default: // network, timeouts, server errors, signature trouble
		s.sendErr = e.Code
		s.sendFails++
		wait := backoff(sendRetryBase, s.sendFails-1, sendRetryMax)
		s.retryAt = now.Add(wait)
		return wait
	}
}
