package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"

const faceswapPrompt = "以第一张图片为底图，只把图中人物的脸换成第二张图片中人物的脸。" +
	"脸部以外的所有内容必须保持原样：服装、姿势、背景、光线、构图、画幅都不要动，" +
	"不要重新生成整张图。"

const (
	baseURL   = "https://gen.002m.com"
	alphabet  = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	maxRefs   = 12
	maxPrompt = 5000
)

type GenClient struct {
	settings *SettingsStore
	deviceID string
	base     string
}

func loadDeviceID() (string, error) {
	if id := strings.TrimSpace(os.Getenv("WORKBENCH_DEVICE_ID")); validDeviceID(id) {
		return id, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".002m", "device-id")
	if b, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(b))
		if validDeviceID(id) {
			return id, nil
		}
	}
	id := randomDeviceID()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id), 0o644); err != nil {
		return "", err
	}
	return id, nil
}

func validDeviceID(id string) bool {
	if len(id) < 8 || len(id) > 40 {
		return false
	}
	for _, r := range id {
		if !(r == '-' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			return false
		}
	}
	return true
}

func randomDeviceID() string {
	b := make([]byte, 12)
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		now := time.Now().UnixNano()
		for i := range b {
			b[i] = alphabet[int(now>>uint(i*5))%len(alphabet)]
		}
		return string(b)
	}
	for i := range b {
		b[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(b)
}

func (c *GenClient) cookie() string { return "gb_id=" + c.deviceID }

func (c *GenClient) do(method, path string, payload any, timeout time.Duration) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Cookie", c.cookie())
	client := c.settings.newHTTPClient(timeout)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 40*1024*1024+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("响应读取中断: %w", err)
	}
	if len(b) > 40*1024*1024 {
		return resp.StatusCode, nil, errors.New("响应超过 40 MB")
	}
	if resp.ContentLength > 0 && int64(len(b)) != resp.ContentLength {
		return resp.StatusCode, nil, errors.New("响应长度不完整")
	}
	return resp.StatusCode, b, nil
}

func (c *GenClient) status(timeout time.Duration) (*Status, error) {
	code, body, err := c.do(http.MethodGet, "/api/status", nil, timeout)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("额度查询失败：HTTP %d %s", code, apiMessage(body))
	}
	var st Status
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, fmt.Errorf("额度响应无法解析：%w", err)
	}
	if st.ID == "" {
		st.ID = c.deviceID
	}
	return &st, nil
}

func apiMessage(body []byte) string {
	var m map[string]any
	if json.Unmarshal(body, &m) == nil {
		if s, ok := m["error"].(string); ok && s != "" {
			return s
		}
		if s, ok := m["message"].(string); ok && s != "" {
			return s
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func statusFromMap(body []byte) *Status {
	var wrap struct {
		State *Status `json:"state"`
		Status *Status `json:"status"`
	}
	if json.Unmarshal(body, &wrap) == nil {
		if wrap.State != nil {
			return wrap.State
		}
		if wrap.Status != nil {
			return wrap.Status
		}
	}
	return nil
}

type generated struct {
	Raw    []byte
	Mime   string
	Width  int
	Height int
	ImgKey string
	State  *Status
}

func (c *GenClient) postGen(payload map[string]any, timeout time.Duration) (*generated, *APIError) {
	code, body, err := c.do(http.MethodPost, "/api/gen", payload, timeout)
	if err != nil {
		if strings.Contains(err.Error(), "中断") || strings.Contains(err.Error(), "不完整") ||
			strings.Contains(err.Error(), "unexpected EOF") {
			return nil, &APIError{Code: 0, Kind: "truncated", Message: "响应被截断"}
		}
		return nil, &APIError{Code: 0, Kind: "network", Message: err.Error()}
	}
	if code != http.StatusOK {
		log.Printf("002M upstream /api/gen HTTP %d: %s", code, apiMessage(body))
		return nil, &APIError{Code: code, Kind: errorKind(code), Message: apiMessage(body)}
	}
	var r GenResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, &APIError{Code: code, Kind: "truncated", Message: "响应不是完整 JSON"}
	}
	if r.Image == "" {
		msg := r.Error
		if msg == "" {
			msg = apiMessage(body)
		}
		return nil, &APIError{Code: code, Kind: "empty", Message: msg}
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(r.Image))
	if err != nil {
		return nil, &APIError{Code: code, Kind: "truncated", Message: "图片 base64 不完整"}
	}
	if !looksComplete(raw) {
		return nil, &APIError{Code: code, Kind: "truncated", Message: "图片数据不完整"}
	}
	meta, err := imageMeta(raw)
	if err != nil {
		return nil, &APIError{Code: code, Kind: "bad_image", Message: err.Error()}
	}
	state := r.State
	if state == nil {
		state = statusFromMap(body)
	}
	return &generated{Raw: raw, Mime: meta.Mime, Width: meta.Width, Height: meta.Height,
		ImgKey: r.ImgKey, State: state}, nil
}

func errorKind(code int) string {
	switch code {
	case 429:
		return "quota"
	case 451:
		return "blocked"
	case 502:
		return "upstream"
	default:
		return "http"
	}
}

func (c *GenClient) generate(prompt string, photo []byte, meta ImageMeta, since int64) (*generated, *APIError) {
	payload := map[string]any{
		"code":   "",
		"prompt": prompt,
		"size":   sizeFor(meta.Width, meta.Height),
		"image":  base64.StdEncoding.EncodeToString(photo),
		"mime":   meta.Mime,
	}
	g, apiErr := c.postGen(payload, 240*time.Second)
	if apiErr == nil {
		return g, nil
	}
	if apiErr.Kind == "truncated" || apiErr.Kind == "empty" || apiErr.Kind == "network" {
		if rec, rerr := c.recover(prompt, since, 3, 2*time.Second); rerr == nil {
			rec.State, _ = c.status(20 * time.Second)
			return rec, nil
		}
	}
	return nil, apiErr
}

func (c *GenClient) faceswapOne(avatar []byte, avatarMeta ImageMeta, ref []byte, refMeta ImageMeta, since int64) (*generated, *APIError) {
	payload := map[string]any{
		"code":   "",
		"prompt": faceswapPrompt,
		"images": []string{base64.StdEncoding.EncodeToString(ref), base64.StdEncoding.EncodeToString(avatar)},
		"mimes":  []string{refMeta.Mime, avatarMeta.Mime},
		"size":   sizeFor(refMeta.Width, refMeta.Height),
	}
	g, apiErr := c.postGen(payload, 260*time.Second)
	if apiErr == nil {
		return g, nil
	}
	if apiErr.Kind == "truncated" || apiErr.Kind == "empty" || apiErr.Kind == "network" {
		if rec, rerr := c.recover(faceswapPrompt, since, 6, 15*time.Second); rerr == nil {
			rec.State, _ = c.status(20 * time.Second)
			return rec, nil
		}
	}
	return nil, apiErr
}

type histItem struct {
	K string `json:"k"`
	P string `json:"p"`
	T int64  `json:"t"`
}

func (c *GenClient) recover(prompt string, since int64, attempts int, wait time.Duration) (*generated, error) {
	for i := 0; i < attempts; i++ {
		time.Sleep(wait)
		code, body, err := c.do(http.MethodGet, "/api/history", nil, 30*time.Second)
		if err != nil || code != http.StatusOK {
			continue
		}
		var h struct {
			Items []histItem `json:"items"`
		}
		if json.Unmarshal(body, &h) != nil {
			continue
		}
		for _, it := range h.Items {
			if it.K == "" {
				continue
			}
			if since > 0 && it.T > 0 && it.T < since-1500 {
				continue
			}
			if prompt != "" && !strings.HasPrefix(it.P, promptPrefix(prompt)) {
				continue
			}
			raw, err := c.fetchBinary("/img/" + it.K)
			if err != nil || !looksComplete(raw) {
				continue
			}
			meta, err := imageMeta(raw)
			if err != nil {
				continue
			}
			return &generated{Raw: raw, Mime: meta.Mime, Width: meta.Width,
				Height: meta.Height, ImgKey: it.K}, nil
		}
	}
	return nil, errors.New("history 中未找到可恢复的成图")
}

func promptPrefix(p string) string {
	r := []rune(p)
	if len(r) > 40 {
		return string(r[:40])
	}
	return p
}

func (c *GenClient) fetchBinary(path string) ([]byte, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", browserUA)
		req.Header.Set("Cookie", c.cookie())
		resp, err := c.settings.newHTTPClient(120 * time.Second).Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 40*1024*1024+1))
		resp.Body.Close()
		if rerr == nil && resp.StatusCode == http.StatusOK && looksComplete(body) {
			return body, nil
		}
		if rerr != nil {
			lastErr = rerr
		} else {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		time.Sleep(2 * time.Second)
	}
	if lastErr == nil {
		lastErr = errors.New("下载失败")
	}
	return nil, lastErr
}

func (c *GenClient) redeem(code string) (int, *Status, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return 0, nil, errors.New("请输入兑换码")
	}
	// The code is deliberately never written to disk or logs.
	payload := map[string]string{"code": code}
	status, body, err := c.do(http.MethodPost, "/api/redeem", payload, 30*time.Second)
	if err != nil {
		return 0, nil, err
	}
	if status != http.StatusOK {
		return 0, nil, fmt.Errorf("兑换失败：%s", apiMessage(body))
	}
	var r struct {
		OK    bool    `json:"ok"`
		Added int     `json:"added"`
		State *Status `json:"state"`
		Error string  `json:"error"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return 0, nil, err
	}
	if !r.OK {
		return 0, nil, fmt.Errorf("兑换失败：%s", r.Error)
	}
	return r.Added, r.State, nil
}
