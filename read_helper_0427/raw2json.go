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

// runRaw2JSON 从指定书目录下的 abstracts/*.raw.txt 解析 JSON，生成对应 .json 与 .md，成功则删除 .raw.txt。
// 与主线摘要逻辑隔离，仅做「raw 文件 → 提取 JSON → 写入摘要文件 → 删 raw」。
// 用法：go run . raw2json [书目录]，书目录默认为 summaryDemoChapterDir。
func runRaw2JSON(bookDir string) {
	abstractsDir := filepath.Join(bookDir, config.C.Dir.SummaryDirName)
	entries, err := os.ReadDir(abstractsDir)
	if err != nil {
		fmt.Printf("读取目录失败: %v\n", err)
		return
	}

	var processed, failed int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".raw.txt") {
			continue
		}
		num := strings.TrimSuffix(name, ".raw.txt")
		rawPath := filepath.Join(abstractsDir, name)
		data, err := os.ReadFile(rawPath)
		if err != nil {
			fmt.Printf("[%s] 读取失败: %v\n", num, err)
			failed++
			continue
		}
		content := string(data)
		rawJSON := response.ExtractJSON(content)
		if rawJSON == "" {
			fmt.Printf("[%s] 无法从内容中提取 JSON，跳过\n", num)
			failed++
			continue
		}
		var summary map[string]interface{}
		if err := json.Unmarshal([]byte(rawJSON), &summary); err != nil {
			fmt.Printf("[%s] JSON 解析失败: %v，跳过\n", num, err)
			failed++
			continue
		}
		fakeChapterPath := filepath.Join(bookDir, num+".txt")
		if _, err := writeSummaryFile(fakeChapterPath, summary); err != nil {
			fmt.Printf("[%s] 写入 .json 失败: %v\n", num, err)
			failed++
			continue
		}
		if _, err := writeSummaryMarkdown(fakeChapterPath, summary); err != nil {
			fmt.Printf("[%s] 写入 .md 失败: %v\n", num, err)
			failed++
			continue
		}
		if err := os.Remove(rawPath); err != nil {
			fmt.Printf("[%s] 已生成 .json/.md，但删除 .raw.txt 失败: %v\n", num, err)
		}
		fmt.Printf("[%s] 已从 .raw.txt 生成 .json 与 .md，并删除原 .raw.txt\n", num)
		processed++
	}

	fmt.Printf("raw2json 完成: 成功 %d，失败或跳过 %d\n", processed, failed)
}
