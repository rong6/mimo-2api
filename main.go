package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	listenAddr string
	apiKey     string
	saveConv   bool
	mimoBase   = "https://aistudio.xiaomimimo.com"
	httpClient *http.Client
	cookies    []string // 多个 cookie，轮询/随机选取
)

// ===================== OpenAI Types =====================

type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Stream      bool          `json:"stream,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
}

type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ChatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
}

type ChatChoice struct {
	Index        int          `json:"index"`
	Message      *ChatMessage `json:"message,omitempty"`
	Delta        *ChatDelta   `json:"delta,omitempty"`
	FinishReason *string      `json:"finish_reason,omitempty"`
}

type ChatDelta struct {
	Role             *string `json:"role,omitempty"`
	Content          *string `json:"content,omitempty"`
	ReasoningContent *string `json:"reasoning_content,omitempty"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// ===================== MiMo Types =====================

type MiMoRequest struct {
	MsgID          string        `json:"msgId"`
	ConversationID string        `json:"conversationId"`
	Query          string        `json:"query"`
	IsEditedQuery  bool          `json:"isEditedQuery"`
	ModelConfig    MiMoModelCfg  `json:"modelConfig"`
	MultiMedias    []interface{} `json:"multiMedias"`
}

type MiMoModelCfg struct {
	EnableThinking  bool    `json:"enableThinking"`
	WebSearchStatus string  `json:"webSearchStatus"`
	Model           string  `json:"model"`
	Temperature     float64 `json:"temperature"`
	TopP            float64 `json:"topP"`
}

var models = []Model{
	{ID: "mimo-v2-flash", Object: "model", Created: 1767239114, OwnedBy: "xiaomi"},
	{ID: "mimo-v2-pro", Object: "model", Created: 1767239114, OwnedBy: "xiaomi"},
	{ID: "mimo-v2-omni", Object: "model", Created: 1767239114, OwnedBy: "xiaomi"},
}

func resolveModel(name string) string {
	m := map[string]string{
		"mimo-v2-flash-studio": "mimo-v2-flash-studio",
		"mimo-v2-flash":        "mimo-v2-flash-studio",
		"mimo-v2-pro":          "mimo-v2-pro",
		"mimo-v2-omni":         "mimo-v2-omni",
	}
	if v, ok := m[name]; ok {
		return v
	}
	return "mimo-v2-pro"
}

// ===================== Helpers =====================

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// 随机选一个 cookie
func pickCookie() string {
	if len(cookies) == 1 {
		return cookies[0]
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(cookies))))
	return cookies[n.Int64()]
}

func messagesToQuery(msgs []ChatMessage) string {
	var parts []string
	for _, m := range msgs {
		text := extractText(m.Content)
		switch m.Role {
		case "system":
			parts = append(parts, "System: "+text)
		case "user":
			parts = append(parts, "Human: "+text)
		case "assistant":
			parts = append(parts, "Assistant: "+text)
		default:
			parts = append(parts, m.Role+": "+text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var out []string
		for _, p := range v {
			if m, ok := p.(map[string]interface{}); ok {
				if m["type"] == "text" {
					out = append(out, fmt.Sprint(m["text"]))
				}
			}
		}
		return strings.Join(out, "\n")
	default:
		return fmt.Sprint(v)
	}
}

func extractPh(cookieStr string) string {
	for _, part := range strings.Split(cookieStr, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "xiaomichatbot_ph=") {
			return strings.Trim(strings.TrimPrefix(part, "xiaomichatbot_ph="), "\"")
		}
	}
	return ""
}

// ===================== Handlers =====================

func handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ModelList{Object: "list", Data: models})
}

func checkAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if apiKey != "" {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "Bearer "+apiKey {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":{"message":"Unauthorized"}}`))
				return
			}
		}
		next(w, r)
	}
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid json"}}`, 400)
		return
	}

	query := messagesToQuery(req.Messages)
	mimoModel := resolveModel(req.Model)

	temp := 0.8
	if req.Temperature != nil {
		temp = *req.Temperature
	}
	topP := 0.95
	if req.TopP != nil {
		topP = *req.TopP
	}

	chatID := "chatcmpl-" + randHex(16)
	convID := randHex(16)

	mimoReq := MiMoRequest{
		MsgID:          randHex(16),
		ConversationID: convID,
		Query:          query,
		IsEditedQuery:  false,
		ModelConfig: MiMoModelCfg{
			EnableThinking:  true,
			WebSearchStatus: "disabled",
			Model:           mimoModel,
			Temperature:     temp,
			TopP:            topP,
		},
		MultiMedias: []interface{}{},
	}

	cookie := pickCookie()
	if saveConv {
		go saveConversation(context.Background(), cookie, convID, getFirstMsg(req.Messages))
	}

	if req.Stream {
		streamChat(w, chatID, req.Model, mimoReq, cookie)
	} else {
		syncChat(w, chatID, req.Model, mimoReq, cookie)
	}
}

func getFirstMsg(msgs []ChatMessage) string {
	for _, m := range msgs {
		if m.Role == "user" {
			text := extractText(m.Content)
			if len(text) > 30 {
				return text[:27] + "..."
			}
			if text != "" {
				return text
			}
		}
	}
	return "New conversation"
}

func saveConversation(ctx context.Context, cookie string, convID string, title string) {
	ph := extractPh(cookie)
	apiURL := mimoBase + "/open-apis/chat/conversation/save?xiaomichatbot_ph=" + url.QueryEscape(ph)
	body, _ := json.Marshal(map[string]string{
		"conversationId": convID,
		"title":          title,
		"type":           "chat",
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	resp, err := httpClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// ---------- call MiMo ----------
func callMiMo(ctx context.Context, mimoReq MiMoRequest, cookie string) (*http.Response, error) {
	body, _ := json.Marshal(mimoReq)

	ph := extractPh(cookie)
	apiURL := mimoBase + "/open-apis/bot/chat?xiaomichatbot_ph=" + url.QueryEscape(ph)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "system")
	req.Header.Set("x-timeZone", "Asia/Shanghai")
	req.Header.Set("Cookie", cookie)

	return httpClient.Do(req)
}

// ---------- sync (non-stream) ----------
func syncChat(w http.ResponseWriter, chatID, model string, mimoReq MiMoRequest, cookie string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	resp, err := callMiMo(ctx, mimoReq, cookie)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, err.Error()), 502)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf(`{"error":{"message":"upstream %d: %s"}}`, resp.StatusCode, string(b)), 502)
		return
	}

	var content strings.Builder
	var inThink bool
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	curEvent := ""

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event:") {
			curEvent = strings.TrimSpace(line[6:])
			continue
		}
		if !strings.HasPrefix(line, "data:") || curEvent != "message" {
			continue
		}
		raw := strings.TrimSpace(line[5:])
		var d struct {
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(raw), &d) != nil || d.Content == "" {
			continue
		}
		chunk := d.Content
		for len(chunk) > 0 {
			if !inThink {
				if i := strings.Index(chunk, "<think>"); i >= 0 {
					content.WriteString(chunk[:i])
					chunk = chunk[i+8:]
					inThink = true
				} else {
					content.WriteString(chunk)
					break
				}
			} else {
				if i := strings.Index(chunk, "</think>"); i >= 0 {
					chunk = chunk[i+9:]
					inThink = false
				} else {
					break
				}
			}
		}
	}

	fr := "stop"
	json.NewEncoder(w).Encode(ChatCompletionResponse{
		ID: chatID, Object: "chat.completion", Created: time.Now().Unix(), Model: model,
		Choices: []ChatChoice{{Index: 0, Message: &ChatMessage{Role: "assistant", Content: content.String()}, FinishReason: &fr}},
	})
}

// ---------- stream (SSE) ----------
func streamChat(w http.ResponseWriter, chatID, model string, mimoReq MiMoRequest, cookie string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	resp, err := callMiMo(ctx, mimoReq, cookie)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, err.Error()), 502)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf(`{"error":{"message":"upstream %d: %s"}}`, resp.StatusCode, string(b)), 502)
		return
	}

	created := time.Now().Unix()
	role := "assistant"
	sendDelta(w, flusher, chatID, model, created, &ChatDelta{Role: &role}, nil)

	inThink := false
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	curEvent := ""

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event:") {
			curEvent = strings.TrimSpace(line[6:])
			continue
		}
		if !strings.HasPrefix(line, "data:") || curEvent != "message" {
			continue
		}
		raw := strings.TrimSpace(line[5:])
		var d struct {
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(raw), &d) != nil || d.Content == "" {
			continue
		}

		chunk := d.Content
		for len(chunk) > 0 {
			if !inThink {
				if i := strings.Index(chunk, "<think>"); i >= 0 {
					before := chunk[:i]
					if before != "" {
						sendDelta(w, flusher, chatID, model, created, &ChatDelta{Content: &before}, nil)
					}
					chunk = chunk[i+8:]
					inThink = true
				} else {
					if chunk != "" {
						sendDelta(w, flusher, chatID, model, created, &ChatDelta{Content: &chunk}, nil)
					}
					break
				}
			} else {
				if i := strings.Index(chunk, "</think>"); i >= 0 {
					thinkChunk := chunk[:i]
					if thinkChunk != "" {
						sendDelta(w, flusher, chatID, model, created, &ChatDelta{ReasoningContent: &thinkChunk}, nil)
					}
					chunk = chunk[i+9:]
					inThink = false
				} else {
					if chunk != "" {
						sendDelta(w, flusher, chatID, model, created, &ChatDelta{ReasoningContent: &chunk}, nil)
					}
					break
				}
			}
		}
	}

	fr := "stop"
	sendDelta(w, flusher, chatID, model, created, &ChatDelta{}, &fr)
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func sendDelta(w http.ResponseWriter, f http.Flusher, id, model string, ts int64, delta *ChatDelta, finish *string) {
	resp := ChatCompletionResponse{
		ID: id, Object: "chat.completion.chunk", Created: ts, Model: model,
		Choices: []ChatChoice{{Index: 0, Delta: delta, FinishReason: finish}},
	}
	b, _ := json.Marshal(resp)
	fmt.Fprintf(w, "data: %s\n\n", b)
	f.Flush()
}

// ===================== Main =====================

func main() {
	// 支持 -cookie, -cookie2, -cookie3, ... 的 flag
	var cookie1, cookie2, cookie3, cookie4, cookie5 string
	flag.StringVar(&listenAddr, "listen", ":8090", "listen address")
	flag.StringVar(&apiKey, "apikey", "", "API Key (optional)")
	flag.BoolVar(&saveConv, "save", false, "Save conversation to web history (optional)")
	flag.StringVar(&cookie1, "cookie", "", "完整 Cookie 字符串 (必需)")
	flag.StringVar(&cookie2, "cookie2", "", "第2个 Cookie (可选)")
	flag.StringVar(&cookie3, "cookie3", "", "第3个 Cookie (可选)")
	flag.StringVar(&cookie4, "cookie4", "", "第4个 Cookie (可选)")
	flag.StringVar(&cookie5, "cookie5", "", "第5个 Cookie (可选)")
	flag.Parse()

	for _, c := range []string{cookie1, cookie2, cookie3, cookie4, cookie5} {
		if c != "" {
			cookies = append(cookies, c)
		}
	}

	if len(cookies) == 0 {
		log.Fatal("ERROR: 至少需要一个 -cookie 参数\n" +
			"从浏览器 DevTools → Network → 任意请求 → Headers → Cookie 复制完整值\n" +
			"例: -cookie 'serviceToken=xxx; userId=6861418446; xiaomichatbot_ph=xxx'\n" +
			"多账号负载均衡: -cookie '...' -cookie2 '...' -cookie3 '...'")
	}

	httpClient = &http.Client{Timeout: 10 * time.Minute}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", checkAuth(handleModels))
	mux.HandleFunc("/v1/chat/completions", checkAuth(handleChat))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"service":   "MiMo 2API",
			"endpoints": []string{"/v1/chat/completions", "/v1/models"},
			"accounts":  len(cookies),
		})
	})

	log.Printf("🚀 MiMo 2API → %s", listenAddr)
	log.Printf("   账号数: %d（每次请求随机选取）", len(cookies))
	log.Printf("   GET /health  |  POST /v1/chat/completions  |  GET /v1/models")

	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatal(err)
	}
}
