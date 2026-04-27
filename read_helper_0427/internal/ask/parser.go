package ask

import "strings"

// ParsedQuestion 解析后的用户提问：类型 + 查询关键字（可选）
type ParsedQuestion struct {
	Type   string // 如 "人物查询"、"通用"
	Keyword string // 用于过滤摘要的关键字，可为空
}

// Parser 提问解析接口。将用户问题解析为结构化字段，便于过滤与路由。
type Parser interface {
	Parse(question string) ParsedQuestion
}

// SimpleParser 简单实现：识别「xxx是谁」等模式。
type SimpleParser struct{}

func (SimpleParser) Parse(question string) ParsedQuestion {
	q := strings.TrimSpace(question)
	if strings.HasSuffix(q, "是谁") {
		keyword := strings.TrimSpace(strings.TrimSuffix(q, "是谁"))
		if keyword != "" {
			return ParsedQuestion{Type: "人物查询", Keyword: keyword}
		}
	}
	return ParsedQuestion{Type: "通用", Keyword: ""}
}

// DefaultParser 默认解析器实例
var DefaultParser Parser = SimpleParser{}

// ParseQuestion 使用默认解析器解析用户问题
func ParseQuestion(question string) ParsedQuestion {
	return DefaultParser.Parse(question)
}
