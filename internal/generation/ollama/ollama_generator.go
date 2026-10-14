package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type OllamaGenerator struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

type generateRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Stream  bool                   `json:"stream"`
	Options map[string]interface{} `json:"options,omitempty"`
}

type generateResponse struct {
	Response string `json:"response"`
}

type streamResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

func NewGenerator(baseURL, model string) *OllamaGenerator {
	return &OllamaGenerator{
		baseURL: baseURL,
		model:   model,
		httpClient: &http.Client{
			Timeout: 300 * time.Second,
		},
	}
}

// Generate returns the whole completion in a single response.
func (g *OllamaGenerator) Generate(ctx context.Context, prompt string) (string, error) {
	reqBody := generateRequest{
		Model:  g.model,
		Prompt: prompt,
		Stream: false,
		Options: map[string]interface{}{
			"temperature": 0.3,
			"top_p":       0.9,
		},
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", g.baseURL+"/api/generate", bytes.NewReader(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama generate error: %s, body: %s", resp.Status, string(body))
	}

	var result generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Response, nil
}

// GenerateStream calls callback for every token as it arrives.
func (g *OllamaGenerator) GenerateStream(ctx context.Context, prompt string, callback func(token string)) error {
	reqBody := generateRequest{
		Model:  g.model,
		Prompt: prompt,
		Stream: true,
		Options: map[string]interface{}{
			"temperature": 0.3,
			"top_p":       0.9,
		},
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", g.baseURL+"/api/generate", bytes.NewReader(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama generate error: %s, body: %s", resp.Status, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk streamResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}
		if chunk.Response != "" {
			callback(chunk.Response)
		}
		if chunk.Done {
			break
		}
	}
	return scanner.Err()
}
