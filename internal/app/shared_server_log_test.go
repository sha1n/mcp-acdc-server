package app

import (
	"bytes"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sharedServerLogTestTime = regexp.MustCompile(`^time=\S+ `)

func sharedServerLogTestSettings(auth config.AuthSettings) *config.Settings {
	return &config.Settings{
		ContentDir: "/content",
		Transport:  "stdio",
		Host:       "0.0.0.0",
		Port:       8080,
		Search: config.SearchSettings{
			MaxResults: 10, InMemory: true, KeywordsBoost: 3, HeadingBoost: 2.5,
			PathBoost: 2, TitleBoost: 2, ContentBoost: 1, ResultMode: "references",
		},
		Auth:         auth,
		UI:           config.UISettings{Enabled: true, MaxResults: 20},
		ServerMode:   config.ServerModeShared,
		SharedServer: config.SharedServerSettings{ConnectWait: 5 * time.Second, Linger: time.Minute},
	}
}

func sharedServerLogTestLines(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		require.Regexp(t, sharedServerLogTestTime, line)
		lines = append(lines, sharedServerLogTestTime.ReplaceAllString(line, ""))
	}
	return lines
}

func TestLogSharedServerStartup_OmitsEveryIdentityExcludedSetting(t *testing.T) {
	excluded := []string{"transport", "host", "port", "server_mode", "shared_server.", "auth", "ui.", "Web UI"}
	auths := map[string]config.AuthSettings{
		"basic":  {Type: config.AuthTypeBasic, Basic: config.BasicAuthSettings{Username: "secret-user", Password: "secret-pass"}},
		"apikey": {Type: config.AuthTypeAPIKey, APIKeys: []string{"k1", "k2"}},
		"none":   {Type: "none"},
	}

	for name, auth := range auths {
		t.Run(name, func(t *testing.T) {
			settings := sharedServerLogTestSettings(auth)
			var full, shared bytes.Buffer
			config.LogWithLogger(settings, slog.New(slog.NewTextHandler(&full, nil)))

			logSharedServerStartup(slog.New(slog.NewTextHandler(&shared, nil)), settings, "1.2.3")

			sharedLines := sharedServerLogTestLines(t, &shared)
			require.NotEmpty(t, sharedLines)
			assert.Equal(t,
				`level=INFO msg="Starting MCP Acdc shared server" version=1.2.3 pid=`+strconv.Itoa(os.Getpid()),
				sharedLines[0], "the startup entry carries the PID")

			var wantDump []string
			for _, line := range sharedServerLogTestLines(t, &full) {
				if !sharedServerLogTestMentionsAny(line, excluded) {
					wantDump = append(wantDump, line)
				}
			}
			assert.Equal(t, wantDump, sharedLines[1:], "the dump is today's dump minus the excluded settings, in the same format")
			assert.Contains(t, sharedLines[1:], `level=INFO msg="Config: content_dir" value=/content`)
			for _, line := range sharedLines {
				assert.False(t, sharedServerLogTestMentionsAny(line, excluded), "excluded setting logged: %s", line)
				assert.NotContains(t, line, "secret-user")
				assert.NotContains(t, line, "count=")
			}
		})
	}
}

func sharedServerLogTestMentionsAny(line string, words []string) bool {
	msg := line[strings.Index(line, "msg="):]
	for _, w := range words {
		if strings.Contains(msg, "Config: "+w) || strings.Contains(msg, w+" requires") {
			return true
		}
	}
	return false
}
