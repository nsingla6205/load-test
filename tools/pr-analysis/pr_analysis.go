package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	analysisURL            = "https://llm-proxy-api.ai.openeng.netapp.com/v1/chat/completions"
	analysisModel          = "o1-mini"
	maxErrorResponseLength = 2048
)

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Write analysis result to a markdown file
func writeToMarkdown(filePath string, content string) error {
	return os.WriteFile(filePath, []byte(content), 0644)
}

func readPrompt() string {
	return `Please conduct a **focused and concise code review** of the provided code changes (diff). Only analyze the changes themselves, using the surrounding lines for context where necessary. Avoid speculative feedback or false positives.

**Instructions:**

1. **Change Analysis:**
   - **Before:** In 1-2 sentences, describe the state of the code before the changes
   - **After:** In 1-2 sentences, summarize the modifications made.
   - **Rationale:** In 1-2 sentences, explain the purpose of the changes (e.g., bug fix, feature addition, optimization). If unclear, note this.

2. **Code Review:**
   - **Quality:** Briefly assess the quality of the changes (e.g., "well-implemented," "introduces potential issues").
   - **Readability:** Comment on the clarity and maintainability of the changes (e.g., "clear and concise," "naming could be improved").
   - **Potential Issues:** Highlight any obvious bugs, edge cases, or side effects.
   - **Best Practices:** Note adherence to or deviations from coding standards and best practices.
   - **Suggestions:** Provide 1-2 actionable suggestions for improvement, if applicable.

3. **Comments:**
   - Add **specific, concise comments** directly on the diff (if possible) to highlight key areas of concern or commendation.
   - Avoid generalities—focus on actionable insights.

**Deliverables:**
- A **short, focused analysis** (3-5 sentences total) summarizing the changes and their impact.

**Rules:**
- **Strictly focus on the diff.** Do not analyze surrounding code unless directly relevant.
- Avoid speculative feedback. Only raise concerns if directly supported by the changes.
- Keep the analysis **crisp and concise**. Avoid unnecessary elaboration.`
}

// Call OpenAI API to analyze diff and return the response content
func analyzeDiff(apiKey string, diffContent string, userName string, prompt string) (string, error) {
	// Construct request payload
	requestBody := map[string]interface{}{
		"model": analysisModel,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt + "\n\nDiff:\n" + diffContent,
			},
		},
		"user": userName,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, analysisURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	// Modify http.Client to skip TLS verification in GitHub Actions
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 2 * time.Minute,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Error closing response body: %v", err)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return parseAnalysisResponse(resp.StatusCode, resp.Status, body)
}

func parseAnalysisResponse(statusCode int, status string, body []byte) (string, error) {
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("LLM proxy returned %s: %s", status, responseSnippet(body))
	}

	var response chatCompletionResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("failed to unmarshal LLM proxy response: %w", err)
	}
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("LLM proxy response contained no choices: %s", responseSnippet(body))
	}

	content := response.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("LLM proxy response contained empty analysis content")
	}

	return content, nil
}

func responseSnippet(body []byte) string {
	if len(body) <= maxErrorResponseLength {
		return string(body)
	}
	return string(body[:maxErrorResponseLength]) + "...[truncated]"
}

func main() {
	if len(os.Args) < 3 {
		log.Fatalf("Usage: %s <diff_path> <output_path>", os.Args[0])
	}

	diffPath := os.Args[1]
	outputPath := os.Args[2]
	// promptPath := "cmd/pr-analysis/instructions.txt"
	userName := os.Getenv("OPENAI_USER")
	if userName == "" {
		log.Fatalf("USER_NAME environment variable is not set")
	}

	// Load the diff content from the file
	diffContent, err := os.ReadFile(diffPath)
	if err != nil {
		log.Fatalf("Failed to read diff file: %v", err)
	}

	apiKey := os.Getenv("OPENAI_KEY")
	if apiKey == "" {
		log.Fatalf("OPENAI_KEY environment variable is not set")
	}

	// Load the prompt from the file
	// prompt, err := readPromptFromFile(promptPath)
	// if err != nil {
	// log.Fatalf("Failed to read prompt file: %v", err)
	// }

	prompt := readPrompt()

	// Analyze the diff using OpenAI API
	analysis, err := analyzeDiff(apiKey, string(diffContent), userName, prompt)
	if err != nil {
		log.Fatalf("Failed to analyze diff: %v", err)
	}

	// Write the analysis result to the markdown file
	if err := writeToMarkdown(outputPath, analysis); err != nil {
		log.Fatalf("Failed to write analysis to markdown: %v", err)
	}
	log.Printf("Analysis written to: %s\n", outputPath)
}
