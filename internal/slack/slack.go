package slack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

var errFake = errors.New("fake slack failure")

func tsString(n int) string { return "100." + strconv.Itoa(n) }

type Client interface {
	PostMessage(text, threadTS string) (ts string, err error)
	UpdateMessage(ts, text string) error
}

type API struct {
	token   string
	channel string
	http    *http.Client
	baseURL string // Slack API base; overridable in tests
}

func New(token, channelID string) *API {
	return &API{
		token:   token,
		channel: channelID,
		http:    &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://slack.com/api",
	}
}

func (a *API) PostMessage(text, threadTS string) (string, error) {
	payload := map[string]any{"channel": a.channel, "text": text}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}
	var out struct {
		OK    bool   `json:"ok"`
		TS    string `json:"ts"`
		Error string `json:"error"`
	}
	if err := a.call("chat.postMessage", payload, &out); err != nil {
		return "", err
	}
	if !out.OK {
		return "", fmt.Errorf("slack chat.postMessage: %s", out.Error)
	}
	return out.TS, nil
}

func (a *API) UpdateMessage(ts, text string) error {
	payload := map[string]any{"channel": a.channel, "ts": ts, "text": text}
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := a.call("chat.update", payload, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("slack chat.update: %s", out.Error)
	}
	return nil
}

func (a *API) call(method string, payload any, out any) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, a.baseURL+"/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}
