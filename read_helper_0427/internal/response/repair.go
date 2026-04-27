package response

import (
	"fmt"
	"strings"
)

// RepairTruncatedJSON 若内容以 "last_updated": 结尾（无值、未闭合），则补全为合法 JSON。
// defaultChapter 用于 last_updated 的值（如 "057" -> 57）。
func RepairTruncatedJSON(content string, defaultChapter string) string {
	trimmed := strings.TrimRight(content, " \t\n\r")
	if !strings.HasSuffix(trimmed, `"last_updated":`) {
		return content
	}
	chapterNum := strings.TrimLeft(defaultChapter, "0")
	if chapterNum == "" {
		chapterNum = "1"
	}
	suffix := fmt.Sprintf(" %s\n    }\n  }\n  , \"locations\": {}, \"timeline\": [], \"mysteries\": []\n}", chapterNum)
	return trimmed + suffix
}
