package response

// JSONExtractor 从大模型回复文本中提取 JSON 的接口。不同模型或格式可提供不同实现。
type JSONExtractor interface {
	// ExtractJSON 从 reply 中截取并返回一段合法 JSON 字符串；无法提取时返回空字符串。
	ExtractJSON(reply string) string
}
