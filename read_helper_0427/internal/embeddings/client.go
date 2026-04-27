package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const (
	defaultBaseURL = "https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal"
	defaultModel   = "doubao-embedding-vision-251215"
)

// embedInputItem 多模态 embedding 单条输入，仅用文本。
type embedInputItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// embedRequest 与火山引擎 /api/v3/embeddings/multimodal 一致（不传图片）。
type embedRequest struct {
	Model string           `json:"model"`
	Input []embedInputItem `json:"input"`
}

// 响应 data 中单条（旧版 /embeddings 返回 index；multimodal 只返回 embedding + object）
type embedDataItem struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
	Object    string    `json:"object"`
}

// multimodal 单条：data 为对象时 { "embedding": [...], "object": "embedding" }，无 index
type embedDataObject struct {
	Embedding []float32 `json:"embedding"`
	Object    string    `json:"object"`
}

// 响应体：data 可能是数组或对象，用 RawMessage 后按格式解析。
type embedResponse struct {
	Data   json.RawMessage `json:"data"`
	Model  string          `json:"model"`
	Object string          `json:"object"`
	Usage  *struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Client 火山引擎文本嵌入客户端，使用与 LLM 相同的 ARK_API_KEY。
type Client struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// NewClient 创建客户端。apiKey 为空则从环境变量 ARK_API_KEY 读取；model/baseURL 为空则用默认值。
func NewClient(apiKey, model, baseURL string) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("ARK_API_KEY")
	}
	if model == "" {
		model = defaultModel
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{},
	}
}

// buildInput 将多段文本转为 multimodal input 数组（仅 type=text，不传图片）。
func buildInput(texts []string) []embedInputItem {
	out := make([]embedInputItem, len(texts))
	for i, s := range texts {
		out[i] = embedInputItem{Type: "text", Text: s}
	}
	return out
}

// Embed 对多段文本做嵌入，返回与 input 顺序一致的 [][]float32。
// 单段文本可：vec, err := c.Embed([]string{text}); v := vec[0]
func (c *Client) Embed(input []string) ([][]float32, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if c.apiKey == "" {
		return nil, fmt.Errorf("未配置 API Key，请设置环境变量 ARK_API_KEY")
	}

	reqBody := embedRequest{
		Model: c.model,
		Input: buildInput(input),
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应: %w", err)
	}

	var out embedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析响应: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("API 错误 %s: %s", out.Error.Code, out.Error.Message)
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("响应中无 embedding 数据")
	}

	// multimodal 返回：data 为单对象 { "embedding": [...], "object": "embedding" } 或数组 [ {...}, ... ]
	byIndex := make(map[int][]float32)
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("响应中无 embedding 数据")
	}
	if out.Data[0] == '[' {
		// 数组：[ { "embedding": [...], "object": "embedding" }, ... ]，按下标对应 input 顺序
		var list []embedDataObject
		if err := json.Unmarshal(out.Data, &list); err != nil {
			return nil, fmt.Errorf("解析 data 数组: %w", err)
		}
		for i, d := range list {
			byIndex[i] = d.Embedding
		}
	} else {
		// 单对象：{ "embedding": [...], "object": "embedding" }，仅一条时对应 input[0]
		var single embedDataObject
		if err := json.Unmarshal(out.Data, &single); err != nil {
			return nil, fmt.Errorf("解析 data 对象: %w", err)
		}
		byIndex[0] = single.Embedding
	}
	result := make([][]float32, len(input))
	for i := range input {
		emb, ok := byIndex[i]
		if !ok {
			return nil, fmt.Errorf("响应中缺少 index=%d 的 embedding", i)
		}
		result[i] = emb
	}
	return result, nil
}
