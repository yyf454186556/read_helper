package splitter

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RegexSplitter 基于正则的章节拆分器，支持多种标题格式（见下方正则），标题行前后可有空行。
// 第一章之前的内容（如作者序言）不会写入任何章节文件。
type RegexSplitter struct{}

// NewRegexSplitter 返回默认的正则拆分实现。
func NewRegexSplitter() *RegexSplitter {
	return &RegexSplitter{}
}

// Split 实现 Splitter 接口。
func (r *RegexSplitter) Split(inputDir, filename, outputDir string) error {
	filePath := filepath.Join(inputDir, filename)
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %v", err)
	}
	defer file.Close()

	// 原有格式：第X回 / 第X章
	patternHui := regexp.MustCompile(`^第?([一二三四五六七八九十百千万]+)回[　\s]+(.+)$`)
	patternZhang := regexp.MustCompile(`^第\s*([一二三四五六七八九十百千万]+)\s*章\s*(.*)$`)
	// 新格式：数字独占一节 + 空格 + 标题，如「一 青衫磊落险峰行」「二 玉壁月华明」（标题上下可有空行）
	patternNumTitle := regexp.MustCompile(`^([一二三四五六七八九十百千万零]+)[ 　]+(.+)$`)

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentChapter *Chapter
	var chapters []*Chapter
	var currentContent strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		var chapterNum, chapterTitle, kind string
		if matches := patternHui.FindStringSubmatch(trimmed); matches != nil {
			chapterNum, chapterTitle, kind = matches[1], matches[2], "回"
		} else if matches := patternZhang.FindStringSubmatch(trimmed); matches != nil {
			chapterNum = matches[1]
			chapterTitle = strings.TrimSpace(matches[2])
			kind = "章"
		} else if matches := patternNumTitle.FindStringSubmatch(trimmed); matches != nil {
			// 「一 青衫磊落险峰行」：整行视为标题，且必须像章节头（前后常为空行，此处仅判断格式）
			chapterNum = matches[1]
			chapterTitle = strings.TrimSpace(matches[2])
			kind = "章"
		}

		if kind != "" {
			if currentChapter != nil {
				currentChapter.Content = currentContent.String()
				chapters = append(chapters, currentChapter)
			}
			currentChapter = &Chapter{
				Number:  chapterNum,
				Title:   chapterTitle,
				Kind:    kind,
				RawLine: trimmed,
			}
			currentContent = strings.Builder{}
			currentContent.WriteString(trimmed)
			currentContent.WriteString("\n")
		} else {
			if currentChapter != nil {
				currentContent.WriteString(line)
				currentContent.WriteString("\n")
			}
		}
	}

	if currentChapter != nil {
		currentChapter.Content = currentContent.String()
		chapters = append(chapters, currentChapter)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取文件时出错: %v", err)
	}

	if len(chapters) == 0 {
		fmt.Printf("  警告: 在文件 %s 中未找到章节标记\n", filename)
		return nil
	}

	baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
	bookOutputDir := filepath.Join(outputDir, baseName)
	if err := os.MkdirAll(bookOutputDir, 0755); err != nil {
		return fmt.Errorf("创建书籍输出目录失败: %v", err)
	}

	maxChapterNum := len(chapters)
	paddingWidth := len(fmt.Sprintf("%d", maxChapterNum))

	for i, chapter := range chapters {
		arabicNum := chineseToArabic(chapter.Number)
		if arabicNum == 0 {
			arabicNum = i + 1
		}
		safeTitle := sanitizeFilename(chapter.Title)
		if safeTitle == "" {
			safeTitle = "无标题"
		}
		if chapter.Kind == "" {
			chapter.Kind = "回"
		}
		outputFilename := fmt.Sprintf("%0*d_第%s%s_%s.txt", paddingWidth, arabicNum, chapter.Number, chapter.Kind, safeTitle)
		outputPath := filepath.Join(bookOutputDir, outputFilename)
		if err := os.WriteFile(outputPath, []byte(chapter.Content), 0644); err != nil {
			return fmt.Errorf("写入章节文件失败: %v", err)
		}
		fmt.Printf("  已创建: %s (第 %d 章)\n", outputFilename, i+1)
	}

	fmt.Printf("  成功拆分 %d 个章节\n", len(chapters))
	return nil
}

func sanitizeFilename(filename string) string {
	illegalChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	result := filename
	for _, char := range illegalChars {
		result = strings.ReplaceAll(result, char, "_")
	}
	if len([]rune(result)) > 100 {
		result = string([]rune(result)[:100])
	}
	return strings.TrimSpace(result)
}

func chineseToArabic(chineseNum string) int {
	digitMap := map[rune]int{
		'一': 1, '二': 2, '三': 3, '四': 4, '五': 5,
		'六': 6, '七': 7, '八': 8, '九': 9,
	}
	if len(chineseNum) == 0 {
		return 0
	}
	runes := []rune(chineseNum)
	result, temp := 0, 0
	for i := 0; i < len(runes); i++ {
		char := runes[i]
		switch char {
		case '十':
			if temp == 0 {
				temp = 1
			}
			result += temp * 10
			temp = 0
		case '百':
			if temp == 0 {
				temp = 1
			}
			result += temp * 100
			temp = 0
		case '千':
			if temp == 0 {
				temp = 1
			}
			result += temp * 1000
			temp = 0
		case '万':
			if temp == 0 {
				temp = 1
			}
			result += temp * 10000
			temp = 0
		default:
			if val, ok := digitMap[char]; ok {
				temp = val
			}
		}
	}
	return result + temp
}
