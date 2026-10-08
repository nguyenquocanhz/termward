package cloud

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// Channel kinds.
const (
	KindTelegram = "telegram"
	KindZalo     = "zalo"
	KindDiscord  = "discord"
	KindSlack    = "slack"
)

// MaxChannels is the server's per-account limit.
const MaxChannels = 10

// ChannelInput is what the UI sends to add a channel. Only the fields of its
// kind are used; they are sent to the server once and never stored locally.
type ChannelInput struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	BotToken   string `json:"botToken,omitempty"`
	ChatID     string `json:"chatId,omitempty"`
	WebhookURL string `json:"webhookUrl,omitempty"`
}

type channelRequest struct {
	Kind   string            `json:"kind"`
	Name   string            `json:"name"`
	Config map[string]string `json:"config"`
}

// These mirror the server (src/channels/config.ts) so the user gets a clear
// message before anything is sent; the server checks again.
var (
	telegramToken = regexp.MustCompile(`^\d{5,20}:[A-Za-z0-9_-]{30,80}$`)
	telegramChat  = regexp.MustCompile(`^(-?\d{1,20}|@[A-Za-z][A-Za-z0-9_]{3,31})$`)
	zaloToken     = regexp.MustCompile(`^\d{1,30}:[A-Za-z0-9_-]{8,200}$`)
	zaloChat      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	discordPath   = regexp.MustCompile(`^/api(?:/v\d{1,2})?/webhooks/\d{5,30}/[A-Za-z0-9_-]{20,120}$`)
	discordQuery  = regexp.MustCompile(`^thread_id=\d{5,30}$`)
	slackPath     = regexp.MustCompile(`^/services/T[A-Z0-9]{6,15}/B[A-Z0-9]{6,15}/[A-Za-z0-9]{16,64}$`)
	badURLChars   = regexp.MustCompile(`[\s\\\x00-\x1f]`)
)

// canonicalWebhook returns the canonical webhook URL for the exact allowed
// hosts over https on the default port, or "" when the server would refuse it.
func canonicalWebhook(raw, kind string) string {
	if len(raw) > 512 || badURLChars.MatchString(raw) || !strings.HasPrefix(strings.ToLower(raw), "https://") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" || strings.Contains(raw, "#") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	// The host must be written literally (no percent-encoding or IDN).
	if !strings.HasPrefix(strings.ToLower(raw), "https://"+host+"/") {
		return ""
	}
	path := u.EscapedPath()
	switch kind {
	case KindDiscord:
		if (host != "discord.com" && host != "discordapp.com") || !discordPath.MatchString(path) {
			return ""
		}
		if u.RawQuery != "" && !discordQuery.MatchString(u.RawQuery) {
			return ""
		}
	case KindSlack:
		if host != "hooks.slack.com" || !slackPath.MatchString(path) || u.RawQuery != "" {
			return ""
		}
	default:
		return ""
	}
	out := "https://" + host + path
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out
}

// cleanName mirrors the server's cleanText: control characters become
// spaces, runs of whitespace collapse, at most max characters.
func cleanName(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

var kindNames = map[string]string{
	KindTelegram: "Telegram", KindZalo: "Zalo Bot", KindDiscord: "Discord", KindSlack: "Slack",
}

// validateChannel checks the input and builds the request body. The name
// defaults to the kind's display name.
func validateChannel(in ChannelInput) (channelRequest, *Error) {
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	display, ok := kindNames[kind]
	if !ok {
		return channelRequest{}, invalid("invalid_kind", "kind", "kind must be telegram, zalo, discord or slack")
	}
	name := cleanName(in.Name, 64)
	if name == "" {
		name = display
	}
	req := channelRequest{Kind: kind, Name: name, Config: map[string]string{}}
	switch kind {
	case KindTelegram:
		tok, chat := strings.TrimSpace(in.BotToken), strings.TrimSpace(in.ChatID)
		if !telegramToken.MatchString(tok) {
			return req, invalid("invalid_config", "botToken", "The Telegram bot token looks wrong. It comes from @BotFather and looks like 123456789:AAH…")
		}
		if !telegramChat.MatchString(chat) {
			return req, invalid("invalid_config", "chatId", "The Telegram chat id must be a number (groups start with -100…) or @channelname")
		}
		req.Config["botToken"], req.Config["chatId"] = tok, chat
	case KindZalo:
		tok, chat := strings.TrimSpace(in.BotToken), strings.TrimSpace(in.ChatID)
		if !zaloToken.MatchString(tok) {
			return req, invalid("invalid_config", "botToken", "The Zalo Bot token looks wrong. It looks like <id>:<secret> (from bot.zaloplatforms.com)")
		}
		if !zaloChat.MatchString(chat) {
			return req, invalid("invalid_config", "chatId", "The Zalo chat id may only contain letters, digits, - and _ (at most 64)")
		}
		req.Config["botToken"], req.Config["chatId"] = tok, chat
	case KindDiscord:
		u := canonicalWebhook(strings.TrimSpace(in.WebhookURL), KindDiscord)
		if u == "" {
			return req, invalid("invalid_config", "webhookUrl", "The Discord webhook URL must look like https://discord.com/api/webhooks/<id>/<token>")
		}
		req.Config["webhookUrl"] = u
	case KindSlack:
		u := canonicalWebhook(strings.TrimSpace(in.WebhookURL), KindSlack)
		if u == "" {
			return req, invalid("invalid_config", "webhookUrl", "The Slack webhook URL must look like https://hooks.slack.com/services/T…/B…/…")
		}
		req.Config["webhookUrl"] = u
	}
	return req, nil
}

// validID guards ids the UI sends that end up in a request path.
var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
