// Package cloud is the client side of Termward Pro: alerts delivered to Slack,
// Discord, Telegram and Zalo Bot by the Termward Cloud server.
//
// The server does all the work that matters (it checks the subscription and
// the calling device on every request and sends to the channels itself), so
// this package holds nothing to unlock. It keeps a device identity (an Ed25519
// key in the OS secret store plus a machine fingerprint), signs requests as
// the contract describes (TW2), caches the non-secret account state, and
// forwards health and hardware alerts through a small on-disk queue.
//
// The wire contract lives in the server repository (docs/API.md); the
// constants and formats below follow it exactly.
package cloud

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the production server. Only TERMWARD_CLOUD_URL overrides it.
const DefaultBaseURL = "https://cloud.nguyenquocanh.io.vn"

// Header names of a signed request.
const (
	HeaderDevice    = "X-TW-Device"
	HeaderMachine   = "X-TW-Machine"
	HeaderTime      = "X-TW-Time"
	HeaderNonce     = "X-TW-Nonce"
	HeaderSignature = "X-TW-Signature"
)

const (
	canonicalVersion    = "TW2"
	registrationVersion = "TW2-REGISTER"
	machinePrefix       = "termward-machine:"
)

// b64 is base64url without padding, the only encoding the contract uses.
var b64 = base64.RawURLEncoding

// BodyHash is the lowercase hex SHA-256 of the exact body bytes ("" when
// there is no body).
func BodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Canonical is the TW2 canonical string: the lines below joined with "\n",
// without a trailing newline. path is the request target exactly as sent
// (Go: req.URL.RequestURI()).
func Canonical(method, path, device, unixTime, nonce, machine string, body []byte) string {
	return strings.Join([]string{
		canonicalVersion,
		strings.ToUpper(method),
		path,
		device,
		unixTime,
		nonce,
		machine,
		BodyHash(body),
	}, "\n")
}

// Signed holds the header values of one signed request.
type Signed struct {
	Device    string
	Machine   string
	Time      string
	Nonce     string
	Signature string
}

// Sign signs one request. nonce must be 16 fresh random bytes (fixed only in
// tests, to reproduce the contract's example vector).
func Sign(key ed25519.PrivateKey, method, path, device, machine string, body []byte, now time.Time, nonce []byte) Signed {
	s := Signed{
		Device:  device,
		Machine: machine,
		Time:    strconv.FormatInt(now.Unix(), 10),
		Nonce:   b64.EncodeToString(nonce),
	}
	msg := Canonical(method, path, device, s.Time, s.Nonce, machine, body)
	s.Signature = b64.EncodeToString(ed25519.Sign(key, []byte(msg)))
	return s
}

// RegistrationMessage is what a new device signs to prove it holds its key:
// subject is the normalized email (auth/verify) or the replaceToken
// (auth/replace), exactly as the server will see it.
func RegistrationMessage(subject, publicKey, machine string) string {
	return strings.Join([]string{registrationVersion, subject, publicKey, machine}, "\n")
}

// Proof is device.proof: base64url Ed25519 signature of RegistrationMessage.
func Proof(key ed25519.PrivateKey, subject, publicKey, machine string) string {
	return b64.EncodeToString(ed25519.Sign(key, []byte(RegistrationMessage(subject, publicKey, machine))))
}

// PublicKeyString is the base64url form of a device public key.
func PublicKeyString(key ed25519.PrivateKey) string {
	return b64.EncodeToString(key.Public().(ed25519.PublicKey))
}

// Fingerprint is the machine fingerprint the server binds a device to: the
// first 32 hex characters of SHA-256("termward-machine:" + machineID).
func Fingerprint(machineID string) string {
	sum := sha256.Sum256([]byte(machinePrefix + machineID))
	return hex.EncodeToString(sum[:])[:32]
}

// emailRE is the server's check (src/routes/auth.ts).
var emailRE = regexp.MustCompile(`^[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)

// NormalizeEmail trims and lowercases an email the way the server stores it
// (the proof signs this form). ok is false when the server would refuse it.
func NormalizeEmail(s string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(s))
	return e, len(e) <= 254 && emailRE.MatchString(e)
}
