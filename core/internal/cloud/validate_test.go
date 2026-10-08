package cloud

import "testing"

func TestValidateChannel(t *testing.T) {
	const tgToken = "123456789:" + "AAHfiqksKZ8WmR2zSjiQ7_v4TMAKdiHm9T0"
	cases := []struct {
		name  string
		in    ChannelInput
		field string // "" = valid
		cfg   map[string]string
	}{
		{"telegram ok", ChannelInput{Kind: "telegram", Name: " Ops\tgroup ", BotToken: " " + tgToken + " ", ChatID: "-1001234567890"}, "",
			map[string]string{"botToken": tgToken, "chatId": "-1001234567890"}},
		{"telegram channel name", ChannelInput{Kind: "telegram", BotToken: tgToken, ChatID: "@ops_alerts"}, "", nil},
		{"telegram bad token", ChannelInput{Kind: "telegram", BotToken: "123:short", ChatID: "1"}, "botToken", nil},
		{"telegram bad chat", ChannelInput{Kind: "telegram", BotToken: tgToken, ChatID: "ops"}, "chatId", nil},
		{"zalo ok", ChannelInput{Kind: "zalo", BotToken: "1234567890:abcDEF_123-xyz", ChatID: "a1b2c3"}, "",
			map[string]string{"botToken": "1234567890:abcDEF_123-xyz", "chatId": "a1b2c3"}},
		{"zalo bad token", ChannelInput{Kind: "zalo", BotToken: "nocolon", ChatID: "x"}, "botToken", nil},
		{"zalo bad chat", ChannelInput{Kind: "zalo", BotToken: "1:abcdefgh", ChatID: "a b"}, "chatId", nil},
		{"discord ok", ChannelInput{Kind: "discord", WebhookURL: "https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz_-0123"}, "",
			map[string]string{"webhookUrl": "https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz_-0123"}},
		{"discord upper host canonicalised", ChannelInput{Kind: "discord", WebhookURL: "https://Discord.com/api/v10/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz?thread_id=123456"}, "",
			map[string]string{"webhookUrl": "https://discord.com/api/v10/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz?thread_id=123456"}},
		{"discordapp ok", ChannelInput{Kind: "discord", WebhookURL: "https://discordapp.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz"}, "", nil},
		{"discord other host", ChannelInput{Kind: "discord", WebhookURL: "https://evil.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz"}, "webhookUrl", nil},
		{"discord http", ChannelInput{Kind: "discord", WebhookURL: "http://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz"}, "webhookUrl", nil},
		{"discord port", ChannelInput{Kind: "discord", WebhookURL: "https://discord.com:8443/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz"}, "webhookUrl", nil},
		{"discord creds", ChannelInput{Kind: "discord", WebhookURL: "https://a@discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz"}, "webhookUrl", nil},
		{"discord encoded host", ChannelInput{Kind: "discord", WebhookURL: "https://disc%6Frd.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz"}, "webhookUrl", nil},
		{"discord other query", ChannelInput{Kind: "discord", WebhookURL: "https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz?wait=true"}, "webhookUrl", nil},
		{"discord fragment", ChannelInput{Kind: "discord", WebhookURL: "https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz#x"}, "webhookUrl", nil},
		{"slack ok", ChannelInput{Kind: "slack", WebhookURL: "https://hooks.slack.com" + "/services/T0123ABCD/B0123ABCD/abcdefghijklmnopqrstuvwx"}, "",
			map[string]string{"webhookUrl": "https://hooks.slack.com" + "/services/T0123ABCD/B0123ABCD/abcdefghijklmnopqrstuvwx"}},
		{"slack query", ChannelInput{Kind: "slack", WebhookURL: "https://hooks.slack.com" + "/services/T0123ABCD/B0123ABCD/abcdefghijklmnopqrstuvwx?x=1"}, "webhookUrl", nil},
		{"slack other host", ChannelInput{Kind: "slack", WebhookURL: "https://hooks.slack.com.evil.com/services/T0123ABCD/B0123ABCD/abcdefghijklmnopqrstuvwx"}, "webhookUrl", nil},
		{"slack space", ChannelInput{Kind: "slack", WebhookURL: "https://hooks.slack.com" + "/services/T0123ABCD/B0123ABCD/abcdefghij klmnopqrstuvwx"}, "webhookUrl", nil},
		{"unknown kind", ChannelInput{Kind: "email"}, "kind", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, err := validateChannel(c.in)
			if c.field == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				for k, v := range c.cfg {
					if req.Config[k] != v {
						t.Errorf("config[%s] = %q, want %q", k, req.Config[k], v)
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted %+v", c.in)
			}
			if err.Field != c.field || err.Status != 400 || (err.Code != "invalid_config" && err.Code != "invalid_kind") {
				t.Errorf("error %+v, want field %s", err, c.field)
			}
		})
	}

	req, _ := validateChannel(ChannelInput{Kind: "telegram", Name: " Ops\tgroup ", BotToken: tgToken, ChatID: "1"})
	if req.Name != "Ops group" {
		t.Errorf("name %q", req.Name)
	}
	req, _ = validateChannel(ChannelInput{Kind: "zalo", BotToken: "1:abcdefgh", ChatID: "x"})
	if req.Name != "Zalo Bot" || req.Kind != "zalo" {
		t.Errorf("default name %q", req.Name)
	}
	if len(req.Config) != 2 {
		t.Errorf("only the kind's fields are sent: %v", req.Config)
	}
}
