package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
)

func TestResourceReadIntegration(t *testing.T) {
	// 1. Prepare content using testkit
	resourceContent := "---\nname: Bert Benchmarking Tool\ndescription: A tool\n---\nHere is the content of the tool."
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"tools/bert-benchmarking.md": resourceContent,
		},
	})

	// 2. Start ACDC Service with stdio transport
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{
		Transport: "stdio",
	})

	service := testkit.NewACDCService("acdc-resource", flags)
	env := testkit.NewTestEnv(service)

	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start env: %v", err)
	}
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
	if initResp == nil {
		t.Fatal("Failed to get initialize response")
	}

	// 4. Send initialized notification
	sendRequest(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	// 5. List Resources to verify exact URI string
	targetURI := "acdc://tools/bert-benchmarking"
	sendRequest(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "resources/list",
	})

	listResp := readResponse(10)
	if listResp == nil {
		t.Fatal("Failed to get resources/list response")
	}

	listResult, ok := listResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("resources/list result is not a map")
	}

	resourcesList, ok := listResult["resources"].([]interface{})
	if !ok {
		t.Fatalf("resources list is invalid")
	}

	found := false
	for _, r := range resourcesList {
		resMap := r.(map[string]interface{})
		uri := resMap["uri"].(string)
		t.Logf("Advertised Resource URI: %s", uri)
		if uri == targetURI {
			found = true
		}
	}
	if !found {
		t.Errorf("Target URI %s not found in resources/list", targetURI)
	}

	// 6. Read Resource
	sendRequest(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "resources/read",
		"params": map[string]string{
			"uri": targetURI,
		},
	})

	readResp := readResponse(2)
	if readResp == nil {
		t.Fatal("Failed to get read resource response")
	}

	result, ok := readResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("Response result is not a map: %v", readResp)
	}

	contents, ok := result["contents"].([]interface{})
	if !ok || len(contents) == 0 {
		errorInfo := readResp["error"]
		t.Fatalf("Failed to get contents. Error in response: %v", errorInfo)
	}

	firstContent := contents[0].(map[string]interface{})
	text := firstContent["text"].(string)

	if text != "Here is the content of the tool." {
		t.Errorf("Unexpected content: %s", text)
	} else {
		t.Logf("Successfully read resource content: %s", text)
	}
}
