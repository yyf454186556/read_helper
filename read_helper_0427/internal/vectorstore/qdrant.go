package vectorstore

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/qdrant/go-client/qdrant"
)

const (
	// DefaultQdrantHost 默认 Qdrant 服务地址（本地安装）
	DefaultQdrantHost = "localhost"
	DefaultQdrantPort = 6334
	// DefaultCollection 默认集合名，可按书或项目区分
	DefaultCollection = "read_helper"
	// DefaultVectorSize 创建集合时的向量维度，需与 embedding 模型输出一致（doubao-embedding-vision-251215 为 2048），换模型时改此常量或调用 EnsureCollection 时传入实际 size
	DefaultVectorSize = 2048
	DefaultDistance  = qdrant.Distance_Cosine
)

// Payload 字段名，检索时按 book_id + chapter_num 过滤
const (
	PayloadBookID   = "book_id"
	PayloadChapter  = "chapter_num"
	PayloadText     = "text"
)

// Client 封装 Qdrant 连接，用于写入 embedding 并按章节过滤检索。
type Client struct {
	c          *qdrant.Client
	collection string
	vectorSize uint64
}

// NewQdrantClient 创建 Qdrant 客户端。host/port 为空则用默认 localhost:6334。
func NewQdrantClient(host string, port int) (*Client, error) {
	if host == "" {
		host = DefaultQdrantHost
	}
	if port <= 0 {
		port = DefaultQdrantPort
	}
	c, err := qdrant.NewClient(&qdrant.Config{
		Host: host,
		Port: port,
	})
	if err != nil {
		return nil, fmt.Errorf("连接 Qdrant: %w", err)
	}
	return &Client{
		c:          c,
		collection: DefaultCollection,
		vectorSize: DefaultVectorSize,
	}, nil
}

// Close 关闭连接。
func (c *Client) Close() error {
	return c.c.Close()
}

// EnsureCollection 若集合不存在则创建；若已存在则取集合信息，仅当向量维度与预期不一致时才删除并重建，避免误删已有数据。
func (c *Client) EnsureCollection(ctx context.Context, collection string, vectorSize uint64) error {
	if collection == "" {
		collection = c.collection
	}
	if vectorSize == 0 {
		vectorSize = c.vectorSize
	}
	collections, err := c.c.ListCollections(ctx)
	if err != nil {
		return fmt.Errorf("列举集合: %w", err)
	}
	exists := false
	for _, name := range collections {
		if name == collection {
			exists = true
			break
		}
	}
	if exists {
		info, err := c.c.GetCollectionInfo(ctx, collection)
		if err != nil {
			return fmt.Errorf("获取集合信息: %w", err)
		}
		existingSize := getCollectionVectorSize(info)
		if existingSize == vectorSize {
			return nil
		}
		if err := c.c.DeleteCollection(ctx, collection); err != nil {
			return fmt.Errorf("删除旧集合 %s（维度 %d 与当前 %d 不一致，需重建）: %w", collection, existingSize, vectorSize, err)
		}
	}
	err = c.c.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: collection,
		VectorsConfig:  qdrant.NewVectorsConfig(&qdrant.VectorParams{Size: vectorSize, Distance: DefaultDistance}),
	})
	if err != nil {
		return fmt.Errorf("创建集合 %s: %w", collection, err)
	}
	return nil
}

// getCollectionVectorSize 从 CollectionInfo 中取出默认向量的 size，无法取得时返回 0。
func getCollectionVectorSize(info *qdrant.CollectionInfo) uint64 {
	if info == nil {
		return 0
	}
	cfg := info.GetConfig()
	if cfg == nil {
		return 0
	}
	params := cfg.GetParams()
	if params == nil {
		return 0
	}
	vc := params.GetVectorsConfig()
	if vc == nil {
		return 0
	}
	// 单向量配置（NewVectorsConfig）时，取 Params
	if p := vc.GetParams(); p != nil {
		return p.GetSize()
	}
	return 0
}

// UpsertChunks 将多段文本及其 embedding 写入 Qdrant，并标记 book_id、chapter_num。
// 同一 (book_id, chapter_num, index) 会覆盖旧点（通过稳定 id 实现）。
func (c *Client) UpsertChunks(ctx context.Context, collection, bookID string, chapterNum int, texts []string, embeddings [][]float32) error {
	if collection == "" {
		collection = c.collection
	}
	if len(texts) != len(embeddings) {
		return fmt.Errorf("texts 与 embeddings 数量不一致: %d vs %d", len(texts), len(embeddings))
	}
	if len(texts) == 0 {
		return nil
	}
	points := make([]*qdrant.PointStruct, 0, len(texts))
	for i, text := range texts {
		if i >= len(embeddings) {
			break
		}
		id := pointID(bookID, chapterNum, i)
		points = append(points, &qdrant.PointStruct{
			Id: qdrant.NewIDNum(id),
			Vectors: qdrant.NewVectorsDense(embeddings[i]),
			Payload: qdrant.NewValueMap(map[string]any{
				PayloadBookID:  bookID,
				PayloadChapter: int64(chapterNum),
				PayloadText:    text,
			}),
		})
	}
	wait := true
	_, err := c.c.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collection,
		Wait:           &wait,
		Points:         points,
	})
	if err != nil {
		return fmt.Errorf("Upsert: %w", err)
	}
	return nil
}

func pointID(bookID string, chapterNum, index int) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(bookID))
	_, _ = h.Write([]byte(fmt.Sprintf("_%d_%d", chapterNum, index)))
	return h.Sum64()
}

// Hit 检索命中的一条，包含原文与章节号。
type Hit struct {
	Text      string
	ChapterNum int
	Score     float32
}

// Search 在指定集合中按 book_id 且 chapter_num <= chapterNumMax 检索，返回与 queryVector 最相似的 top limit 条。
func (c *Client) Search(ctx context.Context, collection, bookID string, chapterNumMax int, queryVector []float32, limit uint64) ([]Hit, error) {
	if collection == "" {
		collection = c.collection
	}
	if limit == 0 {
		limit = 5
	}
	lte := float64(chapterNumMax)
	req := &qdrant.QueryPoints{
		CollectionName: collection,
		Query:          qdrant.NewQueryDense(queryVector),
		Limit:          &limit,
		Filter: &qdrant.Filter{
			Must: []*qdrant.Condition{
				qdrant.NewMatchKeyword(PayloadBookID, bookID),
				qdrant.NewRange(PayloadChapter, &qdrant.Range{Lte: &lte}),
			},
		},
		WithPayload: qdrant.NewWithPayloadInclude(PayloadText, PayloadChapter),
	}
	results, err := c.c.Query(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("Query: %w", err)
	}
	out := make([]Hit, 0, len(results))
	for _, p := range results {
		h := Hit{Score: p.GetScore()}
		if p.GetPayload() != nil {
			if v, ok := p.GetPayload()[PayloadText]; ok && v != nil {
				h.Text = valueToString(v)
			}
			if v, ok := p.GetPayload()[PayloadChapter]; ok && v != nil {
				h.ChapterNum = valueToInt(v)
			}
		}
		out = append(out, h)
	}
	return out, nil
}

func valueToString(v *qdrant.Value) string {
	if v == nil {
		return ""
	}
	if x, ok := v.GetKind().(*qdrant.Value_StringValue); ok {
		return x.StringValue
	}
	return ""
}

func valueToInt(v *qdrant.Value) int {
	if v == nil {
		return 0
	}
	if x, ok := v.GetKind().(*qdrant.Value_IntegerValue); ok {
		return int(x.IntegerValue)
	}
	return 0
}
