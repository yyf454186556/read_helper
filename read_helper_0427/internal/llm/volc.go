package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	defaultBaseURL = "https://ark.cn-beijing.volces.com/api/v3"
	defaultModel   = "doubao-seed-1-6-lite-251015" // 火山引擎豆包 lite，费用更低
)

// --- 请求/响应 JSON 结构（与火山引擎 API 一致）---

type contentItem struct {
	Type     string `json:"type,omitempty"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type inputMessage struct {
	Role    string        `json:"role"`
	Content []contentItem `json:"content"`
}

type responsesRequest struct {
	Model string         `json:"model"`
	Input []inputMessage `json:"input"`
}

type contentOutput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type outputMessage struct {
	Content []contentOutput `json:"content"`
}

type outputItem struct {
	Message *outputMessage `json:"message,omitempty"`
	Text    string         `json:"text,omitempty"`
}

type responsesOutput struct {
	ListValue []outputItem `json:"list_value,omitempty"`
	Text      string       `json:"text,omitempty"`
}

type responsesResponse struct {
	Output json.RawMessage `json:"output,omitempty"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// VolcClient 火山引擎 Ark 大模型 HTTP 客户端，实现 Caller 接口。
type VolcClient struct {
	apiKey       string
	baseURL      string
	model        string
	eventLogPath string
	client       *http.Client
}

// NewVolcClient 创建火山引擎客户端。apiKey 为空则从环境变量 ARK_API_KEY 读取；model/baseURL/eventLogPath 为空则用默认值。
func NewVolcClient(apiKey, model, baseURL, eventLogPath string) *VolcClient {
	if apiKey == "" {
		apiKey = os.Getenv("ARK_API_KEY")
	}
	if model == "" {
		model = defaultModel
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if eventLogPath == "" {
		eventLogPath = "event.log"
	}
	return &VolcClient{
		apiKey:       apiKey,
		baseURL:      baseURL,
		model:        model,
		eventLogPath: eventLogPath,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// Call 实现 Caller 接口。
func (c *VolcClient) Call(ctx context.Context, prompt string, meta map[string]string) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("未配置 API Key，请设置环境变量 ARK_API_KEY")
	}
	reqBody := responsesRequest{
		Model: c.model,
		Input: []inputMessage{
			{Role: "user", Content: []contentItem{{Type: "input_text", Text: prompt}}},
		},
	}
	return c.doCall(ctx, reqBody, meta)
}

// CallWithImage 带图片的调用，可选扩展。
func (c *VolcClient) CallWithImage(ctx context.Context, imageURL, prompt string, meta map[string]string) (string, error) {
	reqBody := responsesRequest{
		Model: c.model,
		Input: []inputMessage{
			{
				Role: "user",
				Content: []contentItem{
					{Type: "input_image", ImageURL: imageURL},
					{Type: "input_text", Text: prompt},
				},
			},
		},
	}
	return c.doCall(ctx, reqBody, meta)
}

func (c *VolcClient) doCall(ctx context.Context, reqBody responsesRequest, meta map[string]string) (string, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("序列化请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("创建请求: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应: %w", err)
	}
	appendEventLog(c.eventLogPath, string(body), string(raw), meta)

	var out responsesResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("解析响应: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("API 错误 %s: %s", out.Error.Code, out.Error.Message)
	}
	text := extractResponseText(out.Output)
	if text == "" {
		return "", fmt.Errorf("响应中无文本内容: %s", string(raw))
	}
	return text, nil
}

func extractResponseText(outputJSON json.RawMessage) string {
	if len(outputJSON) == 0 {
		return ""
	}
	var outputArr []interface{}
	if err := json.Unmarshal(outputJSON, &outputArr); err == nil {
		for _, item := range outputArr {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if strVal(m["type"]) != "message" {
				continue
			}
			content, ok := m["content"].([]interface{})
			if !ok {
				continue
			}
			for _, c := range content {
				cm, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				if strVal(cm["type"]) != "output_text" {
					continue
				}
				if t, ok := cm["text"].(string); ok && t != "" {
					return t
				}
			}
		}
	}
	var obj responsesOutput
	if err := json.Unmarshal(outputJSON, &obj); err == nil {
		if obj.Text != "" {
			return obj.Text
		}
		for _, item := range obj.ListValue {
			if item.Message == nil {
				continue
			}
			for _, c := range item.Message.Content {
				if c.Text != "" {
					return c.Text
				}
			}
		}
	}
	var arr []outputItem
	if err := json.Unmarshal(outputJSON, &arr); err == nil {
		for _, item := range arr {
			if item.Message == nil {
				continue
			}
			for _, c := range item.Message.Content {
				if c.Text != "" {
					return c.Text
				}
			}
		}
	}
	return ""
}

func strVal(v interface{}) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

var eventLogMu sync.Mutex

func appendEventLog(logPath, input, response string, meta map[string]string) {
	eventLogMu.Lock()
	defer eventLogMu.Unlock()
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	now := time.Now()
	entry := map[string]string{"time": now.Format(time.RFC3339), "input": input, "response": response}
	for k, v := range meta {
		entry[k] = v
	}
	sep := fmt.Sprintf("\n========== %s", now.Format("2006-01-02 15:04:05"))
	for k, v := range meta {
		sep += fmt.Sprintf(" %s=%s", k, v)
	}
	sep += " ==========\n"
	f.WriteString(sep)
	data, _ := json.Marshal(entry)
	f.Write(data)
	f.WriteString("\n")
}
