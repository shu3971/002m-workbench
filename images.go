package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/draw"
	xwebp "golang.org/x/image/webp"
)

const maxUploadBytes = 20 * 1024 * 1024

var errNotImage = errors.New("不是可读的 JPG/PNG 图片")

type Upload struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Data string `json:"data"`
}

type ImageMeta struct {
	Mime   string
	Width  int
	Height int
	Bytes  int
}

func decodeUpload(u Upload) ([]byte, ImageMeta, error) {
	raw := strings.TrimSpace(u.Data)
	if strings.HasPrefix(raw, "data:") {
		if i := strings.Index(raw, ","); i >= 0 {
			raw = raw[i+1:]
		}
	}
	if raw == "" {
		return nil, ImageMeta{}, errNotImage
	}
	// base64 grows by 4/3; reject obviously oversized payloads before decoding.
	if len(raw) > maxUploadBytes*4/3+1024 {
		return nil, ImageMeta{}, fmt.Errorf("图片超过 20 MB")
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		if b2, err2 := base64.RawStdEncoding.DecodeString(raw); err2 == nil {
			b, err = b2, nil
		} else {
			return nil, ImageMeta{}, errNotImage
		}
	}
	meta, err := imageMeta(b)
	if err != nil {
		return nil, ImageMeta{}, err
	}
	return b, meta, nil
}

func imageMeta(raw []byte) (ImageMeta, error) {
	if len(raw) == 0 || len(raw) > maxUploadBytes {
		return ImageMeta{}, fmt.Errorf("图片为空或超过 20 MB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return ImageMeta{}, errNotImage
	}
	switch format {
	case "jpeg":
		format = "image/jpeg"
	case "png":
		format = "image/png"
	default:
		return ImageMeta{}, errNotImage
	}
	return ImageMeta{Mime: format, Width: cfg.Width, Height: cfg.Height, Bytes: len(raw)}, nil
}

func looksComplete(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	if bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")) {
		tail := []byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}
		return len(raw) >= 12 && bytes.Equal(raw[len(raw)-12:], tail)
	}
	if bytes.HasPrefix(raw, []byte("\xff\xd8")) {
		return len(raw) >= 2 && bytes.Equal(raw[len(raw)-2:], []byte("\xff\xd9"))
	}
	return false
}

func sizeFor(w, h int) string {
	if w > 0 && h > 0 {
		if w > h {
			return "1536x1024"
		}
		if w == h {
			return "1024x1024"
		}
	}
	return "1024x1536"
}

func extFor(mime string) string {
	if mime == "image/png" {
		return ".png"
	}
	return ".jpg"
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func saveWork(worksDir string, raw []byte, mime string) (string, ImageMeta, error) {
	meta, err := imageMeta(raw)
	if err != nil {
		return "", ImageMeta{}, err
	}
	if !looksComplete(raw) {
		return "", ImageMeta{}, errors.New("图片数据不完整（响应被截断）")
	}
	if err := os.MkdirAll(worksDir, 0o755); err != nil {
		return "", ImageMeta{}, err
	}
	name := time.Now().Format("20060102-150405") + "-" + randomID(3) + extFor(meta.Mime)
	path := filepath.Join(worksDir, name)
	tmp := path + ".part"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return "", ImageMeta{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", ImageMeta{}, err
	}
	return path, meta, nil
}

func safeID(s string) bool {
	if s == "" || len(s) > 80 {
		return false
	}
	for _, r := range s {
		if !(r == '-' || r == '_' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return !strings.Contains(s, "..")
}

func fetchImage(client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,*/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("图片下载失败：HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, 32*1024*1024+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(b) > 32*1024*1024 {
		return nil, errors.New("图片超过 32 MB")
	}
	return b, nil
}

func decodeImage(raw []byte) (image.Image, string, error) {
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		// x/image/webp is not registered with image.Decode; try it explicitly.
		if w, werr := xwebp.Decode(bytes.NewReader(raw)); werr == nil {
			return w, "webp", nil
		}
		return nil, "", err
	}
	return img, format, nil
}

func thumbnailJPEG(raw []byte, maxSide int) ([]byte, error) {
	src, _, err := decodeImage(raw)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("图片尺寸无效")
	}
	scale := float64(maxSide) / float64(max(w, h))
	if scale > 1 {
		scale = 1
	}
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	stddraw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, stddraw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".part-" + randomID(3)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
