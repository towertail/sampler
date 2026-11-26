package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/towertail/sampler/pkg/schema"
)

var (
	// ErrJoinRejected means that the server does not accept the join token (HTTP 401).
	ErrJoinRejected = errors.New("join token rejected")
	// ErrPlanFull means that the plan has no room for a new node (HTTP 402).
	ErrPlanFull = errors.New("plan is full")
)

// Join trades a one-use join token for a sampler token
// (POST /v1/sampler/join). An empty machineID is left out of the request.
func Join(ctx context.Context, endpoint, joinToken, name, machineID string, insecure bool) (schema.EnrollResponse, error) {
	var e schema.EnrollResponse
	body, err := json.Marshal(schema.JoinRequest{JoinToken: joinToken, DisplayName: name, MachineID: machineID})
	if err != nil {
		return e, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(endpoint, "/v1/sampler/join"), bytes.NewReader(body))
	if err != nil {
		return e, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient(insecure).Do(req)
	if err != nil {
		return e, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return e, ErrJoinRejected
	case resp.StatusCode == http.StatusPaymentRequired:
		return e, ErrPlanFull
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return e, fmt.Errorf("join: server %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReplyBytes)).Decode(&e); err != nil {
		return e, fmt.Errorf("join: bad reply: %w", err)
	}
	if e.SamplerToken == "" {
		return e, errors.New("join: reply has no sampler token")
	}
	return e, nil
}
