package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"read_helper/internal/config"
	"read_helper/internal/llm"
	"read_helper/internal/response"
)

func summaryDemo() {
	chapterPath, err := findFirstChapterFile()
	if err != nil {
		fmt.Printf("查找章节文件失败: %v\n", err)
		return
	}
	fmt.Printf("使用章节文件: %s\n", chapterPath)
	if summaryFilesExist(chapterPath) {
		fmt.Printf("[%s] 已存在，跳过\n", chapterNumber(chapterPath))
		return
	}
	client := llm.NewVolcClient("", config.C.LLM.Model, config.C.LLM.BaseURL, config.C.LLM.EventLogPath)
	if err := processOneChapter(context.Background(), chapterPath, "", client); err != nil {
		fmt.Printf("处理失败: %v\n", err)
		return
	}
}

// summaryFilesExist 判断该章节对应的摘要子目录下 num.json 与 num.md 是否都已存在（用于断点续传）
func summaryFilesExist(chapterPath string) bool {
	sdir := filepath.Join(filepath.Dir(chapterPath), config.C.Dir.SummaryDirName)
	num := chapterNumber(chapterPath)
	jsonPath := filepath.Join(sdir, num+".json")
	mdPath := filepath.Join(sdir, num+".md")
	_, errJ := os.Stat(jsonPath)
	_, errM := os.Stat(mdPath)
	return errJ == nil && errM == nil
}

// processOneChapter 处理单章：若已存在则跳过；否则读正文 -> 调大模型 -> 写摘要 JSON + MD。bookName 用于 event.log，可为空。
func processOneChapter(ctx context.Context, chapterPath string, bookName string, client llm.Caller) error {
	if summaryFilesExist(chapterPath) {
		return nil // 已存在，跳过（调用方会打印日志）
	}
	content, err := os.ReadFile(chapterPath)
	if err != nil {
		return fmt.Errorf("读取章节: %w", err)
	}
	chapterContent := strings.TrimSpace(string(content))
	if chapterContent == "" {
		return fmt.Errorf("章节内容为空")
	}

	num := chapterNumber(chapterPath)
	meta := map[string]string{"chapter": num}
	if bookName != "" {
		meta["book"] = bookName
	}

	prompt := buildSummaryPrompt(chapterContent)
	fmt.Printf("[%s] 正在调用大模型...\n", num)
	reply, summary, err := callLLMAndParseSummary(ctx, client, prompt, meta)
	if err != nil {
		// 若为“无法解析 JSON”且配置了备选模型，用备选模型重试一次（默认模型优先，计费更便宜）
		if isJSONParseError(err) && config.C.LLM.FallbackModel != "" {
			fallbackClient := llm.NewVolcClient("", config.C.LLM.FallbackModel, fallbackLLMBaseURL(), config.C.LLM.EventLogPath)
			fmt.Printf("[%s] 默认模型回复无法解析，改用备选模型重试...\n", num)
			reply2, summary2, err2 := callLLMAndParseSummary(ctx, fallbackClient, prompt, meta)
			if err2 == nil && summary2 != nil {
				summary = summary2
				err = nil
			} else if reply2 != "" {
				reply = reply2 // 写备选模型的原始回复便于排查
			}
		}
		if err != nil {
			writeRawSummary(chapterPath, reply)
			return err
		}
	}

	if _, err := writeSummaryFile(chapterPath, summary); err != nil {
		return fmt.Errorf("写入摘要 JSON: %w", err)
	}
	if _, err := writeSummaryMarkdown(chapterPath, summary); err != nil {
		return fmt.Errorf("写入摘要 MD: %w", err)
	}
	fmt.Printf("[%s] 摘要已写入 %s/%s.json 与 %s.md\n", num, config.C.Dir.SummaryDirName, num, num)
	return nil
}

// processWholeBook 处理整本书：从第一章起检查，已存在 .json+.md 的章节跳过（断点续传），未存在的用 summaryConcurrency 个 goroutine 并发处理。
func processWholeBook(bookDir string) {
	chapterPaths, err := listChapterFilesSorted(bookDir)
	if err != nil {
		fmt.Printf("列举章节失败: %v\n", err)
		return
	}
	if len(chapterPaths) == 0 {
		fmt.Printf("在 %s 下未找到章节 .txt 文件\n", bookDir)
		return
	}

	bookName := filepath.Base(bookDir)
	fmt.Printf("开始处理整本书: %s，共 %d 章，并发数 %d（已存在 .json+.md 的章节将跳过）\n", bookName, len(chapterPaths), config.C.Summary.Concurrency)
	ctx := context.Background()
	client := llm.NewVolcClient("", config.C.LLM.Model, config.C.LLM.BaseURL, config.C.LLM.EventLogPath)

	jobs := make(chan string, len(chapterPaths))
	var wg sync.WaitGroup
	for i := 0; i < config.C.Summary.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chapterPath := range jobs {
				num := chapterNumber(chapterPath)
				if summaryFilesExist(chapterPath) {
					fmt.Printf("[%s] 已存在，跳过\n", num)
					continue
				}
				if err := processOneChapter(ctx, chapterPath, bookName, client); err != nil {
					fmt.Printf("[%s] 失败: %v\n", num, err)
				}
			}
		}()
	}
	for _, p := range chapterPaths {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	fmt.Println("整书摘要处理完成。")
}

// listChapterFilesSorted 返回 bookDir 下所有章节 .txt 路径，按章号排序（001, 002, ...）
func listChapterFilesSorted(bookDir string) ([]string, error) {
	entries, err := os.ReadDir(bookDir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".txt") {
			continue
		}
		// 跳过摘要目录里的 .raw.txt 等（本章节在同级目录，只有章节是 001_xxx.txt 这种）
		if idx := strings.Index(name, "_"); idx > 0 {
			paths = append(paths, filepath.Join(bookDir, name))
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		return chapterNumber(paths[i]) < chapterNumber(paths[j])
	})
	return paths, nil
}

// findFirstChapterFile 返回第一章 txt 的路径（当前 demo 固定为 001_ 开头）
func findFirstChapterFile() (string, error) {
	demoDir := config.C.Dir.SummaryDemoChapterDir
	entries, err := os.ReadDir(demoDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".txt") {
			continue
		}
		if strings.HasPrefix(name, "001_") {
			return filepath.Join(demoDir, name), nil
		}
	}
	return "", fmt.Errorf("在 %s 下未找到 001_ 开头的章节文件", demoDir)
}

// callLLMAndParseSummary 调用大模型并解析为 JSON 摘要；返回 (原始回复, 解析后的 summary, error)。
func callLLMAndParseSummary(ctx context.Context, client llm.Caller, prompt string, meta map[string]string) (reply string, summary map[string]interface{}, err error) {
	reply, err = client.Call(ctx, prompt, meta)
	if err != nil {
		return reply, nil, err
	}
	rawJSON := response.ExtractJSON(reply)
	if rawJSON == "" {
		return reply, nil, fmt.Errorf("无法从回复中解析出 JSON，原始回复已写入 .raw.txt")
	}
	summary = make(map[string]interface{})
	if err := json.Unmarshal([]byte(rawJSON), &summary); err != nil {
		return reply, nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	return reply, summary, nil
}

// isJSONParseError 判断是否为「回复无法解析为 JSON」类错误（用于决定是否走备选模型）。
func isJSONParseError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "无法从回复中解析出 JSON") || strings.Contains(s, "JSON 解析失败")
}

// fallbackLLMBaseURL 返回备选模型使用的 base_url；未配置时与默认模型相同。
func fallbackLLMBaseURL() string {
	if config.C.LLM.FallbackBaseURL != "" {
		return config.C.LLM.FallbackBaseURL
	}
	return config.C.LLM.BaseURL
}

func buildSummaryPrompt(chapterContent string) string {
	return `你是一个小说章节分析助手。请**仅**根据下面提供的本章正文进行分析，不要引用、联想或推断本章以外的内容；若文中未提及某项，则对应字段留空或使用空数组/空对象。

【输出格式——必须严格遵守，否则程序无法解析】
你的回复有且仅有：一个合法的 JSON 对象。从第一个 { 开始，到最后一个 } 结束，中间不要插入任何其他内容。

禁止行为（违反任一条都会导致解析失败）：
- 禁止使用 Markdown：不要用 ###、####、**、- 列表、段落式叙述。
- 禁止写「人物梳理」「一、二、三」「基本信息」「主要经历」等分析文章或小标题。
- 禁止用 markdown 代码块（三个反引号加 json 那种）包裹 JSON。
- 禁止在 JSON 前或后写任何说明、总结、备注。

正确做法：直接输出下面这一份结构的 JSON 对象，键名与层级必须一致，内容根据正文填写；整段回复除该 JSON 外不要有任何字符。

必须使用的 JSON 结构（键名不可改，值为根据正文填写的字符串/数字/数组/对象）：

{
  "meta": {
    "book_title": "书名（本章未提及可留空）",
    "chapter_title": "本章标题",
    "current_chapter": 1
  },
  "characters": {
    "角色ID_英文或拼音": {
      "names": ["中文名"],
      "known_roles": ["身份/角色"],
      "description": "简短描述",
      "relationships": { "关系类型": ["其他角色ID"] },
      "status": { "alive": true, "location": "所在地" }
    }
  },
  "locations": {
    "地点ID": {
      "names": ["中文名"],
      "type": "town|building|region|...",
      "description": "简短描述"
    }
  },
  "timeline": [
    {
      "id": "event_1",
      "chapter": 1,
      "summary": "事件简述",
      "participants": ["角色ID"],
      "location": "地点ID",
      "story_time": "文中时间或 unknown"
    }
  ],
  "mysteries": [
    {
      "subject": "悬念或未解之处",
      "introduced_at": 1,
      "status": "unknown|hinted|resolved"
    }
  ]
}

请直接以上述 JSON 结构输出你的分析结果，不要任何前缀、后缀或 Markdown。下面为本章正文。

本章正文如下：

` + chapterContent
}

// chapterNumber 从章节文件名提取数字前缀，如 001_第一回_xxx.txt -> 001
func chapterNumber(chapterPath string) string {
	base := strings.TrimSuffix(filepath.Base(chapterPath), filepath.Ext(chapterPath))
	if idx := strings.Index(base, "_"); idx >= 0 {
		return base[:idx]
	}
	return base
}

// summaryDir 返回与章节同级的摘要目录路径，并确保目录存在
func summaryDir(chapterPath string) (string, error) {
	dir := filepath.Dir(chapterPath)
	summaryPath := filepath.Join(dir, config.C.Dir.SummaryDirName)
	if err := os.MkdirAll(summaryPath, 0755); err != nil {
		return "", err
	}
	return summaryPath, nil
}

func writeSummaryFile(chapterPath string, summary map[string]interface{}) (string, error) {
	sdir, err := summaryDir(chapterPath)
	if err != nil {
		return "", err
	}
	num := chapterNumber(chapterPath)
	outPath := filepath.Join(sdir, num+".json")

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return "", err
	}
	return outPath, nil
}

// writeSummaryMarkdown 将摘要写成人类可读的 Markdown，放入与章节同级的「摘要」目录，文件名仅保留数字如 001.md
func writeSummaryMarkdown(chapterPath string, summary map[string]interface{}) (string, error) {
	sdir, err := summaryDir(chapterPath)
	if err != nil {
		return "", err
	}
	num := chapterNumber(chapterPath)
	outPath := filepath.Join(sdir, num+".md")

	md := formatSummaryAsMarkdown(summary)
	if err := os.WriteFile(outPath, []byte(md), 0644); err != nil {
		return "", err
	}
	return outPath, nil
}

func formatSummaryAsMarkdown(summary map[string]interface{}) string {
	var b strings.Builder

	// meta
	if meta, ok := toMap(summary["meta"]); ok {
		b.WriteString("## 概览\n\n")
		b.WriteString("- **书名**: " + toString(meta["book_title"]) + "\n")
		b.WriteString("- **本章标题**: " + toString(meta["chapter_title"]) + "\n")
		b.WriteString("- **当前回数**: " + toString(meta["current_chapter"]) + "\n\n")
	}

	// characters
	if chars, ok := toMap(summary["characters"]); ok && len(chars) > 0 {
		b.WriteString("## 人物\n\n")
		for id, v := range chars {
			c, _ := toMap(v)
			b.WriteString("### " + id + "\n")
			b.WriteString("- 名称: " + strings.Join(toStringSlice(c["names"]), " / ") + "\n")
			b.WriteString("- 身份/角色: " + strings.Join(toStringSlice(c["known_roles"]), "、") + "\n")
			b.WriteString("- 描述: " + toString(c["description"]) + "\n")
			if rel, ok := toMap(c["relationships"]); ok && len(rel) > 0 {
				b.WriteString("- 关系: ")
				var rs []string
				for k, vals := range rel {
					rs = append(rs, k+": "+strings.Join(toStringSlice(vals), ","))
				}
				b.WriteString(strings.Join(rs, "；") + "\n")
			}
			if status, ok := toMap(c["status"]); ok {
				b.WriteString("- 状态: 存活=" + toString(status["alive"]) + "，所在地=" + toString(status["location"]) + "\n")
			}
			b.WriteString("\n")
		}
	}

	// locations
	if locs, ok := toMap(summary["locations"]); ok && len(locs) > 0 {
		b.WriteString("## 地点\n\n")
		for id, v := range locs {
			loc, _ := toMap(v)
			b.WriteString("### " + id + "\n")
			b.WriteString("- 名称: " + strings.Join(toStringSlice(loc["names"]), " / ") + "\n")
			b.WriteString("- 类型: " + toString(loc["type"]) + "\n")
			b.WriteString("- 描述: " + toString(loc["description"]) + "\n\n")
		}
	}

	// timeline
	if tl, ok := toSlice(summary["timeline"]); ok && len(tl) > 0 {
		b.WriteString("## 时间线\n\n")
		for i, v := range tl {
			ev, _ := toMap(v)
			b.WriteString(fmt.Sprintf("%d. **%s**（第%s回）\n", i+1, toString(ev["summary"]), toString(ev["chapter"])))
			b.WriteString("   - 参与者: " + strings.Join(toStringSlice(ev["participants"]), "、") + "\n")
			b.WriteString("   - 地点: " + toString(ev["location"]) + "，故事时间: " + toString(ev["story_time"]) + "\n\n")
		}
	}

	// mysteries
	if my, ok := toSlice(summary["mysteries"]); ok && len(my) > 0 {
		b.WriteString("## 悬念与未解\n\n")
		for i, v := range my {
			m, _ := toMap(v)
			b.WriteString(fmt.Sprintf("%d. **%s**（引入于第%s回，状态: %s）\n\n", i+1, toString(m["subject"]), toString(m["introduced_at"]), toString(m["status"])))
		}
	}

	return b.String()
}

func toMap(v interface{}) (map[string]interface{}, bool) {
	if v == nil {
		return nil, false
	}
	m, ok := v.(map[string]interface{})
	return m, ok
}

func toSlice(v interface{}) ([]interface{}, bool) {
	if v == nil {
		return nil, false
	}
	s, ok := v.([]interface{})
	return s, ok
}

func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	case bool:
		if x {
			return "是"
		}
		return "否"
	default:
		return fmt.Sprintf("%v", x)
	}
}

func toStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	sl, ok := v.([]interface{})
	if !ok {
		return nil
	}
	var out []string
	for _, e := range sl {
		out = append(out, toString(e))
	}
	return out
}

func writeRawSummary(chapterPath string, raw string) {
	sdir, err := summaryDir(chapterPath)
	if err != nil {
		fmt.Printf("创建摘要目录失败: %v\n", err)
		return
	}
	num := chapterNumber(chapterPath)
	outPath := filepath.Join(sdir, num+".raw.txt")
	_ = os.WriteFile(outPath, []byte(raw), 0644)
	fmt.Printf("原始回复已写入: %s\n", outPath)
}
