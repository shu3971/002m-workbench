package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

type SettingsStore struct {
	path string
	mu   sync.RWMutex
	v    Settings
}

func NewSettingsStore(dataDir string) (*SettingsStore, error) {
	s := &SettingsStore{path: filepath.Join(dataDir, "settings.json"), v: Settings{ProxyMode: "system"}}
	b, err := os.ReadFile(s.path)
	if err == nil {
		_ = json.Unmarshal(b, &s.v)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if s.v.ProxyMode == "" {
		s.v.ProxyMode = "system"
	}
	return s, nil
}

func (s *SettingsStore) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.v
}

func (s *SettingsStore) Set(v Settings) error {
	if v.ProxyMode != "manual" && v.ProxyMode != "none" {
		v.ProxyMode = "system"
	}
	v.ProxyURL = strings.TrimSpace(v.ProxyURL)
	if v.ProxyMode == "manual" {
		if _, err := parseProxyURL(v.ProxyURL); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.v = v
	return nil
}

func parseProxyURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, os.ErrInvalid
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, os.ErrInvalid
	}
	return u, nil
}

// proxyFor returns the proxy selected by the user, the environment, or the OS.
func (s *SettingsStore) proxyFor() *url.URL {
	v := s.Get()
	switch v.ProxyMode {
	case "none":
		return nil
	case "manual":
		u, err := parseProxyURL(v.ProxyURL)
		if err == nil {
			return u
		}
		return nil
	}
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			if u, err := parseProxyURL(raw); err == nil {
				return u
			}
		}
	}
	return systemProxy()
}

func systemProxy() *url.URL {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "windows":
		out, err := exec.CommandContext(ctx, "reg", "query",
			`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`).Output()
		if err != nil {
			return nil
		}
		enabled := regexp.MustCompile(`(?m)^\s*ProxyEnable\s+REG_DWORD\s+0x1\s*$`).Match(out)
		if !enabled {
			return nil
		}
		m := regexp.MustCompile(`(?m)^\s*ProxyServer\s+REG_SZ\s+(.+?)\s*$`).FindSubmatch(out)
		if len(m) != 2 {
			return nil
		}
		raw := strings.TrimSpace(string(m[1]))
		if strings.Contains(raw, "=") {
			for _, part := range strings.Split(raw, ";") {
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(part)), "https=") {
					raw = strings.TrimSpace(part[6:])
					break
				}
			}
		}
		if u, err := parseProxyURL(raw); err == nil {
			return u
		}
	case "darwin":
		out, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
		if err != nil {
			return nil
		}
		text := string(out)
		get := func(key string) string {
			m := regexp.MustCompile(`(?m)^\s*` + key + `\s*:\s*(\S+)`).FindStringSubmatch(text)
			if len(m) == 2 {
				return m[1]
			}
			return ""
		}
		if get("HTTPSEnable") != "1" {
			return nil
		}
		host, port := get("HTTPSProxy"), get("HTTPSPort")
		if host == "" {
			return nil
		}
		if port != "" {
			host += ":" + port
		}
		if u, err := parseProxyURL(host); err == nil {
			return u
		}
	}
	return nil
}

func (s *SettingsStore) newHTTPClient(timeout time.Duration) *http.Client {
	proxy := s.proxyFor()
	tr := &http.Transport{
		Proxy:                 http.ProxyURL(proxy),
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		// Image generation legitimately takes longer than a status probe;
		// the per-call context timeout remains the hard ceiling.
		ResponseHeaderTimeout: 180 * time.Second,
	}
	if proxy == nil {
		tr.Proxy = nil
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}
