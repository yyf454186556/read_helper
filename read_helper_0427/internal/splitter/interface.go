package splitter

// Splitter 按章节拆分整本书的接口。不同风格（第X回、第X章等）可提供不同实现。
type Splitter interface {
	// Split 将 inputDir/filename 按章节拆分为多个文件，写入 outputDir 下以书名命名的子目录。
	Split(inputDir, filename, outputDir string) error
}
