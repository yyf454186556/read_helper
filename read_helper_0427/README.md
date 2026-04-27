# read_helper

小说阅读助手：按章节拆分、生成摘要、向量入库（Qdrant），并支持基于「读到的章节」提问（向量检索或摘要作为背景）。

---

## 环境要求

- **Go** 1.24+
- **大模型 / 向量**：需配置环境变量 `ARK_API_KEY`（火山引擎 Ark，用于摘要生成、提问与 embedding）。运行会用到大模型或 embedding 的子命令（如 `ask`、`serve`、`summary`、`pipeline --summary/--qdrant`、`world`、`qdrant`）在启动时会**判空**，未设置则提示并退出。
- **Qdrant**（可选）：若使用向量检索或写入向量，需本地启动 Qdrant（如 `docker run -p 6333:6333 -p 6334:6334 qdrant/qdrant:v1.17.0`）

---

## 配置文件

除 `ARK_API_KEY` 仍从环境变量读取外，其余参数均从**配置文件**加载，便于统一修改目录、端口、模型等。

- **路径**：默认使用项目根目录下的 `config.json`；可通过环境变量 `READ_HELPER_CONFIG` 指定其它路径。
- **格式**：JSON；未提供的字段使用内置默认值。项目内已附带一份 `config.json`，可直接按需修改。

| 配置块 | 说明 |
|--------|------|
| `dir` | 目录：`input_dir`（原始书）、`output_dir`（拆分输出根目录）、`summary_dir_name`、`world_dir_name`、`summary_demo_chapter_dir`（单章 demo 默认书） |
| `qdrant` | `host`、`port`、`collection`、`vector_size` |
| `embedding` | `base_url`、`model`、`batch_size`、`max_retries`、`retry_delay_seconds`、`max_chunk_runes` |
| `llm` | `base_url`、`model`、`event_log_path`；可选 `fallback_model`、`fallback_base_url`（默认模型回复无法解析为 JSON 时自动用备选模型重试一次，优先用默认模型以节省费用） |
| `serve` | `default_port`（HTTP 服务默认端口） |
| `summary` | `concurrency`（整书摘要并发数） |
| `vector_search` | `default_limit`（RAG 检索条数） |

---

## 目录约定

| 目录 / 路径 | 说明 |
|------------|------|
| `book_resource/` | 原始整书 `.txt` 放置处 |
| `book_chapters/<书名>/` | 拆分后的章节文件，如 `001_第一章_xxx.txt` |
| `book_chapters/<书名>/abstracts/` | 每章摘要：`001.json`、`001.md`；失败时会有 `001.raw.txt` |
| `book_chapters/<书名>/world/` | 世界状态快照（由 `world` 命令生成） |

---

## 命令一览

### 一、完整链路（一本书从零到可提问）

```bash
# 拆分 + 摘要 + 向量写入（按需加参数）
go run . pipeline --summary --qdrant book_resource/天龙八部.txt

# 若书名含中文，请用实际文件名，例如：
go run . pipeline --summary --qdrant book_resource/tianlong8_utf8.txt
```

- 不加 `--summary`：只拆分，不生成摘要  
- 不加 `--qdrant`：不写入 Qdrant  
- 两者都不加：**仅拆分**

---

### 二、只做其中一步

| 目的 | 命令 | 说明 |
|------|------|------|
| **只拆分** | `go run . pipeline book_resource/xxx.txt` | 从整书拆到 `book_chapters/xxx/` |
| **只生成摘要** | `go run . summary all book_chapters/xxx` | 对已拆分好的书生成 `abstracts/*.json`、`*.md` |
| **只写向量** | 先拆分，再 `go run . pipeline --qdrant book_resource/xxx.txt`；或先 `pipeline` 再单独写向量需自己按章节跑 | 按章节顺序 embedding 并写入 Qdrant（需先有章节目录） |
| **摘要只跑第一章** | `go run . summary` | 使用 config 中 `summary_demo_chapter_dir` 对应书目录，只处理第一章 |

---

### 三、提问（CLI）

```bash
go run . ask <书名> <读到的章号> <问题>
```

- **书名**：`book_chapters` 下目录名或前缀，如 `tianlong8_utf8`、`bailuyuan`
- **读到的章号**：当前读到的章节，如 `10`、`001`
- **问题**：任意自然语言问题

示例：

```bash
go run . ask tianlong8_utf8 10 段誉是在哪里遇到木婉清的？
go run . ask bailuyuan 140 狂徒是谁
```

- 若该书**同时有**向量和摘要，会提示选择用「向量检索」还是「摘要」作为背景；若只有一种则自动用该数据源。  
- **调试**：加 `--debug` 可打印本次调用模型的入参和响应：

```bash
go run . ask --debug tianlong8_utf8 10 段誉在哪里
```

---

### 四、提问（HTTP 接口）

先启动服务：

```bash
go run . serve        # 默认 8080
go run . serve 9000   # 指定端口
```

**POST /ask**，JSON 体：

| 字段 | 必填 | 说明 |
|------|------|------|
| `book` | 是 | 小说名（同 CLI 书名） |
| `chapter` | 是 | 当前读到的章节号 |
| `question` | 是 | 用户问题 |
| `source` | 否 | 当同时有向量和摘要时：`vector`（默认）或 `summary` |

示例：

```bash
curl -X POST http://localhost:8080/ask \
  -H "Content-Type: application/json" \
  -d '{"book":"tianlong8_utf8","chapter":"10","question":"段誉是在哪里遇到木婉清的？"}'
```

成功返回 `{"reply":"..."}`，失败返回 `{"error":"..."}` 及对应 HTTP 状态码。

---

### 五、世界状态（前缀和）

根据「第 1 章～第 N 章」的摘要，生成累积世界状态到 `world/`：

```bash
go run . world                          # 默认书目录
go run . world book_chapters/xxx        # 指定书目录
```

---

### 六、摘要失败后的补救

| 场景 | 命令 | 说明 |
|------|------|------|
| **从 .raw.txt 解析出 JSON 并生成 .json/.md** | `go run . raw2json [书目录]` | 对 `abstracts/*.raw.txt` 做 JSON 提取；成功则生成同名 `.json`、`.md` 并**删除**该 `.raw.txt`。不写书目录时用代码内默认目录 |
| **按章节号修复指定 .raw** | `go run . fixraw 02 10` 或 `go run . fixraw book_chapters/xxx 02 10` | 针对 `abstracts/02.raw.txt`、`abstracts/10.raw.txt` 做修复并写出 `.json`、`.md`（不删 raw，逻辑与 raw2json 不同，见代码） |

---

### 七、Qdrant 与向量

| 目的 | 命令 | 说明 |
|------|------|------|
| **检查 Qdrant 是否正常** | `go run . qdrant` | 连接默认 `localhost:6334`，创建集合并做一次写入+检索 |

向量维度与当前 embedding 模型一致（默认 2048）。若更换模型，需在 `internal/vectorstore/qdrant.go` 中调整 `DefaultVectorSize`，或删除已有集合让其按新维度重建。

---

## 命令速查表

| 命令 | 用途 |
|------|------|
| `go run . pipeline [--summary] [--qdrant] <书的txt路径>` | 拆分；可选摘要；可选写 Qdrant |
| `go run . ask [--debug] <书名> <章号> <问题>` | 根据读到的章节提问（向量或摘要） |
| `go run . serve [端口]` | 启动 HTTP 服务，提供 POST /ask |
| `go run . summary` | 对默认书只做第一章摘要 |
| `go run . summary all [书目录]` | 对指定书做全书摘要 |
| `go run . world [书目录]` | 生成世界状态（前缀和） |
| `go run . raw2json [书目录]` | 从 abstracts/*.raw.txt 解析并生成 .json/.md，成功后删 raw |
| `go run . fixraw [书目录] 章号 章号 ...` | 按章号修复指定 .raw.txt |
| `go run . qdrant` | 验证 Qdrant 连接与写入/检索 |
| 无子命令 | `go run .` | 仅拆分：把 `book_resource/` 下所有 txt 拆到 `book_chapters/` |

---

## 常见流程

1. **新书从零到可提问（摘要 + 向量都要）**  
   `go run . pipeline --summary --qdrant book_resource/xxx.txt`  
   然后：`go run . ask xxx 10 你的问题` 或调用 `POST /ask`。

2. **只想要摘要、不要向量**  
   `go run . pipeline --summary book_resource/xxx.txt`  
   提问时会自动用摘要作为背景（若未写 Qdrant）。

3. **摘要有一批 .raw.txt 想抢救**  
   `go run . raw2json book_chapters/xxx`  
   会尝试从每个 `.raw.txt` 提取 JSON，成功则生成 `.json`/`.md` 并删除该 raw。

4. **用 HTTP 给前端/其他服务用**  
   `go run . serve 8080`  
   然后对 `http://localhost:8080/ask` 发 POST，body 带 `book`、`chapter`、`question`（及可选 `source`）。
