package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
)

// TestURISchemeIntegration verifies that the server uses the configured URI scheme in resource URIs,
// and that resources can be read back using those URIs.
func TestURISchemeIntegration(t *testing.T) {
	resourceContent := "---\nname: Scheme Test Resource\ndescription: A resource for URI scheme testing\n---\nScheme test content."

	tests := []struct {
		name           string
		scheme         string
		expectedScheme string
	}{
		{
			name:           "default scheme",
			scheme:         "",
			expectedScheme: "acdc",
		},
		{
			name:           "custom scheme",
			scheme:         "myorg",
			expectedScheme: "myorg",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
				Resources: map[string]string{
					"tools/test-tool.md": resourceContent,
				},
			})

			flagOpts := &testkit.FlagOptions{
				Transport: "stdio",
			}
			if tc.scheme != "" {
				flagOpts.Scheme = tc.scheme
			}

			flags := testkit.NewTestFlags(t, contentDir, flagOpts)
			service := testkit.NewACDCService("acdc-scheme-test", flags)
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

			// Initialize
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

			sendRequest(map[string]interface{}{
				"jsonrpc": "2.0",
				"method":  "notifications/initialized",
			})

			// List resources and verify URI scheme
			expectedURI := fmt.Sprintf("%s://tools/test-tool", tc.expectedScheme)

			sendRequest(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      2,
				"method":  "resources/list",
			})

			listResp := readResponse(2)
			if listResp == nil {
				t.Fatal("Failed to get resources/list response")
			}

			listResult, ok := listResp["result"].(map[string]interface{})
			if !ok {
				t.Fatalf("resources/list result is not a map: %v", listResp)
			}

			resourcesList, ok := listResult["resources"].([]interface{})
			if !ok {
				t.Fatalf("resources list is invalid: %v", listResult)
			}

			found := false
			for _, r := range resourcesList {
				resMap := r.(map[string]interface{})
				uri := resMap["uri"].(string)
				t.Logf("Resource URI: %s", uri)
				if uri == expectedURI {
					found = true
				}
			}
			if !found {
				t.Fatalf("Expected URI %s not found in resources/list", expectedURI)
			}

			// Read resource using the scheme-prefixed URI
			sendRequest(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      3,
				"method":  "resources/read",
				"params": map[string]string{
					"uri": expectedURI,
				},
			})

			readResp := readResponse(3)
			if readResp == nil {
				t.Fatal("Failed to get resources/read response")
			}

			result, ok := readResp["result"].(map[string]interface{})
			if !ok {
				t.Fatalf("Response result is not a map: %v", readResp)
			}

			contents, ok := result["contents"].([]interface{})
			if !ok || len(contents) == 0 {
				t.Fatalf("Failed to get contents. Response: %v", readResp)
			}

			firstContent := contents[0].(map[string]interface{})
			text := firstContent["text"].(string)

			if text != "Scheme test content." {
				t.Errorf("Unexpected content: %s", text)
			}
		})
	}
}
