package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	app    *App
	token  string
	assets http.Handler
	locks  sync.Map
}

type bootstrapResponse struct {
	Version   string         `json:"version"`
	DeviceID  string         `json:"device_id"`
	DataDir   string         `json:"data_dir"`
	WorksDir  string         `json:"works_dir"`
	OS        string         `json:"os"`
	Settings  Settings       `json:"settings"`
	Status    *Status        `json:"status"`
	StatusErr string         `json:"status_error,omitempty"`
	Cases     CaseSnapshot   `json:"cases"`
	History   []HistoryEntry `json:"history"`
	Error     string         `json:"error,omitempty"`
}

func NewServer(app *App, token string, assetsHandler http.Handler) *Server {
	return &Server{app: app, token: token, assets: assetsHandler}
}

func (s *Server) Start(addr string) (net.Listener, *http.Server, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	srv := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server stopped: %v", err)
		}
	}()
	return ln, srv, nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", s.handleAPI)
	mux.HandleFunc("/thumb/", s.handleThumb)
	mux.HandleFunc("/case-image/", s.handleCaseImage)
	mux.HandleFunc("/media/", s.handleMedia)
	mux.Handle("/", s.static())
	return mux
}

func (s *Server) static() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
			w.Header().Set("Cache-Control", "no-cache")
		}
		s.assets.ServeHTTP(w, r)
	})
}

func (s *Server) tokenOK(r *http.Request, query bool) bool {
	if s.token == "" {
		return true
	}
	if r.Header.Get("X-002M-Token") == s.token {
		return true
	}
	return query && r.URL.Query().Get("t") == s.token
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if !s.tokenOK(r, false) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "本地会话校验失败，请重新打开应用"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	switch {
	case path == "bootstrap" && r.Method == http.MethodGet:
		s.bootstrap(w, r)
	case path == "status" && r.Method == http.MethodGet:
		s.status(w, r)
	case path == "gen" && r.Method == http.MethodPost:
		s.gen(w, r)
	case path == "faceswap" && r.Method == http.MethodPost:
		s.faceswap(w, r)
	case path == "history" && r.Method == http.MethodGet:
		s.history(w, r)
	case path == "history/star" && r.Method == http.MethodPost:
		s.star(w, r)
	case path == "history/delete" && r.Method == http.MethodPost:
		s.deleteHistory(w, r)
	case path == "history/export" && r.Method == http.MethodPost:
		s.export(w, r)
	case path == "settings" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"settings": s.app.Settings.Get()})
	case path == "settings" && r.Method == http.MethodPost:
		s.saveSettings(w, r)
	case path == "redeem" && r.Method == http.MethodPost:
		s.redeem(w, r)
	case path == "update" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.app.checkUpdate())
	case path == "open" && r.Method == http.MethodPost:
		s.open(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "接口不存在"})
	}
}

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	st, err := s.app.Client.status(15 * time.Second)
	resp := bootstrapResponse{
		Version:  appVersion,
		DeviceID: s.app.DeviceID,
		DataDir:  s.app.DataDir,
		WorksDir: s.app.WorksDir,
		OS:       runtimeGOOS(),
		Settings: s.app.Settings.Get(),
		Cases:    s.app.Cases,
	}
	if err != nil {
		resp.StatusErr = err.Error()
	} else {
		resp.Status = st
	}
	if h, err := s.app.Store.List(HistoryFilter{Limit: 60}); err == nil {
		resp.History = h
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	st, err := s.app.Client.status(30 * time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st})
}

type genRequest struct {
	Prompt string `json:"prompt"`
	Photo  Upload `json:"photo"`
}

func (s *Server) gen(w http.ResponseWriter, r *http.Request) {
	var req genRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求格式错误：" + err.Error()})
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请填写英文提示词", "charged": false})
		return
	}
	if len([]rune(prompt)) > maxPrompt {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "提示词过长（最多 5000 字）", "charged": false})
		return
	}
	photo, meta, err := decodeUpload(req.Photo)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "照片不合格：" + err.Error() + "。已在调用接口前拦截，0 扣费。", "charged": false})
		return
	}
	since := nowMillis()
	g, apiErr := s.app.Client.generate(prompt, photo, meta, since)
	if apiErr != nil {
		writeJSON(w, statusForError(apiErr), map[string]any{
			"error":   friendlyError(apiErr),
			"kind":    apiErr.Kind,
			"code":    apiErr.Code,
			"charged": false,
		})
		return
	}
	entry, err := s.app.addEntry(g, prompt, "gen", "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   "成图已生成并已扣额度，但保存到本地历史失败：" + err.Error(),
			"charged": true,
			"img_key": g.ImgKey,
		})
		return
	}
	st := g.State
	if st == nil {
		st, _ = s.app.Client.status(20 * time.Second)
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": entry, "status": st, "charged": true})
}

type refUpload struct {
	Upload
	Index int `json:"index"`
}

type faceswapRequest struct {
	Avatar Upload      `json:"avatar"`
	Refs   []refUpload `json:"refs"`
}

type faceResult struct {
	Index int           `json:"index"`
	Entry *HistoryEntry `json:"entry,omitempty"`
	Error string        `json:"error,omitempty"`
}

func (s *Server) faceswap(w http.ResponseWriter, r *http.Request) {
	var req faceswapRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求格式错误：" + err.Error()})
		return
	}
	if len(req.Refs) > maxRefs {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("最多 %d 张参考图", maxRefs), "charged": false})
		return
	}
	if len(req.Refs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请至少上传 1 张参考图", "charged": false})
		return
	}
	avatar, avatarMeta, err := decodeUpload(req.Avatar)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "头像不合格：" + err.Error() + "。已在调用接口前拦截，0 扣费。", "charged": false})
		return
	}
	groupID := randomID(5)
	results := make([]faceResult, 0, len(req.Refs))
	generatedCount := 0
	chargedCount := 0
	for i, ref := range req.Refs {
		raw, meta, derr := decodeUpload(ref.Upload)
		if derr != nil {
			results = append(results, faceResult{Index: i, Error: "跳过：" + derr.Error() + "（未扣费）"})
			continue
		}
		since := nowMillis()
		g, apiErr := s.app.Client.faceswapOne(avatar, avatarMeta, raw, meta, since)
		if apiErr != nil {
			results = append(results, faceResult{Index: i, Error: friendlyError(apiErr)})
			continue
		}
		entry, err := s.app.addEntry(g, faceswapPrompt, "faceswap", groupID)
		if err != nil {
			results = append(results, faceResult{Index: i, Error: "成图已扣额度但本地保存失败：" + err.Error()})
			chargedCount++
			continue
		}
		results = append(results, faceResult{Index: i, Entry: entry})
		generatedCount++
		chargedCount++
	}
	st, _ := s.app.Client.status(30 * time.Second)
	writeJSON(w, http.StatusOK, map[string]any{
		"results":   results,
		"generated": generatedCount,
		"requested": len(req.Refs),
		"charged":   chargedCount > 0,
		"status":    st,
	})
}

func friendlyError(e *APIError) string {
	switch e.Kind {
	case "quota":
		return "免费额度已用完或全站排队（429）。请稍后再试，或在设置里兑换额度。"
	case "blocked":
		return "内容被上游拒绝（451）。请调整提示词或更换照片。"
	case "upstream":
		return "上游未出图（502）。本次响应没有返回成图，请稍后再试；不要连续重点，避免重复扣费。"
	case "truncated", "empty", "network":
		return "响应中断：" + e.Message + "。已按规则查询 history 尝试恢复；若未恢复，请先到 gen.002m.com 查看，确认没有成图再重试。"
	default:
		return fmt.Sprintf("生成失败（HTTP %d）：%s", e.Code, e.Message)
	}
}

func statusForError(e *APIError) int {
	switch e.Kind {
	case "quota":
		return http.StatusTooManyRequests
	case "blocked":
		return http.StatusUnavailableForLegalReasons
	case "upstream":
		return http.StatusBadGateway
	default:
		return http.StatusBadGateway
	}
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := HistoryFilter{
		Query:   q.Get("q"),
		Source:  q.Get("source"),
		Starred: q.Get("starred") == "1",
	}
	if n, _ := strconv.Atoi(q.Get("limit")); n > 0 {
		f.Limit = n
	}
	list, err := s.app.Store.List(f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "count": len(list)})
}

func (s *Server) star(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少记录 id"})
		return
	}
	starred, err := s.app.Store.ToggleStar(req.ID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "记录不存在"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"starred": starred})
}

func (s *Server) deleteHistory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少记录 id"})
		return
	}
	file, err := s.app.Store.Delete(req.ID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "记录不存在"})
		return
	}
	if pathInside(file, s.app.WorksDir) {
		_ = os.Remove(file)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	list, err := s.app.Store.List(HistoryFilter{Limit: 500})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	dir, err := s.app.Store.Export(s.app.Exports, list)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "导出失败：" + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "count": len(list)})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var v Settings
	if err := decodeJSON(w, r, &v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "设置格式错误"})
		return
	}
	if err := s.app.Settings.Set(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "代理地址无效，示例：http://127.0.0.1:7890"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": s.app.Settings.Get()})
}

func (s *Server) redeem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求格式错误"})
		return
	}
	added, st, err := s.app.Client.redeem(req.Code)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "status": st})
}

func (s *Server) open(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求格式错误"})
		return
	}
	if err := s.app.openPath(req.Kind, req.ID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "打开失败：" + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) lockFor(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	if !s.tokenOK(r, true) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/thumb/")
	if !safeID(id) {
		http.NotFound(w, r)
		return
	}
	c := s.app.caseByID[id]
	if c == nil || c.Image == "" {
		http.NotFound(w, r)
		return
	}
	cache := filepath.Join(s.app.CacheDir, "thumbs", id+".jpg")
	if fileExists(cache) {
		serveFile(w, r, cache, "image/jpeg")
		return
	}
	mu := s.lockFor("thumb:" + id)
	mu.Lock()
	defer mu.Unlock()
	if fileExists(cache) {
		serveFile(w, r, cache, "image/jpeg")
		return
	}
	raw, err := fetchImage(s.app.Settings.newHTTPClient(45*time.Second), "https://www.002m.com"+c.Image)
	if err != nil {
		http.Error(w, "thumbnail unavailable", http.StatusBadGateway)
		return
	}
	thumb, err := thumbnailJPEG(raw, 560)
	if err != nil {
		http.Error(w, "bad image", http.StatusBadGateway)
		return
	}
	if err := writeAtomic(cache, thumb); err != nil {
		http.Error(w, "cache write failed", http.StatusInternalServerError)
		return
	}
	serveFile(w, r, cache, "image/jpeg")
}

func (s *Server) handleCaseImage(w http.ResponseWriter, r *http.Request) {
	if !s.tokenOK(r, true) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/case-image/")
	if !safeID(id) {
		http.NotFound(w, r)
		return
    }
	c := s.app.caseByID[id]
	if c == nil || c.Image == "" {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(c.Image))
	if ext == "" {
		ext = ".jpg"
	}
	cache := filepath.Join(s.app.CacheDir, "cases", id+ext)
	if fileExists(cache) {
		serveFile(w, r, cache, contentTypeForExt(ext))
		return
	}
	mu := s.lockFor("case:" + id)
	mu.Lock()
	defer mu.Unlock()
	if fileExists(cache) {
		serveFile(w, r, cache, contentTypeForExt(ext))
		return
	}
	raw, err := fetchImage(s.app.Settings.newHTTPClient(60*time.Second), "https://www.002m.com"+c.Image)
	if err != nil || !looksComplete(raw) {
		http.Error(w, "image unavailable", http.StatusBadGateway)
		return
	}
	if err := writeAtomic(cache, raw); err != nil {
		http.Error(w, "cache write failed", http.StatusInternalServerError)
		return
	}
	serveFile(w, r, cache, contentTypeForExt(ext))
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if !s.tokenOK(r, true) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/media/")
	if !safeID(id) {
		http.NotFound(w, r)
		return
	}
	e, err := s.app.Store.Get(id)
	if err != nil || !pathInside(e.File, s.app.WorksDir) {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, e.File, e.Mime)
}

func serveFile(w http.ResponseWriter, r *http.Request, path, ctype string) {
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeFile(w, r, path)
}

func contentTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 70*1024*1024)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("请求为空")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func runtimeGOOS() string { return runtime.GOOS + "/" + runtime.GOARCH }
