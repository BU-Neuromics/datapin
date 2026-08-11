package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FAIRScore is the summary of an F-UJI evaluation (percentages).
type FAIRScore struct {
	FAIR float64 `json:"fair"`
	F    float64 `json:"f"`
	A    float64 `json:"a"`
	I    float64 `json:"i"`
	R    float64 `json:"r"`
}

// FUJIClient calls an F-UJI server's evaluate endpoint
// (https://www.f-uji.net — self-hostable; the hosted demo requires basic
// auth). datapin never implements FAIR metrics itself (plan §2.5).
type FUJIClient struct {
	url  string
	user string
	pass string
	http *http.Client
}

// NewFUJIClient targets url (the …/evaluate endpoint) with optional
// basic-auth credentials.
func NewFUJIClient(url, user, pass string) *FUJIClient {
	return &FUJIClient{url: url, user: user, pass: pass,
		http: &http.Client{Timeout: 180 * time.Second}} // F-UJI probes dozens of endpoints; slow is normal
}

// Evaluate runs a FAIR assessment of the identifier (normally a DOI).
func (c *FUJIClient) Evaluate(ctx context.Context, identifier string) (FAIRScore, error) {
	body, err := json.Marshal(map[string]any{
		"object_identifier": identifier,
		"test_debug":        false,
		"use_datacite":      true,
	})
	if err != nil {
		return FAIRScore{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return FAIRScore{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return FAIRScore{}, fmt.Errorf("calling F-UJI: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return FAIRScore{}, fmt.Errorf("F-UJI returned HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(data))
	}
	var out struct {
		Summary struct {
			ScorePercent map[string]float64 `json:"score_percent"`
		} `json:"summary"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return FAIRScore{}, fmt.Errorf("parsing F-UJI response: %w", err)
	}
	sp := out.Summary.ScorePercent
	return FAIRScore{FAIR: sp["FAIR"], F: sp["F"], A: sp["A"], I: sp["I"], R: sp["R"]}, nil
}
