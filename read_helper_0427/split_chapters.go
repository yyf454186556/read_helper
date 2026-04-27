package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"read_helper/internal/config"
	"read_helper/internal/embeddings"
	"read_helper/internal/splitter"
	"read_helper/internal/vectorstore"
)

func main() {
	// 提问（基于读到的章节）：go run . ask [--debug] <书名> <章号> <问题>
	if len(os.Args) > 1 && os.Args[1] == "ask" {
		fs := flag.NewFlagSet("ask", flag.ExitOnError)
		debug := fs.Bool("debug", false, "调试模式：打印调用模型的入参与响应")
		_ = fs.Parse(os.Args[2:])
		if fs.NArg() < 3 {
			fmt.Println("用法: go run . ask [--debug] <书名> <读到的章号> <问题>")
			fmt.Println("示例: go run . ask bailuyuan 140 狂徒是谁")
			fmt.Println("      go run . ask --debug tianlong8_utf8 10 段誉在哪里")
			fmt.Println("说明: 书名为 book_chapters 下子目录名或前缀；--debug 时打印模型入参与响应")
			return
		}
		bookName := strings.TrimSpace(fs.Arg(0))
		chapterNum := fs.Arg(1)
		question := strings.Join(fs.Args()[2:], " ")
		bookDir, err := resolveBookDir(bookName)
		if err != nil {
			fmt.Printf("未找到书名对应的目录: %v\n", err)
			return
		}
		config.RequireARKAPIKey()
		askQuestion(bookDir, chapterNum, question, *debug)
		return
	}

	// 世界状态（前缀和）：go run . world [书目录]
	if len(os.Args) > 1 && os.Args[1] == "world" {
		config.RequireARKAPIKey()
		bookDir := config.C.Dir.SummaryDemoChapterDir
		if len(os.Args) > 2 {
			bookDir = os.Args[2]
		}
		buildWorldStates(bookDir)
		return
	}

	// 修复 .raw.txt：go run . fixraw 057 091  [书目录可选]
	if len(os.Args) > 1 && os.Args[1] == "fixraw" {
		if len(os.Args) < 3 {
			fmt.Println("用法: go run . fixraw 057 091  或  go run . fixraw <书目录> 057 091")
			return
		}
		bookDir := config.C.Dir.SummaryDemoChapterDir
		numbers := os.Args[2:]
		if info, err := os.Stat(os.Args[2]); err == nil && info.IsDir() {
			bookDir = os.Args[2]
			if len(os.Args) < 4 {
				fmt.Println("用法: go run . fixraw <书目录> 057 091")
				return
			}
			numbers = os.Args[3:]
		}
		fixRaw(bookDir, numbers)
		return
	}

	// 从 .raw.txt 解析并生成 .json/.md，成功则删除 .raw.txt（与主线隔离）：go run . raw2json [书目录]
	if len(os.Args) > 1 && os.Args[1] == "raw2json" {
		bookDir := config.C.Dir.SummaryDemoChapterDir
		if len(os.Args) > 2 {
			bookDir = os.Args[2]
		}
		runRaw2JSON(bookDir)
		return
	}

	// 摘要：go run . summary（仅第一章） | go run . summary all [书目录]
	if len(os.Args) > 1 && (os.Args[1] == "summary" || os.Args[1] == "summary_demo") {
		config.RequireARKAPIKey()
		if len(os.Args) > 2 && (os.Args[2] == "all" || os.Args[2] == "book") {
			bookDir := config.C.Dir.SummaryDemoChapterDir
			if len(os.Args) > 3 {
				bookDir = os.Args[3]
			}
			processWholeBook(bookDir)
		} else {
			summaryDemo()
		}
		return
	}

	// 一条命令完成：拆分 + 可选摘要 + 可选写入 Qdrant。go run . pipeline [--summary] [--qdrant] <书的txt路径>
	if len(os.Args) > 1 && os.Args[1] == "pipeline" {
		fs := flag.NewFlagSet("pipeline", flag.ExitOnError)
		doSummary := fs.Bool("summary", false, "生成每章摘要并写入 abstracts/")
		doQdrant := fs.Bool("qdrant", false, "按章节顺序 embedding 并写入 Qdrant（严格区分章节）")
		_ = fs.Parse(os.Args[2:])
		if fs.NArg() < 1 {
			fmt.Println("用法: go run . pipeline [--summary] [--qdrant] <书的txt路径>")
			fmt.Println("示例: go run . pipeline book_resource/xxx.txt")
			fmt.Println("      go run . pipeline --summary --qdrant book_resource/xxx.txt")
			return
		}
		bookPath := fs.Arg(0)
		bookDir, err := runSplitOne(bookPath, config.C.Dir.OutputDir)
		if err != nil {
			fmt.Printf("拆分失败: %v\n", err)
			return
		}
		if bookDir == "" {
			fmt.Println("未拆分出章节，后续步骤跳过。")
			return
		}
		if *doSummary || *doQdrant {
			config.RequireARKAPIKey()
		}
		if *doSummary {
			fmt.Printf("\n--- 开始生成摘要: %s ---\n", bookDir)
			processWholeBook(bookDir)
		}
		if *doQdrant {
			fmt.Printf("\n--- 按章节顺序写入 Qdrant: %s ---\n", bookDir)
			runEmbedAndUpsertQdrant(bookDir)
		}
		if !*doSummary && !*doQdrant {
			fmt.Println("未指定 --summary 或 --qdrant，仅完成拆分。")
		} else {
			fmt.Println("\n流水线完成。")
		}
		return
	}

	// 验证 Qdrant 连接与写入/检索：go run . qdrant
	if len(os.Args) > 1 && os.Args[1] == "qdrant" {
		config.RequireARKAPIKey()
		qdrantDemo()
		return
	}

	// HTTP 服务，提供提问接口：go run . serve [端口]，默认从 config.json
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		config.RequireARKAPIKey()
		port := config.C.Serve.DefaultPort
		if len(os.Args) > 2 {
			if p, err := strconv.Atoi(os.Args[2]); err == nil && p > 0 {
				port = p
			}
		}
		runServe(port)
		return
	}

	// 仅拆分：无子命令时，从输入目录拆分到输出目录
	_, err := runSplit(config.C.Dir.InputDir, config.C.Dir.OutputDir)
	if err != nil {
		fmt.Printf("拆分失败: %v\n", err)
		return
	}
	fmt.Println("处理完成！")
}

// qdrantDemo 连接 Qdrant（配置见 config.json），创建集合并做一次写入+检索，用于验证安装。
func qdrantDemo() {
	ctx := context.Background()
	client, err := vectorstore.NewQdrantClient(config.C.Qdrant.Host, config.C.Qdrant.Port)
	if err != nil {
		fmt.Printf("连接 Qdrant 失败: %v\n", err)
		return
	}
	defer client.Close()

	if err := client.EnsureCollection(ctx, config.C.Qdrant.Collection, config.C.Qdrant.VectorSize); err != nil {
		fmt.Printf("创建/检查集合失败: %v\n", err)
		return
	}
	fmt.Println("Qdrant 连接成功，集合已就绪。")

	vec := make([]float32, config.C.Qdrant.VectorSize)
	for i := range vec {
		vec[i] = 0.01 * float32(i%100)
	}
	texts := []string{"这是一段测试文本，用于验证 Qdrant 写入与检索。"}
	embeddings := [][]float32{vec}
	if err := client.UpsertChunks(ctx, config.C.Qdrant.Collection, "test_book", 1, texts, embeddings); err != nil {
		fmt.Printf("写入测试数据失败: %v\n", err)
		return
	}
	fmt.Println("已写入 1 条测试数据。")

	hits, err := client.Search(ctx, config.C.Qdrant.Collection, "test_book", 1, vec, 1)
	if err != nil {
		fmt.Printf("检索失败: %v\n", err)
		return
	}
	if len(hits) == 0 {
		fmt.Println("检索完成，但未命中（正常情况下应命中刚写入的那条）。")
		return
	}
	fmt.Printf("检索命中: 章节=%d 分数=%.4f 文本=%s\n", hits[0].ChapterNum, hits[0].Score, hits[0].Text)
	fmt.Println("Qdrant 验证通过：连接、写入、检索均正常。")
}

// runEmbedAndUpsertQdrant 按章节顺序读取 bookDir 下所有章节，分块后 embedding 并写入 Qdrant，严格按 book_id + chapter_num 区分。
func runEmbedAndUpsertQdrant(bookDir string) {
	chapterPaths, err := listChapterFilesSorted(bookDir)
	if err != nil {
		fmt.Printf("列举章节失败: %v\n", err)
		return
	}
	if len(chapterPaths) == 0 {
		fmt.Printf("在 %s 下未找到章节 .txt 文件\n", bookDir)
		return
	}

	ctx := context.Background()
	vc, err := vectorstore.NewQdrantClient(config.C.Qdrant.Host, config.C.Qdrant.Port)
	if err != nil {
		fmt.Printf("连接 Qdrant 失败: %v\n", err)
		return
	}
	defer vc.Close()
	if err := vc.EnsureCollection(ctx, config.C.Qdrant.Collection, config.C.Qdrant.VectorSize); err != nil {
		fmt.Printf("创建/检查集合失败: %v\n", err)
		return
	}

	embClient := embeddings.NewClient("", config.C.Embedding.Model, config.C.Embedding.BaseURL)
	bookID := filepath.Base(bookDir)
	cfg := &config.C.Embedding

	for _, chapterPath := range chapterPaths {
		content, err := os.ReadFile(chapterPath)
		if err != nil {
			fmt.Printf("读取章节 %s 失败: %v\n", chapterPath, err)
			continue
		}
		text := strings.TrimSpace(string(content))
		if text == "" {
			continue
		}

		chapterNumStr := chapterNumber(chapterPath)
		chapterNum, _ := strconv.Atoi(chapterNumStr)
		if chapterNum <= 0 {
			chapterNum = 1 // 兜底
		}

		chunks := chunkText(text, cfg.MaxChunkRunes)
		if len(chunks) == 0 {
			continue
		}

		// 先对本章所有批次做 embedding（带重试），任一批失败则本章不写入 Qdrant
		var allTexts []string
		var allVecs [][]float32
		chapterOK := true
		batchSize := cfg.BatchSize
		if batchSize <= 0 {
			batchSize = 1
		}
		for i := 0; i < len(chunks); i += batchSize {
			end := i + batchSize
			if end > len(chunks) {
				end = len(chunks)
			}
			batch := chunks[i:end]
			var vecs [][]float32
			var lastErr error
			for attempt := 0; attempt < cfg.MaxRetries; attempt++ {
				vecs, lastErr = embClient.Embed(batch)
				if lastErr == nil {
					break
				}
				if attempt < cfg.MaxRetries-1 && cfg.RetryDelaySeconds > 0 {
					time.Sleep(time.Duration(cfg.RetryDelaySeconds) * time.Second)
				}
			}
			if lastErr != nil {
				fmt.Printf("[%s] embedding 失败(已重试 %d 次)，跳过本章，不写入 Qdrant: %v\n", chapterNumStr, cfg.MaxRetries, lastErr)
				chapterOK = false
				break
			}
			allTexts = append(allTexts, batch...)
			allVecs = append(allVecs, vecs...)
		}
		if !chapterOK || len(allTexts) == 0 {
			continue
		}
		if err := vc.UpsertChunks(ctx, config.C.Qdrant.Collection, bookID, chapterNum, allTexts, allVecs); err != nil {
			fmt.Printf("[%s] 写入 Qdrant 失败: %v\n", chapterNumStr, err)
			continue
		}
		fmt.Printf("[%s] 已写入 %d 块\n", chapterNumStr, len(allTexts))
	}
	fmt.Println("Qdrant 按章节写入完成。")
}

// chunkText 将正文按 maxRunes 一段切分，尽量在换行处断开，返回非空片段。
func chunkText(text string, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = config.C.Embedding.MaxChunkRunes
	}
	if maxRunes <= 0 {
		maxRunes = 1500
	}
	var out []string
	runes := []rune(text)
	for len(runes) > 0 {
		n := maxRunes
		if n > len(runes) {
			n = len(runes)
		}
		chunk := runes[:n]
		// 若未取到末尾，尽量在换行处截断
		if n < len(runes) {
			lastNewline := -1
			for i := len(chunk) - 1; i >= 0; i-- {
				if chunk[i] == '\n' {
					lastNewline = i
					break
				}
			}
			if lastNewline > 0 {
				chunk = chunk[:lastNewline+1]
			}
		}
		s := strings.TrimSpace(string(chunk))
		if s != "" {
			out = append(out, s)
		}
		runes = runes[len(chunk):]
	}
	return out
}

// resolveBookDir 根据用户输入的书名（如 bailuyuan）解析为输出目录下的完整路径（如 book_chapters/bailuyuan_utf8）
func resolveBookDir(bookName string) (string, error) {
	if bookName == "" {
		return "", fmt.Errorf("书名为空")
	}
	outputDir := config.C.Dir.OutputDir
	// 先试精确匹配，再试 书名_utf8
	candidates := []string{
		filepath.Join(outputDir, bookName),
		filepath.Join(outputDir, bookName+"_utf8"),
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, nil
		}
	}
	// 再试：列出 outputDir 下子目录，找名称等于或以前缀匹配的
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return "", fmt.Errorf("读取 %s: %w", outputDir, err)
	}
	var match string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == bookName {
			return filepath.Join(outputDir, name), nil // 优先精确
		}
		if strings.HasPrefix(name, bookName) {
			if match != "" {
				return "", fmt.Errorf("书名「%s」对应多个目录: %s, %s", bookName, match, name)
			}
			match = name
		}
	}
	if match != "" {
		return filepath.Join(outputDir, match), nil
	}
	return "", fmt.Errorf("在 %s 下未找到以「%s」为名或前缀的目录", outputDir, bookName)
}

// runSplitOne 将指定的一本书（.txt 路径）拆分为章节，返回生成的书籍目录；无章节时返回空字符串
func runSplitOne(bookPath, outputDir string) (string, error) {
	bookPath = filepath.Clean(bookPath)
	info, err := os.Stat(bookPath)
	if err != nil {
		return "", fmt.Errorf("无法访问 %s: %w", bookPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("请指定书的 .txt 文件路径，而不是目录: %s", bookPath)
	}
	if !strings.HasSuffix(strings.ToLower(filepath.Base(bookPath)), ".txt") {
		return "", fmt.Errorf("请指定 .txt 文件: %s", bookPath)
	}
	inputDir := filepath.Dir(bookPath)
	filename := filepath.Base(bookPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("创建输出目录: %w", err)
	}
	if err := splitter.NewRegexSplitter().Split(inputDir, filename, outputDir); err != nil {
		return "", err
	}
	baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
	bookDir := filepath.Join(outputDir, baseName)
	if info, err := os.Stat(bookDir); err != nil || !info.IsDir() {
		return "", nil // 未拆出章节，未建目录
	}
	return bookDir, nil
}

// runSplit 从 inputDir 读取所有 .txt，按章节拆分到 outputDir 下各子目录，返回生成的书籍目录列表（用于后续摘要）
func runSplit(inputDir, outputDir string) ([]string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("创建输出目录: %w", err)
	}
	files, err := os.ReadDir(inputDir)
	if err != nil {
		return nil, fmt.Errorf("读取目录 %s: %w", inputDir, err)
	}
	var bookDirs []string
	for _, entry := range files {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".txt") {
			continue
		}
		fmt.Printf("正在处理文件: %s\n", entry.Name())
		if err := splitter.NewRegexSplitter().Split(inputDir, entry.Name(), outputDir); err != nil {
			fmt.Printf("处理文件 %s 时出错: %v\n", entry.Name(), err)
			continue
		}
		// 与 SplitChapters 一致：子目录名 = 文件名去掉扩展名；仅当目录存在时加入（无章节时不会建目录）
		baseName := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		bookDir := filepath.Join(outputDir, baseName)
		if info, err := os.Stat(bookDir); err == nil && info.IsDir() {
			bookDirs = append(bookDirs, bookDir)
		}
	}
	return bookDirs, nil
}
