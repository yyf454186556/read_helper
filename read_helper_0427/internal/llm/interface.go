package llm

import "context"

// Caller 大模型调用接口：仅依赖文本 prompt 与可选 meta，返回模型回复文本。
// 便于替换为不同厂商或不同实现（如火山引擎、OpenAI、本地模型等）。
type Caller interface {
	Call(ctx context.Context, prompt string, meta map[string]string) (string, error)
}
