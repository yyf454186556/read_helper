package response

import (
	"regexp"
	"strings"
)

// DefaultExtractor 默认实现：去除 BOM、```json ... ```、按花括号匹配或首尾 {} 提取。
var DefaultExtractor JSONExtractor = (*defaultExtractor)(nil)

type defaultExtractor struct{}

func (*defaultExtractor) ExtractJSON(reply string) string {
	reply = strings.TrimSpace(reply)
	// 去除 UTF-8 BOM，避免 HasPrefix("{") 为假或 Unmarshal 报错
	if strings.HasPrefix(reply, "\xef\xbb\xbf") {
		reply = reply[3:]
		reply = strings.TrimSpace(reply)
	}
	// 1) 先尝试 markdown 代码块
	re := regexp.MustCompile("(?s)\\s*```(?:json)?\\s*\\n?(.*?)\\n?```\\s*")
	if m := re.FindStringSubmatch(reply); len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	// 2) 整段就是 JSON（以 { 开头）
	if strings.HasPrefix(reply, "{") {
		// 用花括号匹配取第一个完整对象，避免末尾有多余字符导致 Unmarshal 失败
		if extracted := extractBalancedJSON(reply); extracted != "" {
			return extracted
		}
		return reply
	}
	// 3) 从第一个 { 起用花括号匹配提取
	start := strings.Index(reply, "{")
	if start < 0 {
		return ""
	}
	if extracted := extractBalancedJSON(reply[start:]); extracted != "" {
		return extracted
	}
	// 兜底：首尾 {}（可能与末尾多余 } 冲突，仅作后备）
	end := strings.LastIndex(reply, "}")
	if end > start {
		return reply[start : end+1]
	}
	return ""
}

// extractBalancedJSON 从 s 开头取第一个完整的 { ... }（按花括号匹配），保证可被 json.Unmarshal 解析。
func extractBalancedJSON(s string) string {
	if s == "" || s[0] != '{' {
		return ""
	}
	depth := 0
	inString := false
	escape := false
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' && (quote == '"' || quote == '\'') {
				escape = true
				continue
			}
			if c == quote {
				inString = false
			}
			continue
		}
		switch c {
		case '"', '\'':
			inString = true
			quote = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}
	return ""
}

// ExtractJSON 使用默认提取器从 reply 中提取 JSON。便于包外直接调用。
func ExtractJSON(reply string) string {
	return DefaultExtractor.ExtractJSON(reply)
}
