package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 全局配置：从 JSON 文件读取，未设置或文件不存在时使用内置默认值。
// 配置文件路径：环境变量 READ_HELPER_CONFIG，未设置时为当前目录下的 config.json。
// ARK_API_KEY 仍仅从环境变量读取，不放在配置文件中。

// C 全局配置单例，Load() 时填充。
var C struct {
	Dir          DirConfig
	Qdrant       QdrantConfig
	Embedding    EmbeddingConfig
	LLM          LLMConfig
	Serve        ServeConfig
	Summary      SummaryConfig
	VectorSearch VectorSearchConfig
}

// DirConfig 目录与路径
type DirConfig struct {
	InputDir               string `json:"input_dir"`                 // 原始书存放目录
	OutputDir              string `json:"output_dir"`                // 拆分后章节根目录
	SummaryDirName         string `json:"summary_dir_name"`          // 摘要子目录名，如 abstracts
	WorldDirName           string `json:"world_dir_name"`            // 世界状态子目录名，如 world
	SummaryDemoChapterDir  string `json:"summary_demo_chapter_dir"`  // summary 单章 demo 默认书目录
}

// QdrantConfig 向量库
type QdrantConfig struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Collection  string `json:"collection"`
	VectorSize  uint64 `json:"vector_size"`
}

// EmbeddingConfig 文本嵌入
type EmbeddingConfig struct {
	BaseURL           string `json:"base_url"`
	Model             string `json:"model"`
	BatchSize         int    `json:"batch_size"`
	MaxRetries        int    `json:"max_retries"`
	RetryDelaySeconds int    `json:"retry_delay_seconds"`
	MaxChunkRunes     int    `json:"max_chunk_runes"`
}

// LLMConfig 大模型（默认模型优先；可选备选模型在回复无法解析为 JSON 时自动重试一次）
type LLMConfig struct {
	BaseURL         string `json:"base_url"`
	Model           string `json:"model"`
	EventLogPath    string `json:"event_log_path"`
	FallbackModel   string `json:"fallback_model"`   // 可选；当默认模型回复无法解析为 JSON 时，用此模型再试一次
	FallbackBaseURL string `json:"fallback_base_url"` // 可选；备选模型所在 base_url，空则与 base_url 相同
}

// ServeConfig HTTP 服务
type ServeConfig struct {
	DefaultPort int `json:"default_port"`
}

// SummaryConfig 整书摘要
type SummaryConfig struct {
	Concurrency int `json:"concurrency"`
}

// VectorSearchConfig 向量检索
type VectorSearchConfig struct {
	DefaultLimit uint64 `json:"default_limit"`
}

// fileConfig 与 JSON 文件结构一致（可含空字段）
type fileConfig struct {
	Dir          *DirConfig          `json:"dir,omitempty"`
	Qdrant       *QdrantConfig       `json:"qdrant,omitempty"`
	Embedding    *EmbeddingConfig    `json:"embedding,omitempty"`
	LLM          *LLMConfig          `json:"llm,omitempty"`
	Serve        *ServeConfig        `json:"serve,omitempty"`
	Summary      *SummaryConfig      `json:"summary,omitempty"`
	VectorSearch *VectorSearchConfig `json:"vector_search,omitempty"`
}

func init() {
	Load()
}

// ConfigPath 返回配置文件路径（用于加载与文档）。
func ConfigPath() string {
	p := os.Getenv("READ_HELPER_CONFIG")
	if p != "" {
		return strings.TrimSpace(p)
	}
	return "config.json"
}

// Load 从配置文件重新加载；文件不存在或字段缺失时保留内置默认值。
func Load() {
	setDefaults()
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "读取配置 %s 失败: %v，使用默认配置\n", path, err)
		}
		return
	}
	var f fileConfig
	if err := json.Unmarshal(data, &f); err != nil {
		fmt.Fprintf(os.Stderr, "解析配置 %s 失败: %v，使用默认配置\n", path, err)
		return
	}
	if f.Dir != nil {
		mergeDir(f.Dir)
	}
	if f.Qdrant != nil {
		mergeQdrant(f.Qdrant)
	}
	if f.Embedding != nil {
		mergeEmbedding(f.Embedding)
	}
	if f.LLM != nil {
		mergeLLM(f.LLM)
	}
	if f.Serve != nil {
		mergeServe(f.Serve)
	}
	if f.Summary != nil {
		mergeSummary(f.Summary)
	}
	if f.VectorSearch != nil {
		mergeVectorSearch(f.VectorSearch)
	}
}

func setDefaults() {
	C.Dir = DirConfig{
		InputDir:              "book_resource",
		OutputDir:             "book_chapters",
		SummaryDirName:        "abstracts",
		WorldDirName:          "world",
		SummaryDemoChapterDir:  "book_chapters/yongzhengwangchao_utf8",
	}
	C.Qdrant = QdrantConfig{
		Host:       "localhost",
		Port:       6334,
		Collection: "read_helper",
		VectorSize: 2048,
	}
	C.Embedding = EmbeddingConfig{
		BaseURL:           "https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal",
		Model:             "doubao-embedding-vision-251215",
		BatchSize:         1,
		MaxRetries:        3,
		RetryDelaySeconds: 2,
		MaxChunkRunes:     1500,
	}
	C.LLM = LLMConfig{
		BaseURL:      "https://ark.cn-beijing.volces.com/api/v3",
		Model:        "doubao-seed-1-6-lite-251015",
		EventLogPath: "event.log",
	}
	C.Serve = ServeConfig{DefaultPort: 8080}
	C.Summary = SummaryConfig{Concurrency: 10}
	C.VectorSearch = VectorSearchConfig{DefaultLimit: 10}
}

func mergeDir(d *DirConfig) {
	if d.InputDir != "" {
		C.Dir.InputDir = d.InputDir
	}
	if d.OutputDir != "" {
		C.Dir.OutputDir = d.OutputDir
	}
	if d.SummaryDirName != "" {
		C.Dir.SummaryDirName = d.SummaryDirName
	}
	if d.WorldDirName != "" {
		C.Dir.WorldDirName = d.WorldDirName
	}
	if d.SummaryDemoChapterDir != "" {
		C.Dir.SummaryDemoChapterDir = d.SummaryDemoChapterDir
	}
}

func mergeQdrant(q *QdrantConfig) {
	if q.Host != "" {
		C.Qdrant.Host = q.Host
	}
	if q.Port > 0 {
		C.Qdrant.Port = q.Port
	}
	if q.Collection != "" {
		C.Qdrant.Collection = q.Collection
	}
	if q.VectorSize > 0 {
		C.Qdrant.VectorSize = q.VectorSize
	}
}

func mergeEmbedding(e *EmbeddingConfig) {
	if e.BaseURL != "" {
		C.Embedding.BaseURL = e.BaseURL
	}
	if e.Model != "" {
		C.Embedding.Model = e.Model
	}
	if e.BatchSize > 0 {
		C.Embedding.BatchSize = e.BatchSize
	}
	if e.MaxRetries > 0 {
		C.Embedding.MaxRetries = e.MaxRetries
	}
	if e.RetryDelaySeconds > 0 {
		C.Embedding.RetryDelaySeconds = e.RetryDelaySeconds
	}
	if e.MaxChunkRunes > 0 {
		C.Embedding.MaxChunkRunes = e.MaxChunkRunes
	}
}

func mergeLLM(l *LLMConfig) {
	if l.BaseURL != "" {
		C.LLM.BaseURL = l.BaseURL
	}
	if l.Model != "" {
		C.LLM.Model = l.Model
	}
	if l.EventLogPath != "" {
		C.LLM.EventLogPath = l.EventLogPath
	}
	if l.FallbackModel != "" {
		C.LLM.FallbackModel = l.FallbackModel
	}
	if l.FallbackBaseURL != "" {
		C.LLM.FallbackBaseURL = l.FallbackBaseURL
	}
}

func mergeServe(s *ServeConfig) {
	if s.DefaultPort > 0 {
		C.Serve.DefaultPort = s.DefaultPort
	}
}

func mergeSummary(s *SummaryConfig) {
	if s.Concurrency > 0 {
		C.Summary.Concurrency = s.Concurrency
	}
}

func mergeVectorSearch(v *VectorSearchConfig) {
	if v.DefaultLimit > 0 {
		C.VectorSearch.DefaultLimit = v.DefaultLimit
	}
}

// RequireARKAPIKey 检查环境变量 ARK_API_KEY 是否已设置；未设置时打印说明并退出。
// 在需要调用大模型或 embedding 的子命令入口处调用。
func RequireARKAPIKey() {
	key := strings.TrimSpace(os.Getenv("ARK_API_KEY"))
	if key == "" {
		fmt.Fprintln(os.Stderr, "未设置 ARK_API_KEY。请设置环境变量后再运行（例如：export ARK_API_KEY=你的密钥）。")
		os.Exit(1)
	}
}

// OutputDirAbs 返回输出目录的绝对路径（便于 resolveBookDir 等使用）。
func OutputDirAbs() (string, error) {
	return filepath.Abs(C.Dir.OutputDir)
}
