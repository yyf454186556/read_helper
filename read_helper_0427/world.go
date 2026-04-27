package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"read_helper/internal/ask"
	"read_helper/internal/config"
	"read_helper/internal/embeddings"
	"read_helper/internal/llm"
	"read_helper/internal/response"
	"read_helper/internal/vectorstore"
)

// listSummaryChapterNumbers 返回 bookDir 下已有摘要的章节号列表（来自 abstracts/*.json），按章号排序
func listSummaryChapterNumbers(bookDir string) ([]string, error) {
	abstractsDir := filepath.Join(bookDir, config.C.Dir.SummaryDirName)
	entries, err := os.ReadDir(abstractsDir)
	if err != nil {
		return nil, err
	}
	var numbers []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, "world_") {
			continue
		}
		num := strings.TrimSuffix(name, ".json")
		if num == "" {
			continue
		}
		numbers = append(numbers, num)
	}
	sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })
	return numbers, nil
}

func worldStatePath(bookDir string, num string) string {
	return filepath.Join(bookDir, config.C.Dir.WorldDirName, num+".json")
}

func summaryJSONPath(bookDir string, num string) string {
	return filepath.Join(bookDir, config.C.Dir.SummaryDirName, num+".json")
}

func loadJSON(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func saveWorldState(bookDir string, num string, world map[string]interface{}) error {
	worldDir := filepath.Join(bookDir, config.C.Dir.WorldDirName)
	if err := os.MkdirAll(worldDir, 0755); err != nil {
		return err
	}
	path := worldStatePath(bookDir, num)
	data, err := json.MarshalIndent(world, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// buildWorldStates 从第一章起串行构建世界状态：第 1 章世界 = 第 1 章摘要；第 n 章世界 = 第 n-1 章世界 + 第 n 章摘要（由大模型合并）
func buildWorldStates(bookDir string) {
	numbers, err := listSummaryChapterNumbers(bookDir)
	if err != nil {
		fmt.Printf("列举摘要章节失败: %v\n", err)
		return
	}
	if len(numbers) == 0 {
		fmt.Printf("在 %s/%s 下未找到任何摘要 .json\n", bookDir, config.C.Dir.SummaryDirName)
		return
	}

	bookName := filepath.Base(bookDir)
	fmt.Printf("开始构建世界状态: %s，共 %d 章（串行执行）\n", bookName, len(numbers))
	ctx := context.Background()
	var client llm.Caller = llm.NewVolcClient("", config.C.LLM.Model, config.C.LLM.BaseURL, config.C.LLM.EventLogPath)

	for i, num := range numbers {
		worldPath := worldStatePath(bookDir, num)
		if _, err := os.Stat(worldPath); err == nil {
			fmt.Printf("[%s] 世界状态已存在，跳过\n", num)
			continue
		}

		summaryPath := summaryJSONPath(bookDir, num)
		currentSummary, err := loadJSON(summaryPath)
		if err != nil {
			fmt.Printf("[%s] 读取摘要失败: %v\n", num, err)
			continue
		}

		var nextWorld map[string]interface{}
		if i == 0 {
			// 第一章：世界状态 = 本章摘要
			nextWorld = currentSummary
			fmt.Printf("[%s] 第一章，世界状态 = 本章摘要\n", num)
		} else {
			prevNum := numbers[i-1]
			prevWorldPath := worldStatePath(bookDir, prevNum)
			prevWorld, err := loadJSON(prevWorldPath)
			if err != nil {
				fmt.Printf("[%s] 读取上一章世界状态 %s 失败: %v\n", num, prevNum, err)
				continue
			}
			// 调用大模型合并：上一章世界 + 本章摘要 -> 本章世界
			prompt := buildWorldMergePrompt(prevWorld, currentSummary, num)
			meta := map[string]string{"chapter": num, "book": bookName, "mode": "world_merge"}
			fmt.Printf("[%s] 正在合并世界状态（%s + 本章摘要）...\n", num, prevNum)
			reply, err := client.Call(ctx, prompt, meta)
			if err != nil {
				fmt.Printf("[%s] 大模型调用失败: %v\n", num, err)
				continue
			}
			rawJSON := response.ExtractJSON(reply)
			if rawJSON == "" {
				fmt.Printf("[%s] 无法从回复中解析出 JSON\n", num)
				continue
			}
			if err := json.Unmarshal([]byte(rawJSON), &nextWorld); err != nil {
				fmt.Printf("[%s] 世界状态 JSON 解析失败: %v\n", num, err)
				continue
			}
		}

		// 确保 meta.current_chapter 为当前章
		if meta, ok := nextWorld["meta"].(map[string]interface{}); ok {
			meta["current_chapter"] = num
		} else {
			nextWorld["meta"] = map[string]interface{}{"current_chapter": num}
		}

		if err := saveWorldState(bookDir, num, nextWorld); err != nil {
			fmt.Printf("[%s] 写入世界状态失败: %v\n", num, err)
			continue
		}
		fmt.Printf("[%s] 已写入 %s\n", num, worldPath)
	}
	fmt.Println("世界状态构建完成。")
}

// buildWorldMergePrompt 生成“上一章世界 + 本章摘要 -> 本章世界”的合并提示词
func buildWorldMergePrompt(prevWorld map[string]interface{}, chapterSummary map[string]interface{}, currentChapter string) string {
	prevJSON, _ := json.MarshalIndent(prevWorld, "", "  ")
	summaryJSON, _ := json.MarshalIndent(chapterSummary, "", "  ")
	return fmt.Sprintf(`你是一个小说世界状态合并助手。请根据以下两份 JSON 合并出「到本章为止」的世界状态。

【到上一章为止的世界状态】
%s

【本章（第 %s 章）的章节摘要】
%s

请将本章摘要中的角色、地点、时间线事件、悬念等合并进上一章的世界状态，得到「从书开头到本章结尾」的完整世界状态。要求：
- 注意不要丢失任何关键信息
- 注意不要添加任何无关信息
- 角色：若已存在则用本章信息更新，若为新角色则追加。
- 地点：同上，合并或追加。
- 时间线：在原有事件列表末尾追加本章的事件。
- 悬念：合并本章新悬念，已解决的可更新 status。
- 仅根据上述两份 JSON 合并，不要臆造或联想其他内容。
- 输出为**单个 JSON 对象**，不要任何说明或 markdown，只输出这一份 JSON。结构与世界状态一致（含 meta、characters、locations、timeline、mysteries）。meta.current_chapter 请设为 %s。
`, string(prevJSON), currentChapter, string(summaryJSON), currentChapter)
}

// valueContainsKeyword 递归判断 JSON 值中是否包含关键字（任意字符串字段包含即返回 true）
func valueContainsKeyword(v interface{}, keyword string) bool {
	if keyword == "" {
		return true
	}
	switch x := v.(type) {
	case string:
		return strings.Contains(x, keyword)
	case map[string]interface{}:
		for _, val := range x {
			if valueContainsKeyword(val, keyword) {
				return true
			}
		}
		return false
	case []interface{}:
		for _, elem := range x {
			if valueContainsKeyword(elem, keyword) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// filterAbstractByKeyword 只保留摘要中「包含关键字」的部分，保持原有结构（characters/locations/timeline/mysteries 等）
func filterAbstractByKeyword(m map[string]interface{}, keyword string) map[string]interface{} {
	out := make(map[string]interface{})
	// meta 保留以便标明章节
	if meta, ok := m["meta"].(map[string]interface{}); ok {
		out["meta"] = meta
	}
	// characters: 只保留包含关键字的角色
	if chars, ok := m["characters"].(map[string]interface{}); ok {
		filtered := make(map[string]interface{})
		for id, entry := range chars {
			if entryMap, ok := entry.(map[string]interface{}); ok && valueContainsKeyword(entryMap, keyword) {
				filtered[id] = entry
			}
		}
		out["characters"] = filtered
	}
	// locations: 只保留包含关键字的地点
	if locs, ok := m["locations"].(map[string]interface{}); ok {
		filtered := make(map[string]interface{})
		for id, entry := range locs {
			if entryMap, ok := entry.(map[string]interface{}); ok && valueContainsKeyword(entryMap, keyword) {
				filtered[id] = entry
			}
		}
		out["locations"] = filtered
	}
	// timeline: 只保留包含关键字的事件
	if timeline, ok := m["timeline"].([]interface{}); ok {
		var filtered []interface{}
		for _, ev := range timeline {
			if valueContainsKeyword(ev, keyword) {
				filtered = append(filtered, ev)
			}
		}
		out["timeline"] = filtered
	}
	// mysteries: 只保留包含关键字的悬念
	if mysteries, ok := m["mysteries"].([]interface{}); ok {
		var filtered []interface{}
		for _, item := range mysteries {
			if valueContainsKeyword(item, keyword) {
				filtered = append(filtered, item)
			}
		}
		out["mysteries"] = filtered
	}
	return out
}

// chapterNumToInt 将章节号字符串转为数字便于比较（01、034、34 等均支持）
func chapterNumToInt(s string) int {
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

// askQuestion 根据用户读到的章节与问题，按数据源（向量 / 摘要）选取背景后调用大模型作答；若同时存在两种数据源则让用户选择。debug 为 true 时打印模型入参与响应。
func askQuestion(bookDir string, chapterNum string, question string, debug bool) {
	chapterNum = normalizeChapterNum(chapterNum)
	ctx := context.Background()
	upToInt := chapterNumToInt(chapterNum)

	hasVector := hasVectorDataForBook(ctx, bookDir, upToInt)
	_, hasSummary := hasSummariesForChapters(bookDir, upToInt)

	if !hasVector && !hasSummary {
		fmt.Printf("未找到第 1 章到第 %s 章的可用于答问的数据（既无 Qdrant 向量也无摘要）。请先运行 pipeline --summary 或 pipeline --qdrant 生成其一。\n", chapterNum)
		return
	}

	useVector := false
	if hasVector && hasSummary {
		fmt.Println("检测到该书同时存在「向量检索」与「摘要」两种背景数据，请选择本次回答问题使用的来源：")
		fmt.Println("  1 = 向量检索（原文片段，更贴近原文）")
		fmt.Println("  2 = 摘要")
		fmt.Print("请输入 1 或 2: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			choice := strings.TrimSpace(scanner.Text())
			useVector = choice == "1"
		}
	} else if hasVector {
		useVector = true
	}

	reply, promptUsed, err := AskWithSource(bookDir, chapterNum, question, useVector)
	if err != nil {
		fmt.Printf("%v\n", err)
		return
	}
	if debug {
		fmt.Println("========== 提问入参（prompt）==========")
		fmt.Println(promptUsed)
		fmt.Println("========== 以上为入参，以下为模型响应 ==========")
	}
	fmt.Println(reply)
}

// hasVectorDataForBook 检测该书在 Qdrant 中是否存在 1..chapterNumMax 范围内的向量数据（用于判断是否可选「向量检索」）。
func hasVectorDataForBook(ctx context.Context, bookDir string, chapterNumMax int) bool {
	vc, err := vectorstore.NewQdrantClient(config.C.Qdrant.Host, config.C.Qdrant.Port)
	if err != nil {
		return false
	}
	defer vc.Close()
	embClient := embeddings.NewClient("", config.C.Embedding.Model, config.C.Embedding.BaseURL)
	queryVecs, err := embClient.Embed([]string{"的"})
	if err != nil || len(queryVecs) == 0 {
		return false
	}
	bookID := filepath.Base(bookDir)
	hits, err := vc.Search(ctx, config.C.Qdrant.Collection, bookID, chapterNumMax, queryVecs[0], 1)
	return err == nil && len(hits) > 0
}

// hasSummariesForChapters 返回 bookDir 下第 1 章到 upToInt 章中已有摘要的章节号列表；若至少有一章有摘要则 ok=true。
func hasSummariesForChapters(bookDir string, upToInt int) (numbers []string, ok bool) {
	allNumbers, err := listSummaryChapterNumbers(bookDir)
	if err != nil {
		return nil, false
	}
	for _, num := range allNumbers {
		if chapterNumToInt(num) <= upToInt {
			numbers = append(numbers, num)
		}
	}
	return numbers, len(numbers) > 0
}

// AskWithSource 按指定数据源（useVector=true 用向量检索，false 用摘要）获取背景并调用大模型作答。
// 返回 (回复正文, 使用的 prompt, error)。供 CLI 与 HTTP 复用。
func AskWithSource(bookDir string, chapterNum string, question string, useVector bool) (reply string, promptUsed string, err error) {
	chapterNum = normalizeChapterNum(chapterNum)
	parsed := ask.ParseQuestion(question)
	ctx := context.Background()
	upToInt := chapterNumToInt(chapterNum)

	if useVector {
		contextText, ok := getContextFromVectorSearch(ctx, bookDir, upToInt, question)
		if !ok || contextText == "" {
			return "", "", fmt.Errorf("向量检索未返回结果，请检查 Qdrant 与章节范围")
		}
		promptUsed = buildRAGPrompt(contextText, chapterNum, question, parsed)
		client := llm.NewVolcClient("", config.C.LLM.Model, config.C.LLM.BaseURL, config.C.LLM.EventLogPath)
		meta := map[string]string{"mode": "ask", "chapter": chapterNum, "context": "vector"}
		reply, err = client.Call(ctx, promptUsed, meta)
		if err != nil {
			return "", "", err
		}
		return reply, promptUsed, nil
	}

	numbers, ok := hasSummariesForChapters(bookDir, upToInt)
	if !ok {
		return "", "", fmt.Errorf("未找到第 1 章到第 %s 章的摘要", chapterNum)
	}
	var parts []string
	for _, num := range numbers {
		path := summaryJSONPath(bookDir, num)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", "", fmt.Errorf("读取 %s: %w", path, readErr)
		}
		if parsed.Keyword != "" {
			var abstract map[string]interface{}
			if jsonErr := json.Unmarshal(data, &abstract); jsonErr != nil {
				return "", "", fmt.Errorf("解析 %s: %w", path, jsonErr)
			}
			filtered := filterAbstractByKeyword(abstract, parsed.Keyword)
			hasContent := false
			if c, _ := filtered["characters"].(map[string]interface{}); len(c) > 0 {
				hasContent = true
			}
			if !hasContent {
				if loc, _ := filtered["locations"].(map[string]interface{}); len(loc) > 0 {
					hasContent = true
				}
			}
			if !hasContent {
				if tl, _ := filtered["timeline"].([]interface{}); len(tl) > 0 {
					hasContent = true
				}
			}
			if !hasContent {
				if my, _ := filtered["mysteries"].([]interface{}); len(my) > 0 {
					hasContent = true
				}
			}
			if !hasContent {
				parts = append(parts, fmt.Sprintf("【第 %s 章】摘要中无与「%s」相关的内容。", num, parsed.Keyword))
				continue
			}
			filteredJSON, _ := json.MarshalIndent(filtered, "", "  ")
			parts = append(parts, fmt.Sprintf("【第 %s 章摘要】\n%s", num, string(filteredJSON)))
		} else {
			parts = append(parts, fmt.Sprintf("【第 %s 章摘要】\n%s", num, string(data)))
		}
	}
	contextText := strings.Join(parts, "\n\n")
	promptUsed = buildAskPrompt(contextText, chapterNum, question, parsed)
	client := llm.NewVolcClient("", config.C.LLM.Model, config.C.LLM.BaseURL, config.C.LLM.EventLogPath)
	meta := map[string]string{"mode": "ask", "chapter": chapterNum}
	reply, err = client.Call(ctx, promptUsed, meta)
	if err != nil {
		return "", "", err
	}
	return reply, promptUsed, nil
}

// getContextFromVectorSearch 从 Qdrant 按 book_id + 章节上限检索与 question 相关的原文，返回拼接后的背景文本；若未配置向量或检索失败则返回 ("", false)。
func getContextFromVectorSearch(ctx context.Context, bookDir string, chapterNumMax int, question string) (string, bool) {
	vc, err := vectorstore.NewQdrantClient(config.C.Qdrant.Host, config.C.Qdrant.Port)
	if err != nil {
		return "", false
	}
	defer vc.Close()
	embClient := embeddings.NewClient("", config.C.Embedding.Model, config.C.Embedding.BaseURL)
	queryVecs, err := embClient.Embed([]string{question})
	if err != nil || len(queryVecs) == 0 {
		return "", false
	}
	bookID := filepath.Base(bookDir)
	limit := config.C.VectorSearch.DefaultLimit
	if limit == 0 {
		limit = 10
	}
	hits, err := vc.Search(ctx, config.C.Qdrant.Collection, bookID, chapterNumMax, queryVecs[0], limit)
	if err != nil || len(hits) == 0 {
		return "", false
	}
	var parts []string
	for _, h := range hits {
		parts = append(parts, fmt.Sprintf("【第 %d 章 原文片段】\n%s", h.ChapterNum, h.Text))
	}
	return strings.Join(parts, "\n\n"), true
}

// buildRAGPrompt 使用向量检索到的原文片段作为背景，构造与 buildAskPrompt 约束一致的提问模板。
func buildRAGPrompt(relatedPassages string, upToChapter string, question string, parsed ask.ParsedQuestion) string {
	return `你是一个仅根据给定原文片段回答读者问题的助手。

【重要约束】你必须**仅**依据下面提供的原文片段作答，**禁止**使用你的先验知识、训练数据中关于该书或其中人物的记忆；凡片段中未出现的信息一律不得写入回答。若仅凭片段无法回答，请明确说「根据目前提供的原文无法确定」，不要编造或联想。

以下是从第 1 章到第 ` + upToChapter + ` 章范围内，与读者问题最相关的原文片段（按相关性排序）。

` + relatedPassages + `

---

【读者问题】` + "\n" + question + `

请严格依据上述原文片段作答，不得使用片段以外的知识；若无法从片段得出答案，请回答「根据目前提供的原文无法确定」。`
}

func normalizeChapterNum(s string) string {
	s = strings.TrimSpace(s)
	// 补成至少 3 位便于与 001、002 等比较
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

func buildAskPrompt(summariesText string, upToChapter string, question string, parsed ask.ParsedQuestion) string {
	orderHint := "以下内容**严格按章节号递增顺序**排列（第 1 章、第 2 章、…、第 " + upToChapter + " 章），请按此顺序理解并作答。\n\n"
	return `你是一个仅根据给定摘要回答读者问题的助手。

【重要约束】你必须**仅**依据下面提供的各章摘要内容作答，**禁止**使用你的先验知识、训练数据中关于该书或其中人物的记忆；凡摘要中未出现的信息一律不得写入回答。若仅凭摘要无法回答，请明确说「根据目前摘要无法确定」，不要编造或联想。

下面是从第 1 章到第 ` + upToChapter + ` 章的各章摘要（JSON 格式）。

` + orderHint + `各章摘要如下：

` + summariesText + `

---

【读者问题】` + "\n" + question + `

请严格依据上述摘要作答，不得使用摘要以外的知识；若无法从摘要得出答案，请回答「根据目前摘要无法确定」。`
}
