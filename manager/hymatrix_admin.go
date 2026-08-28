package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type hymatrixAdminError struct {
	Error string `json:"error"`
}

const defaultHymatrixAdminURL = "http://127.0.0.1:8082"

func (m *Manager) hymatrixAdminURL(adminURL string) string {
	if value := strings.TrimSpace(adminURL); value != "" {
		return value
	}
	if value := strings.TrimSpace(m.config.MiniProgram.AdminURL); value != "" {
		return value
	}
	return defaultHymatrixAdminURL
}

func (m *Manager) stopHymatrixVM(ctx context.Context, adminURL, pid string) error {
	adminURL = m.hymatrixAdminURL(adminURL)
	pid = strings.TrimSpace(pid)
	if pid == "" || strings.HasPrefix(pid, "pending_") {
		return nil
	}
	base, err := url.Parse(adminURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return errors.New("invalid Hymx admin URL")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/admin/vms/stop"
	base.RawQuery, base.Fragment = "", ""
	body, err := json.Marshal(map[string]string{"pid": pid})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := m.hymatrixAdminClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode/100 == 2 {
		return nil
	}
	var apiErr hymatrixAdminError
	if json.Unmarshal(raw, &apiErr) == nil {
		switch apiErr.Error {
		case "err_process_not_found", "err_process_stopped":
			return nil
		}
		if apiErr.Error != "" {
			return errors.New(apiErr.Error)
		}
	}
	return fmt.Errorf("HTTP %d", res.StatusCode)
}
