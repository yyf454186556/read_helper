package splitter

// Chapter 表示一个章节（拆分结果中的单章）
type Chapter struct {
	Number  string // 章节号（中文数字）
	Title   string // 标题
	Kind    string // "回" 或 "章"，用于输出文件名
	RawLine string // 原始标题行
	Content string // 章节正文内容
}
