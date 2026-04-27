package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// askRequest 提问接口入参：小说名、当前章节、问题均为必填。
type askRequest struct {
	Book    string `json:"book"`    // 小说名（book_chapters 下目录名或前缀，如 tianlong8_utf8）
	Chapter string `json:"chapter"` // 当前读到的章节号（如 10、001）
	Question string `json:"question"` // 用户问题
	Source   string `json:"source"`   // 可选：vector | summary，当同时存在两种数据源时指定用哪种，不传则默认 vector
}

// askResponse 提问接口响应。
type askResponse struct {
	Reply string `json:"reply,omitempty"`
	Error string `json:"error,omitempty"`
}

func runServe(port int) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ask", handleAsk)
	addr := fmt.Sprintf(":%d", port)
	log.Printf("HTTP 服务监听 %s，提问接口: POST %s/ask\n", addr, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("启动服务失败: %v", err)
	}
}

func handleAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, askResponse{Error: "请使用 POST"})
		return
	}
	var req askRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, askResponse{Error: "请求体不是合法 JSON: " + err.Error()})
		return
	}
	req.Book = strings.TrimSpace(req.Book)
	req.Chapter = strings.TrimSpace(req.Chapter)
	req.Question = strings.TrimSpace(req.Question)
	if req.Book == "" || req.Chapter == "" || req.Question == "" {
		writeJSON(w, http.StatusBadRequest, askResponse{Error: "缺少必填字段：book、chapter、question 均不能为空"})
		return
	}

	bookDir, err := resolveBookDir(req.Book)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, askResponse{Error: err.Error()})
		return
	}

	ctx := context.Background()
	chapterNum := normalizeChapterNum(req.Chapter)
	upToInt := chapterNumToInt(chapterNum)
	hasVector := hasVectorDataForBook(ctx, bookDir, upToInt)
	_, hasSummary := hasSummariesForChapters(bookDir, upToInt)

	if !hasVector && !hasSummary {
		writeJSON(w, http.StatusBadRequest, askResponse{Error: fmt.Sprintf("未找到第 1 章到第 %s 章的可用于答问的数据（既无 Qdrant 向量也无摘要）", req.Chapter)})
		return
	}

	useVector := false
	if hasVector && hasSummary {
		// 两种都有时按 source 选择；未传则默认 vector
		src := strings.ToLower(strings.TrimSpace(req.Source))
		useVector = src != "summary" && src != "摘要"
	} else if hasVector {
		useVector = true
	}

	reply, _, err := AskWithSource(bookDir, chapterNum, req.Question, useVector)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, askResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, askResponse{Reply: reply})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
