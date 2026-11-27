package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const appVersion = "1.0.0"

type App struct {
	DataDir  string
	WorksDir string
	CacheDir string
	Exports  string
	Store    *Store
	Settings *SettingsStore
	Client   *GenClient
	Cases    CaseSnapshot
	caseByID map[string]*Case
	DeviceID string
}

func NewApp() (*App, error) {
	dataDir := strings.TrimSpace(os.Getenv("WORKBENCH_DATA_DIR"))
	if dataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		dataDir = filepath.Join(base, "002M Workbench")
	}
	app := &App{
		DataDir:  dataDir,
		WorksDir: filepath.Join(dataDir, "works"),
		CacheDir: filepath.Join(dataDir, "cache"),
		Exports:  filepath.Join(dataDir, "exports"),
	}
	for _, dir := range []string{app.DataDir, app.WorksDir, app.CacheDir, app.Exports} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	st, err := NewSettingsStore(app.DataDir)
	if err != nil {
		return nil, err
	}
	app.Settings = st
	store, err := NewStore(filepath.Join(app.DataDir, "history.db"))
	if err != nil {
		return nil, err
	}
	app.Store = store
	id, err := loadDeviceID()
	if err != nil {
		store.Close()
		return nil, err
	}
	app.DeviceID = id
	app.Client = &GenClient{settings: st, deviceID: id, base: baseURL}
	if err := app.loadCases(); err != nil {
		store.Close()
		return nil, err
	}
	return app, nil
}

func (a *App) Close() {
	if a.Store != nil {
		a.Store.Close()
	}
}

func (a *App) loadCases() error {
	b, err := assets.ReadFile("frontend/dist/data/cases.json")
	if err != nil {
		return fmt.Errorf("内置案例快照缺失: %w", err)
	}
	if err := json.Unmarshal(b, &a.Cases); err != nil {
		return fmt.Errorf("案例快照无法解析: %w", err)
	}
	a.caseByID = make(map[string]*Case, len(a.Cases.Cases))
	for i := range a.Cases.Cases {
		c := &a.Cases.Cases[i]
		if c.ID != "" {
			a.caseByID[c.ID] = c
		}
	}
	return nil
}

func (a *App) addEntry(g *generated, prompt, source, groupID string) (*HistoryEntry, error) {
	path, meta, err := saveWork(a.WorksDir, g.Raw, g.Mime)
	if err != nil {
		return nil, err
	}
	if g.Width == 0 && g.Height == 0 {
		g.Width, g.Height = meta.Width, meta.Height
	}
	e := HistoryEntry{
		ID:        randomID(8),
		CreatedAt: nowMillis(),
		Source:    source,
		Prompt:    prompt,
		File:      path,
		Mime:      meta.Mime,
		Width:     g.Width,
		Height:    g.Height,
		ImgKey:    g.ImgKey,
		GroupID:   groupID,
		Size:      sizeFor(g.Width, g.Height),
	}
	if err := a.Store.Add(e); err != nil {
		return nil, err
	}
	return &e, nil
}

func (a *App) openPath(kind, id string) error {
	var target string
	switch kind {
	case "data":
		target = a.DataDir
	case "works":
		target = a.WorksDir
	case "exports":
		target = a.Exports
	case "file":
		e, err := a.Store.Get(id)
		if err != nil {
			return err
		}
		target = e.File
	default:
		return errors.New("未知目录")
	}
	if !pathInside(target, a.DataDir) {
		return errors.New("路径越界")
	}
	if _, err := os.Stat(target); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "windows":
		if kind == "file" {
			return exec.Command("explorer.exe", "/select,", target).Start()
		}
		return exec.Command("explorer.exe", target).Start()
	case "darwin":
		if kind == "file" {
			return exec.Command("open", "-R", target).Start()
		}
		return exec.Command("open", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

func pathInside(path, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	URL       string `json:"url"`
	Note      string `json:"note"`
}

func (a *App) checkUpdate() UpdateInfo {
	info := UpdateInfo{Current: appVersion}
	client := a.Settings.newHTTPClient(12 * time.Second)
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/shu3971/002m-workbench/releases/latest", nil)
	req.Header.Set("User-Agent", "002m-workbench/"+appVersion)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		info.Note = "检查更新失败：" + err.Error()
		return info
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		info.Note = "暂无正式 Release（首次发布后会在这里提示）"
		return info
	}
	if resp.StatusCode != http.StatusOK {
		info.Note = fmt.Sprintf("检查更新失败：HTTP %d", resp.StatusCode)
		return info
	}
	var r struct {
		Tag    string `json:"tag_name"`
		HTML   string `json:"html_url"`
		Draft  bool   `json:"draft"`
		Assets []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		info.Note = "检查更新失败：响应无法解析"
		return info
	}
	info.Latest = strings.TrimPrefix(r.Tag, "v")
	info.URL = r.HTML
	if info.Latest != "" && compareVersion(info.Latest, appVersion) > 0 {
		info.Available = true
		info.Note = "发现新版本，可前往 GitHub Releases 下载。"
	} else {
		info.Note = "已是最新版本。"
	}
	return info
}

func compareVersion(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			fmt.Sscanf(pa[i], "%d", &x)
		}
		if i < len(pb) {
			fmt.Sscanf(pb[i], "%d", &y)
		}
		if x > y {
			return 1
		}
		if x < y {
			return -1
		}
	}
	return 0
}
