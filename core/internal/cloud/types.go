package cloud

import (
	"fmt"
	"time"
)

// Wire types of the contract (docs/API.md). Unknown fields are ignored.

type Device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	AppVersion string `json:"appVersion"`
	CreatedAt  string `json:"createdAt,omitempty"`
	LastSeen   string `json:"lastSeen,omitempty"`
	Current    bool   `json:"current"`
}

type ChannelResult struct {
	OK    bool   `json:"ok"`
	At    string `json:"at,omitempty"`
	Error string `json:"error,omitempty"`
}

// Channel as listed by the server: never carries the config (tokens,
// webhook URLs), which the server keeps encrypted.
type Channel struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	CreatedAt  string         `json:"createdAt,omitempty"`
	LastResult *ChannelResult `json:"lastResult"`
}

type Plan struct {
	Active     bool   `json:"active"`
	PaidUntil  string `json:"paidUntil,omitempty"` // null on the wire before the first payment
	MaxDevices int    `json:"maxDevices,omitempty"`
}

type Price struct {
	Months   int    `json:"months"`
	Amount   int64  `json:"amount"`
	VAT      int64  `json:"vat"`
	Total    int64  `json:"total"`
	Currency string `json:"currency"`
}

type Limits struct {
	AlertsPerDay int `json:"alertsPerDay"`
	UsedToday    int `json:"usedToday"`
}

type me struct {
	Account struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"account"`
	Plan   Plan `json:"plan"`
	Device struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"device"`
	Devices  []Device  `json:"devices"`
	Channels []Channel `json:"channels"`
	Prices   []Price   `json:"prices"`
	Limits   *Limits   `json:"limits"`
}

// DeviceInfo is the device object of auth/verify and auth/replace.
type DeviceInfo struct {
	PublicKey  string `json:"publicKey"`
	Machine    string `json:"machine"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	AppVersion string `json:"appVersion"`
	Proof      string `json:"proof"`
}

type registered struct {
	AccountID string `json:"accountId"`
	DeviceID  string `json:"deviceId"`
	Email     string `json:"email"`
}

// Order is a PayOS checkout (POST /v1/checkout, GET /v1/orders/{code}).
type Order struct {
	OrderCode   int64  `json:"orderCode"`
	Status      string `json:"status,omitempty"` // pending | paid | cancelled | expired
	Months      int    `json:"months,omitempty"`
	Amount      int64  `json:"amount,omitempty"`
	CheckoutURL string `json:"checkoutUrl,omitempty"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
	PaidAt      string `json:"paidAt,omitempty"`
	Plan        *Plan  `json:"plan,omitempty"`
}

// Order statuses.
const (
	OrderPending   = "pending"
	OrderPaid      = "paid"
	OrderCancelled = "cancelled"
	OrderExpired   = "expired"
)

// Text is one message in both languages.
type Text struct {
	EN string `json:"en"`
	VI string `json:"vi"`
}

type EventHost struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
}

// Event is one alert sent to POST /v1/alerts.
type Event struct {
	ID    string    `json:"id"`
	Kind  string    `json:"kind"`  // health | hardware
	Level string    `json:"level"` // crit | warn | info | ok
	Host  EventHost `json:"host"`
	Title Text      `json:"title"`
	Body  Text      `json:"body"`
	At    time.Time `json:"at"`
}

type alertsResponse struct {
	Results []struct {
		EventID   string `json:"eventId"`
		Delivered int    `json:"delivered"`
		Failed    []struct {
			ChannelID string `json:"channelId"`
			Error     string `json:"error"`
		} `json:"failed"`
		Duplicate bool `json:"duplicate"`
	} `json:"results"`
}

// TestResult is the answer of POST /v1/channels/{id}/test.
type TestResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Error is a failure the UI can act on: an error answer of the server (its
// code and fields), a local validation error, or "cloud_unreachable".
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"error"`
	Message string `json:"message"`

	// Fields some errors carry (see the contract's error table).
	ServerTime   int64    `json:"serverTime,omitempty"`
	RetryAfter   int      `json:"retryAfter,omitempty"`
	Max          int      `json:"max,omitempty"`
	Devices      []Device `json:"devices,omitempty"`
	ReplaceToken string   `json:"replaceToken,omitempty"`
	PaidUntil    string   `json:"paidUntil,omitempty"`
	Limit        int      `json:"limit,omitempty"`
	UsedToday    int      `json:"usedToday,omitempty"`
	// Field names the input that failed local validation ("email", "botToken"…).
	Field string `json:"field,omitempty"`
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("termward cloud: %s (HTTP %d)", e.Code, e.Status)
}

func invalid(code, field, msg string) *Error {
	return &Error{Status: 400, Code: code, Field: field, Message: msg}
}

// Codes that mean this device can no longer sign requests.
func (e *Error) deviceGone() bool {
	return e.Status == 401 && (e.Code == "device_revoked" || e.Code == "device_mismatch")
}
