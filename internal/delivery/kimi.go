package delivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/windviki/agentbus/internal/bus"
)

type kimiInstance struct {
	Product   string `json:"product"`
	PID       int    `json:"pid"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	StartedAt int64  `json:"started_at"`
}

func pushKimi(ctx context.Context, s bus.Session, msgs []bus.Msg) error {
	base, token, err := kimiServer(ctx, s.Delivery.Token, s.Delivery.Address)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	promptID := kimiPromptID(ids)
	body := map[string]any{
		"content":   []map[string]string{{"type": "text", "text": IncomingText(msgs)}},
		"prompt_id": promptID,
		"metadata":  map[string]any{"agentbus": true, "message_ids": ids},
	}
	action := "prompts"
	if s.Working {
		action = "steer"
	}
	endpoint := fmt.Sprintf("%s/api/v1/sessions/%s/%s", strings.TrimRight(base, "/"), url.PathEscape(strings.TrimPrefix(s.Delivery.NativeID, "sid:")), action)
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", promptID)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("Kimi %s: %w", action, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("Kimi %s response: %w", action, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Kimi %s: HTTP %s", action, resp.Status)
	}
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("Kimi %s: invalid response", action)
	}
	if envelope.Code != 0 {
		return fmt.Errorf("Kimi %s: code %d: %s", action, envelope.Code, envelope.Msg)
	}
	return nil
}

func kimiPromptID(ids []string) string {
	sum := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	return fmt.Sprintf("agentbus-%x", sum[:12])
}

func kimiServer(ctx context.Context, explicitToken, explicitURL string) (string, string, error) {
	token := explicitToken
	if token == "" {
		token = strings.TrimSpace(os.Getenv("AGENTBUS_KIMI_TOKEN"))
	}
	base := strings.TrimSpace(explicitURL)
	if base == "" {
		base = strings.TrimSpace(os.Getenv("AGENTBUS_KIMI_URL"))
	}
	if base != "" {
		if err := loopbackURL(base); err != nil {
			return "", "", err
		}
		return strings.TrimRight(base, "/"), token, nil
	}
	home := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("Kimi server: home directory unavailable")
		}
		home = filepath.Join(userHome, ".kimi-code")
	}
	if token == "" {
		if data, err := os.ReadFile(filepath.Join(home, "server.token")); err == nil {
			token = strings.TrimSpace(string(data))
		}
	}
	entries, err := os.ReadDir(filepath.Join(home, "server", "instances"))
	if err != nil {
		return "", "", fmt.Errorf("Kimi server unavailable (start kimi web or set AGENTBUS_KIMI_URL): %w", err)
	}
	var instances []kimiInstance
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(home, "server", "instances", entry.Name()))
		if err != nil {
			continue
		}
		var instance kimiInstance
		if json.Unmarshal(data, &instance) == nil && instance.Port > 0 {
			instances = append(instances, instance)
		}
	}
	sort.SliceStable(instances, func(i, j int) bool {
		iDesktop := instances[i].Product == "kimi-code-desktop"
		jDesktop := instances[j].Product == "kimi-code-desktop"
		if iDesktop != jDesktop {
			return iDesktop
		}
		return instances[i].StartedAt > instances[j].StartedAt
	})
	for _, instance := range instances {
		base := instanceURL(instance)
		if base == "" {
			continue
		}
		if kimiMetaReachable(ctx, base, token) {
			return base, token, nil
		}
	}
	return "", "", fmt.Errorf("no reachable Kimi server instance")
}

func instanceURL(instance kimiInstance) string {
	host := strings.TrimSpace(instance.Host)
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::", "::1":
		host = "::1"
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() {
		return ""
	}
	return "http://" + net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(instance.Port))
}

func loopbackURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" {
		return fmt.Errorf("AGENTBUS_KIMI_URL must be a loopback HTTP URL")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("AGENTBUS_KIMI_URL must stay on loopback")
	}
	return nil
}

func kimiMetaReachable(ctx context.Context, base, token string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/meta", nil)
	if err != nil {
		return false
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var envelope struct {
		Code int `json:"code"`
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope) == nil && envelope.Code == 0
}
