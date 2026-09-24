package health

import (
	"fmt"
	"strings"
)

// AlertText renders an alert as a short notification (title, body) for
// places without the UI's translations, such as native mobile notifications.
// lang is "vi" or anything else for English.
func AlertText(a Alert, lang string) (string, string) {
	vi := strings.HasPrefix(lang, "vi")
	pick := func(en, v string) string {
		if vi {
			return v
		}
		return en
	}
	var title string
	switch a.To {
	case LevelDown:
		title = pick("%s is down", "%s mất kết nối")
	case LevelCrit:
		title = pick("%s is critical", "%s đang nghiêm trọng")
	case LevelWarn:
		title = pick("%s needs attention", "%s cần chú ý")
	case LevelOK:
		title = pick("%s is healthy again", "%s đã ổn định trở lại")
	default:
		title = "%s"
	}
	title = fmt.Sprintf(title, a.HostName)

	for _, f := range a.Findings {
		if f.Level != LevelInfo {
			return title, FindingText(f, lang)
		}
	}
	return title, ""
}

// FindingText is the English/Vietnamese sentence for one finding.
func FindingText(f Finding, lang string) string {
	vi := strings.HasPrefix(lang, "vi")
	pct := fmt.Sprintf("%.0f%%", f.Value)
	switch f.Code {
	case "unreachable":
		if vi {
			return "Không kết nối được máy chủ"
		}
		return "Server is unreachable"
	case "cpu":
		return "CPU " + pct
	case "mem":
		if vi {
			return "RAM " + pct
		}
		return "Memory " + pct
	case "disk":
		if vi {
			return f.Subject + " đã đầy " + pct
		}
		return f.Subject + " is " + pct + " full"
	case "load":
		if vi {
			return fmt.Sprintf("Tải %.2f mỗi nhân", f.Value)
		}
		return fmt.Sprintf("Load %.2f per core", f.Value)
	case "unit_failed":
		if vi {
			return "Dịch vụ " + f.Subject + " bị lỗi"
		}
		return "Service " + f.Subject + " failed"
	case "container_unhealthy":
		if vi {
			return "Container " + f.Subject + " không khỏe"
		}
		return "Container " + f.Subject + " is unhealthy"
	case "container_restarting":
		if vi {
			return "Container " + f.Subject + " liên tục khởi động lại"
		}
		return "Container " + f.Subject + " keeps restarting"
	case "container_dead":
		if vi {
			return "Container " + f.Subject + " đã dừng hẳn"
		}
		return "Container " + f.Subject + " is dead"
	}
	return f.Code
}
