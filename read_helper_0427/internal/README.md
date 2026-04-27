# internal 包说明

各模块通过**接口**与主流程交互，便于替换实现或扩展。

## 目录结构

- **llm/** — 大模型调用
  - `Caller`：`Call(ctx, prompt, meta) (string, error)`
  - 实现：`VolcClient`（火山引擎 Ark），见 `volc.go`
  - 可扩展：其他厂商或本地模型只需实现 `Caller` 并注入

- **splitter/** — 章节拆分（按不同文章风格）
  - `Splitter`：`Split(inputDir, filename, outputDir) error`
  - `Chapter`：单章结构（Number, Title, Kind, Content）
  - 实现：`RegexSplitter`（第X回、第X章等正则），见 `regex.go`
  - 可扩展：新格式（如“卷·章”）可新增实现并注入

- **response/** — 大模型响应的数据结构处理
  - `JSONExtractor`：`ExtractJSON(reply string) string`（从回复文本中截取 JSON）
  - 实现：`DefaultExtractor`（去 ```json、取 {} 等），见 `extract.go`
  - `RepairTruncatedJSON`：修末尾截断的摘要 JSON，见 `repair.go`
  - 可扩展：不同模型回复格式可提供新的 Extractor

- **ask/** — 提问解析（用于 ask 命令的过滤与路由）
  - `Parser`：`Parse(question string) ParsedQuestion`
  - `ParsedQuestion`：Type（如人物查询）、Keyword（过滤摘要用）
  - 实现：`SimpleParser`（如「xxx是谁」），见 `parser.go`
  - 可扩展：更多问法或类型可加实现

## 主流程（main 包）

- `world.go`：世界状态合并、ask 问答；依赖 `llm.Caller`、`ask.Parser`、`response.ExtractJSON`
- `summary_demo.go`：单章/整书摘要生成；依赖 `llm.Caller`、`response.ExtractJSON`
- `split_chapters.go`：命令行入口、拆分与 pipeline；依赖 `splitter.Splitter`
- `fix_raw.go`：修复 .raw.txt；依赖 `response.RepairTruncatedJSON`

路径与摘要文件 I/O（如 `summaryDirName`、`writeSummaryFile`）仍在 main 包，后续如需可再抽到 `internal/book`。
