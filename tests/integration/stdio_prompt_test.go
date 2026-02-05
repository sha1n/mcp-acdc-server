package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
)

func TestPromptIntegration(t *testing.T) {
	// 1. Prepare content using testkit
	promptContent := `---
name: test-prompt
description: A test prompt
arguments:
  - name: arg1
    description: First argument
    required: true
---
Hello {{.arg1}}`
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Prompts: map[string]string{
			"test-prompt.md": promptContent,
		},
	})

	// 2. Start ACDC Service with stdio transport
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{
		Transport: "stdio",
	})

	service := testkit.NewACDCService("acdc-prompt", flags)
	env := testkit.NewTestEnv(service)

	props, err := env.Start()
	assert.NoError(t, err)
	defer func() { _ = env.Stop() }()

	stdin := props["acdc.stdin"].(io.Writer)
	stdout := props["acdc.stdout"].(io.Reader)

	scanner := bufio.NewScanner(stdout)

	sendRequest := func(req interface{}) {
		reqBytes, _ := json.Marshal(req)
		if _, err := fmt.Fprintf(stdin, "%s\n", reqBytes); err != nil {
			t.Fatalf("Failed to write to stdin: %v", err)
		}
	}

	readResponse := func(expectedID int) map[string]interface{} {
		for scanner.Scan() {
			line := scanner.Text()
			var resp map[string]interface{}
			if err := json.Unmarshal([]byte(line), &resp); err == nil {
				if id, ok := resp["id"].(float64); ok && int(id) == expectedID {
					return resp
				}
			}
		}
		return nil
	}

	// 3. Initialize
	sendRequest(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]string{
				"name":    "test-client",
				"version": "1.0",
			},
		},
	})

	initResp := readResponse(1)
	assert.NotNil(t, initResp)

	// 4. Send initialized notification
	sendRequest(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	// 5. List Prompts
	sendRequest(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "prompts/list",
	})

	listResp := readResponse(10)
	assert.NotNil(t, listResp)

	listResult := listResp["result"].(map[string]interface{})
	promptsList := listResult["prompts"].([]interface{})
	assert.Len(t, promptsList, 1)
	assert.Equal(t, "docs:test-prompt", promptsList[0].(map[string]interface{})["name"])

	// 6. Get Prompt
	sendRequest(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "prompts/get",
		"params": map[string]interface{}{
			"name": "docs:test-prompt",
			"arguments": map[string]string{
				"arg1": "ACDC",
			},
		},
	})

	getResp := readResponse(2)
	assert.NotNil(t, getResp)

	getResult := getResp["result"].(map[string]interface{})
	messages := getResult["messages"].([]interface{})
	assert.Len(t, messages, 1)

	msg := messages[0].(map[string]interface{})
	content := msg["content"].(map[string]interface{})
	assert.Equal(t, "Hello ACDC", content["text"])
}
