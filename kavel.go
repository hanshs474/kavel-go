// Package kavel generates and edits images from Go with no API key and no
// account.
//
// It calls the anonymous tier of kavel.ai, which meters its free allowance
// against a client id this package invents rather than an account you register,
// so a program using it works on a machine with nothing configured.
//
//	img, err := kavel.Generate(context.Background(), "a paper boat at sunrise", kavel.Options{AspectRatio: "16:9"})
//
// Measured against the running service on 2026-09-07: a signed-out caller is
// granted 15 credits, one generated image costs 5 and one edit costs 15, so the
// package mints a fresh client id per call and neither lane is limited to a
// single run. A ceiling of 30 credits per IP per day sits above that. Free
// output is 1K and watermarked.
package kavel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// BaseURL is the service this package talks to. It is a variable so a test or
// a corporate proxy can point the package somewhere else.
var BaseURL = "https://www.kavel.ai"

// Free-tier facts, read out of the running service rather than estimated.
const (
	// AnonGrant is the credit balance a signed-out caller starts with.
	AnonGrant = 15
	// CostTextToImage is what one generated image costs, so the grant pays for three.
	CostTextToImage = 5
	// CostEdit is what one edit costs: exactly the grant, so one edit per client id.
	CostEdit = 15
	// IPDailyCeiling caps one machine per day regardless of how many client ids it mints.
	IPDailyCeiling = 30
)

// Models reachable without an account. Anything else needs a signed-in session
// and returns ErrSignIn.
const (
	// ModelGenerate is the free text-to-image engine. It takes no image input.
	ModelGenerate = "kavel-image-v1"
	// ModelEdit is the free image-to-image engine. It requires a source image.
	ModelEdit = "nano-banana-2-lite"
)

// ErrQuota is returned when the free allowance is spent, either for this client
// id or for this machine's daily ceiling. Signing in at https://www.kavel.ai
// lifts the ceiling and removes the watermark.
var ErrQuota = errors.New("kavel: free allowance spent")

// ErrRejected is returned when the content filter refuses a prompt, or when the
// model fails on it. Rewording clears it; retrying the same text does not.
var ErrRejected = errors.New("kavel: prompt refused")

// ErrSignIn is returned when the request asks for something the anonymous tier
// does not serve — a model off the free shelf, or video, which costs more than
// the grant can pay at any setting.
var ErrSignIn = errors.New("kavel: this needs a signed-in account")

// Image is one finished picture.
type Image struct {
	// URL is a permanent, cacheable CDN link.
	URL string
	// Watermarked reports whether a mark was actually drawn on it. Signing in
	// removes it; the field is here so a caller can tell rather than assume.
	Watermarked bool
}

// Options tunes a single call. The zero value is valid and means a square image
// with a six minute deadline.
type Options struct {
	// AspectRatio is one of "1:1", "16:9", "9:16", "4:3", "3:4". Empty means
	// "1:1". Ignored by Edit, which keeps the source framing.
	AspectRatio string
	// PollEvery is how often the job is polled. Zero means five seconds.
	PollEvery time.Duration
	// Timeout bounds the whole call. Zero means six minutes.
	Timeout time.Duration
	// HTTPClient is used for every request. Nil means http.DefaultClient.
	HTTPClient *http.Client
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type submitData struct {
	ID     string `json:"id"`
	Wall   bool   `json:"wall"`
	Reason string `json:"reason"`
}

type queryData struct {
	Status      string   `json:"status"`
	Images      []string `json:"images"`
	Watermarked []bool   `json:"watermarked"`
	Queued      bool     `json:"queued"`
}

func anonID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("go-%d", time.Now().UnixNano())
	}
	return "go-" + hex.EncodeToString(b)
}

// Generate turns prompt into a new image and returns it.
//
// Naming the light, the material and the composition moves the result far more
// than adding adjectives: "a product photo of a mug" gives the model nothing,
// "matte black ceramic mug on pale oak, soft window light from the left,
// shallow depth of field" gives it a picture.
func Generate(ctx context.Context, prompt string, opts Options) (Image, error) {
	if strings.TrimSpace(prompt) == "" {
		return Image{}, errors.New("kavel: prompt is required")
	}
	ratio := opts.AspectRatio
	if ratio == "" {
		ratio = "1:1"
	}
	return run(ctx, opts, map[string]any{
		"provider":  "kie",
		"mediaType": "image",
		"model":     ModelGenerate,
		"scene":     "text-to-image",
		"prompt":    prompt,
		"options":   map[string]any{"aspect_ratio": ratio},
	})
}

// Edit rewrites an existing image. sourceURL must be a publicly reachable
// http(s) url and instruction says what to change ("give him a buzz cut",
// "put the product on a marble surface").
//
// One edit costs the whole anonymous grant, so a fresh client id buys exactly
// one. This is the lane behind the hairstyle, outfit and product-photo tools on
// the site.
func Edit(ctx context.Context, sourceURL, instruction string, opts Options) (Image, error) {
	if !strings.HasPrefix(sourceURL, "http://") && !strings.HasPrefix(sourceURL, "https://") {
		return Image{}, errors.New("kavel: sourceURL must be a public http(s) url")
	}
	if strings.TrimSpace(instruction) == "" {
		return Image{}, errors.New("kavel: instruction is required")
	}
	return run(ctx, opts, map[string]any{
		"provider":  "kie",
		"mediaType": "image",
		"model":     ModelEdit,
		"scene":     "image-to-image",
		"prompt":    instruction,
		"options":   map[string]any{"image_input": []string{sourceURL}},
	})
}

func run(ctx context.Context, opts Options, payload map[string]any) (Image, error) {
	poll := opts.PollEvery
	if poll <= 0 {
		poll = 5 * time.Second
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 6 * time.Minute
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// A fresh identity per call: the grant pays for one edit or three images, so
	// a reused id walls partway through a loop for reasons the caller cannot see.
	id := anonID()

	body, err := json.Marshal(payload)
	if err != nil {
		return Image{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/api/ai/generate", bytes.NewReader(body))
	if err != nil {
		return Image{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-anon-id", id)

	resp, err := client.Do(req)
	if err != nil {
		return Image{}, err
	}
	var env envelope
	err = json.NewDecoder(resp.Body).Decode(&env)
	resp.Body.Close()
	if err != nil {
		return Image{}, err
	}
	// Refusals answer HTTP 200 with code -1 and a human message; only the
	// message distinguishes "sign in for this" from an argument mistake.
	if env.Code != 0 {
		if strings.Contains(strings.ToLower(env.Message), "sign in") {
			return Image{}, fmt.Errorf("%w: %s", ErrSignIn, env.Message)
		}
		return Image{}, fmt.Errorf("kavel: %s", env.Message)
	}

	var submitted submitData
	if err := json.Unmarshal(env.Data, &submitted); err != nil {
		return Image{}, err
	}
	// The quota wall is also a 200 with code 0. Without this branch it surfaces
	// as the misleading "no task id".
	if submitted.Wall {
		switch submitted.Reason {
		case "anon_ip_daily":
			return Image{}, fmt.Errorf("%w: this machine has used its %d credits for today", ErrQuota, IPDailyCeiling)
		case "anon_unmetered_video":
			return Image{}, ErrSignIn
		}
		return Image{}, ErrQuota
	}
	if submitted.ID == "" {
		return Image{}, errors.New("kavel: the service returned no task id")
	}

	// Free runs sit in a 25-80s queue and are only sent to the provider by the
	// poll that crosses the end of it, so polling is not merely how the result
	// is read — it is what starts the work.
	query := fmt.Sprintf("%s/api/ai/anon-query?taskId=%s&provider=kie&mediaType=image", BaseURL, submitted.ID)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return Image{}, fmt.Errorf("kavel: %w (last poll: %v)", ctx.Err(), lastErr)
			}
			return Image{}, fmt.Errorf("kavel: %w", ctx.Err())
		case <-ticker.C:
		}

		polled, err := pollOnce(ctx, client, query, id)
		if err != nil {
			// A dropped connection mid-queue is not a failed generation, and
			// giving up on one would abandon a job that is about to be paid
			// for. Keep polling until the deadline instead.
			lastErr = err
			continue
		}
		lastErr = nil

		if len(polled.Images) > 0 {
			img := Image{URL: polled.Images[0]}
			if len(polled.Watermarked) > 0 {
				img.Watermarked = polled.Watermarked[0]
			}
			return img, nil
		}
		if polled.Status == "failed" || polled.Status == "error" {
			return Image{}, ErrRejected
		}
	}
}

func pollOnce(ctx context.Context, client *http.Client, query, id string) (queryData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, query, nil)
	if err != nil {
		return queryData{}, err
	}
	req.Header.Set("x-anon-id", id)
	resp, err := client.Do(req)
	if err != nil {
		return queryData{}, err
	}
	defer resp.Body.Close()

	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return queryData{}, err
	}
	if env.Code != 0 {
		return queryData{}, fmt.Errorf("kavel: %s", env.Message)
	}
	var polled queryData
	if err := json.Unmarshal(env.Data, &polled); err != nil {
		return queryData{}, err
	}
	return polled, nil
}

// Credits reports what the current machine and a given client id have left. It
// is the same read the site's own header does, and it costs nothing to call.
//
// Pass an empty clientID to ask about a fresh identity, which is what Generate
// and Edit each mint per call.
func Credits(ctx context.Context, clientID string, httpClient *http.Client) (remaining, grant int, err error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if clientID == "" {
		clientID = anonID()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseURL+"/api/ai/anon-credits", nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("x-anon-id", clientID)
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return 0, 0, err
	}
	if env.Code != 0 {
		return 0, 0, fmt.Errorf("kavel: %s", env.Message)
	}
	var data struct {
		Remaining int `json:"remaining"`
		Grant     int `json:"grant"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return 0, 0, err
	}
	return data.Remaining, data.Grant, nil
}
