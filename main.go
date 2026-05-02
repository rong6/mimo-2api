package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
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
	debugMode  bool
	mimoBase   = "https://aistudio.xiaomimimo.com"
	httpClient *http.Client
	cookies    []string // 多个 cookie，轮询随机选取
)

// ===================== Logger =====================

func logInfo(format string, a ...interface{}) {
	log.Printf("\033[1;36m[INFO]\033[0m "+format, a...)
}

func logSuccess(format string, a ...interface{}) {
	log.Printf("\033[1;32m[SUCCESS]\033[0m "+format, a...)
}

func logWarn(format string, a ...interface{}) {
	log.Printf("\033[1;33m[WARN]\033[0m "+format, a...)
}

func logError(format string, a ...interface{}) {
	log.Printf("\033[1;31m[ERROR]\033[0m "+format, a...)
}

func logDebug(format string, a ...interface{}) {
	if debugMode {
		log.Printf("\033[1;35m[DEBUG]\033[0m "+format, a...)
	}
}

func logReq(method, path, ip string) {
	log.Printf("\033[1;34m[REQ]\033[0m \033[1m%s\033[0m %s (From: %s)", method, path, ip)
}

func logRes(method, path, ip string, duration time.Duration) {
	log.Printf("\033[1;36m[RES]\033[0m \033[1m%s\033[0m %s (From: %s) \033[1;33m[%v]\033[0m", method, path, ip, duration)
}

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
	{ID: "mimo-v2.5-pro", Object: "model", Created: 1767239114, OwnedBy: "xiaomi"},
	{ID: "mimo-v2.5", Object: "model", Created: 1767239114, OwnedBy: "xiaomi"},
	{ID: "mimo-v2-flash", Object: "model", Created: 1767239114, OwnedBy: "xiaomi"},
	{ID: "mimo-v2-pro", Object: "model", Created: 1767239114, OwnedBy: "xiaomi"},
	{ID: "mimo-v2-omni", Object: "model", Created: 1767239114, OwnedBy: "xiaomi"},
}

func resolveModel(name string) string {
	m := map[string]string{
		"mimo-v2.5-pro":        "mimo-v2.5-pro",
		"mimo-v2.5":            "mimo-v2.5",
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

// 随机选一个cookie
func pickCookie() string {
	if len(cookies) == 1 {
		return cookies[0]
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(cookies))))
	return cookies[n.Int64()]
}

func messagesToQuery(ctx context.Context, cookie string, msgs []ChatMessage, modelName string) (string, []interface{}, error) {
	var parts []string
	var medias []interface{}
	for _, m := range msgs {
		text, ms, err := extractContent(ctx, cookie, m.Content, modelName)
		if err != nil {
			return "", nil, err
		}
		if len(ms) > 0 {
			medias = append(medias, ms...)
		}
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
	return strings.Join(parts, "\n"), medias, nil
}

func getMimeExt(mime string) string {
	mapping := map[string]string{
		"image/jpeg":      ".jpg",
		"image/png":       ".png",
		"image/gif":       ".gif",
		"image/webp":      ".webp",
		"audio/mpeg":      ".mp3",
		"audio/wav":       ".wav",
		"audio/webm":      ".weba",
		"video/mp4":       ".mp4",
		"application/pdf": ".pdf",
		"text/plain":      ".txt",
	}
	if ext, ok := mapping[mime]; ok {
		return ext
	}
	return ".bin"
}

func uploadMedia(ctx context.Context, cookie string, rawUrl string, mediaType string, modelName string) (interface{}, error) {
	var data []byte
	var ext string = ".bin"

	if strings.HasPrefix(rawUrl, "data:") {
		parts := strings.SplitN(rawUrl, ",", 2)
		if len(parts) == 2 {
			mimeInfo := strings.TrimPrefix(parts[0], "data:")
			idx := strings.Index(mimeInfo, ";")
			if idx > 0 {
				ext = getMimeExt(mimeInfo[:idx])
			}
			b, err := base64.StdEncoding.DecodeString(parts[1])
			if err != nil {
				return nil, err
			}
			data = b
		}
	} else if strings.HasPrefix(rawUrl, "http://") || strings.HasPrefix(rawUrl, "https://") {
		req, _ := http.NewRequestWithContext(ctx, "GET", rawUrl, nil)
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		data = b
		mimeType := resp.Header.Get("Content-Type")
		if mimeType != "" {
			ext = getMimeExt(strings.Split(mimeType, ";")[0])
		}
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("empty media data")
	}

	hash := md5.Sum(data)
	md5Str := hex.EncodeToString(hash[:])

	// FDS requires base64 encoded MD5 for Content-MD5 header
	// md5Base64 := base64.StdEncoding.EncodeToString(hash[:])

	fileName := randHex(8) + ext

	ph := extractPh(cookie)
	apiURL := mimoBase + "/open-apis/resource/genUploadInfo?xiaomichatbot_ph=" + url.QueryEscape(ph)

	reqBody, _ := json.Marshal(map[string]string{
		"fileName":       fileName,
		"fileContentMd5": md5Str,
	})

	ulReq, _ := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(reqBody))
	ulReq.Header.Set("Content-Type", "application/json")
	ulReq.Header.Set("Accept-Language", "system")
	ulReq.Header.Set("x-timeZone", "Asia/Shanghai")
	ulReq.Header.Set("Cookie", cookie)

	ulResp, err := httpClient.Do(ulReq)
	if err != nil {
		return nil, err
	}
	defer ulResp.Body.Close()

	if ulResp.StatusCode != 200 {
		b, _ := io.ReadAll(ulResp.Body)
		return nil, fmt.Errorf("genUploadInfo failed with status %d: %s", ulResp.StatusCode, string(b))
	}

	var ulData struct {
		Code int `json:"code"`
		Data struct {
			ResourceId  string `json:"resourceId"`
			ResourceUrl string `json:"resourceUrl"`
			UploadUrl   string `json:"uploadUrl"`
			ObjectName  string `json:"objectName"`
		} `json:"data"`
	}
	if err := json.NewDecoder(ulResp.Body).Decode(&ulData); err != nil {
		return nil, err
	}
	if ulData.Code != 0 || ulData.Data.UploadUrl == "" {
		return nil, fmt.Errorf("genUploadInfo failed, code: %d", ulData.Code)
	}

	putReq, _ := http.NewRequestWithContext(ctx, "PUT", ulData.Data.UploadUrl, bytes.NewReader(data))
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putReq.Header.Set("Content-MD5", md5Str)

	putResp, err := httpClient.Do(putReq)
	if err != nil {
		return nil, err
	}
	defer putResp.Body.Close()

	putRespBody, _ := io.ReadAll(putResp.Body)
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		return nil, fmt.Errorf("FDS PUT Error %d: %s", putResp.StatusCode, string(putRespBody))
	}

	// 关键节点：上传完成后请求 parse 接口
	parseURL := mimoBase + "/open-apis/resource/parse?fileUrl=" + url.QueryEscape(ulData.Data.ResourceUrl) + "&objectName=" + url.QueryEscape(ulData.Data.ObjectName) + "&model=" + url.QueryEscape(modelName) + "&xiaomichatbot_ph=" + url.QueryEscape(ph)
	parseReq, _ := http.NewRequestWithContext(ctx, "POST", parseURL, strings.NewReader(`{}`))
	parseReq.Header.Set("Content-Type", "application/json")
	parseReq.Header.Set("Cookie", cookie)

	finalID := ulData.Data.ResourceId
	tokenUsage := 0

	parseResp, err := httpClient.Do(parseReq)
	if err == nil {
		defer parseResp.Body.Close()
		pb, _ := io.ReadAll(parseResp.Body)

		var parseData struct {
			Code int `json:"code"`
			Data struct {
				Id         string `json:"id"`
				TokenUsage int    `json:"tokenUsage"`
			} `json:"data"`
		}
		if json.Unmarshal(pb, &parseData) == nil {
			if parseData.Data.Id != "" {
				finalID = parseData.Data.Id
			}
			tokenUsage = parseData.Data.TokenUsage
		}
	}

	mediaItem := map[string]interface{}{
		"mediaType":          mediaType,
		"fileUrl":            ulData.Data.ResourceUrl,
		"compressedVideoUrl": "",
		"audioTrackUrl":      "",
		"name":               fileName,
		"size":               len(data),
		"status":             "completed",
		"objectName":         ulData.Data.ObjectName,
		"url":                finalID,
		"tokenUsage":         tokenUsage,
	}
	return mediaItem, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func extractContent(ctx context.Context, cookie string, content interface{}, modelName string) (string, []interface{}, error) {
	switch v := content.(type) {
	case string:
		return v, nil, nil
	case []interface{}:
		var out []string
		var medias []interface{}
		for _, p := range v {
			if m, ok := p.(map[string]interface{}); ok {
				switch m["type"] {
				case "text":
					out = append(out, fmt.Sprint(m["text"]))
				case "image_url", "file_url", "video_url", "audio_url":
					typeRef := m["type"].(string)
					mediaType := "image"
					if typeRef != "image_url" {
						mediaType = strings.TrimSuffix(typeRef, "_url")
					}
					var urlStr string
					if obj, _ := m[typeRef].(map[string]interface{}); obj != nil {
						urlStr, _ = obj["url"].(string)
					}
					if urlStr != "" {
						mediaItem, err := uploadMedia(ctx, cookie, urlStr, mediaType, modelName)
						if err == nil && mediaItem != nil {
							medias = append(medias, mediaItem)
						}
					}
				}
			}
		}
		return strings.Join(out, "\n"), medias, nil
	default:
		return fmt.Sprint(v), nil, nil
	}
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
		start := time.Now()
		logReq(r.Method, r.URL.Path, r.RemoteAddr)
		defer func() {
			logRes(r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
		}()

		if apiKey != "" {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "Bearer "+apiKey {
				logDebug("Auth Failed. Expected apiKey matched, Got header: %s", authHeader)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":{"message":"Unauthorized"}}`))
				return
			}
		}
		logDebug("Auth Passed or apiKey not set")
		next(w, r)
	}
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	var req ChatCompletionRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, `{"error":{"message":"invalid json"}}`, 400)
		return
	}

	cookie := pickCookie()
	ctx := r.Context()

	logDebug("Selected Cookie: %s", cookie)

	mimoModel := resolveModel(req.Model)
	logDebug("Req Model: %s -> MiMo Model: %s", req.Model, mimoModel)

	query, medias, err := messagesToQuery(ctx, cookie, req.Messages, mimoModel)
	if err != nil {
		logError("messagesToQuery Error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, err.Error()), 500)
		return
	}
	logDebug("Generated Query Length: %d, Medias Count: %d", len(query), len(medias))
	if medias == nil {
		medias = []interface{}{}
	}

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
			EnableThinking:  mimoModel != "mimo-v2-omni",
			WebSearchStatus: "disabled",
			Model:           mimoModel,
			Temperature:     temp,
			TopP:            topP,
		},
		MultiMedias: medias,
	}

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

	logDebug("callMiMo URL: %s", apiURL)
	logDebug("callMiMo Body: %s", string(body))

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "system")
	req.Header.Set("x-timeZone", "Asia/Shanghai")
	req.Header.Set("Cookie", cookie)

	resp, err := httpClient.Do(req)
	if err != nil {
		logError("callMiMo Error: %v", err)
	} else {
		logDebug("callMiMo Status: %d", resp.StatusCode)
	}
	return resp, err
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
	firstLine := true

	for scanner.Scan() {
		line := scanner.Text()

		// 检查是不是后端返回的非流式报错（例如 {"code":...})
		if firstLine && strings.HasPrefix(line, "{") {
			sendDelta(w, flusher, chatID, model, created, &ChatDelta{Content: &line}, nil)
			firstLine = false
			continue
		}
		firstLine = false

		if strings.HasPrefix(line, "event:") {
			curEvent = strings.TrimSpace(line[6:])
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue // 忽略没用的行
		}

		raw := strings.TrimSpace(line[5:])

		if curEvent == "error" {
			// 直接把错误信息抛给客户端
			var e struct {
				Content string `json:"content"`
			}
			json.Unmarshal([]byte(raw), &e)
			errMsg := e.Content
			if errMsg == "" {
				errMsg = raw
			}
			fullMsg := "Error: " + errMsg
			sendDelta(w, flusher, chatID, model, created, &ChatDelta{Content: &fullMsg}, nil)
			break
		} else if curEvent != "" && curEvent != "message" {
			continue
		}

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
	// 支持 -cookie, -cookie2, -cookie3, ...
	var cookie1, cookie2, cookie3, cookie4, cookie5 string
	flag.StringVar(&listenAddr, "listen", ":8090", "listen address")
	flag.StringVar(&apiKey, "apikey", "", "API Key (optional)")
	flag.BoolVar(&saveConv, "save", false, "Save conversation to web history (optional)")
	flag.BoolVar(&debugMode, "debug", false, "Enable debug logging (optional)")
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
		logError("至少需要一个 -cookie 参数")
		logError("从浏览器 DevTools 的 Network 标签页中，任意请求的 Headers 中复制完整 Cookie 值")
		logError("例如: -cookie 'serviceToken=xxx; userId=6861418446; xiaomichatbot_ph=xxx'")
		logError("多账号负载均衡: -cookie '...' -cookie2 '...' -cookie3 '...'")
		return
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

	logSuccess("MiMo 2API Started on %s", listenAddr)
	logInfo("账号数: %d（每次请求随机选取）", len(cookies))
	logInfo("Endpoints: GET /health | POST /v1/chat/completions | GET /v1/models")
	if debugMode {
		logWarn("调试模式已开启 (-debug)，将输出详细请求日志")
	}

	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		logError("Server failed: %v", err)
	}
}
