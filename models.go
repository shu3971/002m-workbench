package main

import (
	"encoding/json"
	"time"
)

// Case is one approved gallery entry from the nanobanana-site fixtures.
type Case struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Category       string          `json:"category"`
	Tags           []string        `json:"tags"`
	Image          string          `json:"image"`
	PromptZH       string          `json:"prompt_zh"`
	PromptEN       string          `json:"prompt_en"`
	RemixVariables json.RawMessage `json:"remix_variables"`
	StrictBan      string          `json:"strict_ban"`
}

type CaseSnapshot struct {
	SchemaVersion string         `json:"schema_version"`
	Source        string         `json:"source"`
	ApprovedCount int            `json:"approved_count"`
	Count         int            `json:"count"`
	MissingIDs    []string       `json:"missing_ids"`
	Categories    map[string]int `json:"categories"`
	Tags          map[string]int `json:"tags"`
	Cases         []Case         `json:"cases"`
}

type HistoryEntry struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"created_at"`
	Source    string `json:"source"`
	Prompt    string `json:"prompt"`
	File      string `json:"file"`
	Mime      string `json:"mime"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	ImgKey    string `json:"img_key"`
	GroupID   string `json:"group_id"`
	Size      string `json:"size"`
	Starred   bool   `json:"starred"`
}

type Settings struct {
	ProxyMode string `json:"proxy_mode"` // system | manual | none
	ProxyURL  string `json:"proxy_url"`
}

type Status struct {
	ID         string `json:"id"`
	TrialLeft  int    `json:"trialLeft"`
	FreeLeft   int    `json:"freeLeft"`
	UnlockLeft int    `json:"unlockLeft"`
	Credit     int    `json:"credit"`
	CodeLeft   int    `json:"codeLeft"`
	DualUsed   bool   `json:"dualUsed"`
}

func (s Status) Free() int {
	if s.FreeLeft > 0 {
		return s.FreeLeft
	}
	return s.TrialLeft
}

func (s Status) Available() int { return s.Free() + s.Credit }

type GenResponse struct {
	Image  string  `json:"image"`
	Mime   string  `json:"mime"`
	ImgKey string  `json:"imgKey"`
	State  *Status `json:"state"`
	Error  string  `json:"error"`
}

type StatusEnvelope struct {
	Status *Status `json:"status,omitempty"`
	Error  string  `json:"error,omitempty"`
}

type APIError struct {
	Code    int
	Kind    string
	Message string
}

func (e *APIError) Error() string { return e.Message }

func nowMillis() int64 { return time.Now().UnixMilli() }
