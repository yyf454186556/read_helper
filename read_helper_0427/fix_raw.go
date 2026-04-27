package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"read_helper/internal/config"
	"read_helper/internal/response"
)

// fixRaw 从 abstracts/num.raw.txt 修复并生成 .json 与 .md
// 用法：go run . fixraw 057 091  或  go run . fixraw 057 091 --book book_chapters/yongzhengwangchao_utf8
func fixRaw(bookDir string, numbers []string) {
	abstractsDir := filepath.Join(bookDir, config.C.Dir.SummaryDirName)
	for _, num := range numbers {
		num = strings.TrimSpace(num)
		if num == "" {
			continue
		}
		rawPath := filepath.Join(abstractsDir, num+".raw.txt")
		data, err := os.ReadFile(rawPath)
		if err != nil {
			fmt.Printf("[%s] 未找到或无法读取 %s: %v\n", num, rawPath, err)
			continue
		}
		content := string(data)

		// 修复 091 型：relationships 误写为数组 "relationships": [ 改为 "relationships": {
		content = strings.Replace(content, `"relationships": [`, `"relationships": {`, -1)

		// 修复 057 型：末尾截断的 "last_updated": 补全并闭合 JSON
		content = response.RepairTruncatedJSON(content, num)

		var summary map[string]interface{}
		if err := json.Unmarshal([]byte(content), &summary); err != nil {
			fmt.Printf("[%s] 修复后仍无法解析 JSON: %v\n", num, err)
			continue
		}

		// 用“假”章节路径仅为了得到目录和章号，便于复用 writeSummaryFile / writeSummaryMarkdown..
		fakeChapterPath := filepath.Join(bookDir, num+".txt")
		jsonPath, err := writeSummaryFile(fakeChapterPath, summary)
		if err != nil {
			fmt.Printf("[%s] 写入 JSON 失败: %v\n", num, err)
			continue
		}
		mdPath, err := writeSummaryMarkdown(fakeChapterPath, summary)
		if err != nil {
			fmt.Printf("[%s] 写入 MD 失败: %v\n", num, err)
			continue
		}
		fmt.Printf("[%s] 已修复并生成 %s、%s\n", num, jsonPath, mdPath)
	}
}
