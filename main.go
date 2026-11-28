package main

import (
	"context"
	"embed"
	"flag"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	headless := flag.Bool("headless", false, "run the local HTTP service without a window (for smoke tests)")
	addrFlag := flag.String("addr", "127.0.0.1:0", "local listen address in headless mode")
	flag.Parse()

	app, err := NewApp()
	if err != nil {
		log.Fatalf("002M 工作台启动失败: %v", err)
	}
	defer app.Close()

	logPath := filepath.Join(app.DataDir, "workbench.log")
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		defer f.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, f))
	}
	log.Printf("002M Workbench %s starting; data=%s device=%s", appVersion, app.DataDir, app.DeviceID)

	sub, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatalf("内置前端资源不可用: %v", err)
	}
	token := strings.TrimSpace(os.Getenv("WORKBENCH_TOKEN"))
	if token == "" {
		token = randomID(12)
	}
	server := NewServer(app, token, http.FileServer(http.FS(sub)))
	addr := "127.0.0.1:0"
	if *headless {
		addr = *addrFlag
	}
	ln, httpServer, err := server.Start(addr)
	if err != nil {
		log.Fatalf("本地服务启动失败: %v", err)
	}
	listenAddr := ln.Addr().String()
	log.Printf("local service: http://%s", listenAddr)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()

	if *headless {
		// Print a single machine-readable line for the smoke-test runner.
		os.Stdout.WriteString("LISTEN=" + listenAddr + "\nTOKEN=" + token + "\n")
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		return
	}

	wailsApp := application.New(application.Options{
		Name:        "002M 工作台",
		Description: "002M 桌面工作台 v1.0.0 — 案例画廊 / 照片生图 / 换脸套图 / 本地历史",
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "002M 工作台",
		Width:            1320,
		Height:           860,
		MinWidth:         1000,
		MinHeight:        660,
		BackgroundColour: application.NewRGB(14, 15, 19),
		URL:              "http://" + listenAddr + "/?t=" + url.QueryEscape(token),
	})
	if err := wailsApp.Run(); err != nil {
		log.Fatal(err)
	}
}
