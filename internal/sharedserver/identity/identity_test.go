package identity

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/require"
)

// excludedSettingPaths is the spec section 3.1 item 3 exclusion list, written
// independently of the implementation so the two must agree.
var excludedSettingPaths = []string{
	"transport",
	"host",
	"port",
	"auth",
	"ui",
	"server_mode",
	"shared_server.connect_wait",
	"shared_server.linger",
}

func baseSettings(t *testing.T) *config.Settings {
	t.Helper()
	return &config.Settings{
		ContentDir: t.TempDir(),
		Transport:  "stdio",
		Host:       "0.0.0.0",
		Port:       8080,
		Scheme:     "acdc",
		CrossRef:   false,
		Search: config.SearchSettings{
			MaxResults:    10,
			InMemory:      true,
			KeywordsBoost: config.DefaultKeywordsBoost,
			HeadingBoost:  config.DefaultHeadingBoost,
			TitleBoost:    config.DefaultTitleBoost,
			PathBoost:     config.DefaultPathBoost,
			ContentBoost:  config.DefaultContentBoost,
			ResultMode:    config.SearchResultModeReferences,
		},
		Auth: config.AuthSettings{
			Type:    config.AuthTypeBasic,
			Basic:   config.BasicAuthSettings{Username: "user-c0ffee", Password: "secret-p4ssw0rd"},
			APIKeys: []string{"key-deadbeef"},
		},
		UI:         config.UISettings{Enabled: false, MaxResults: config.DefaultUIMaxResults},
		ServerMode: config.ServerModeShared,
		SharedServer: config.SharedServerSettings{
			ConnectWait: config.DefaultSharedServerConnectWait,
			Linger:      config.DefaultSharedServerLinger,
		},
	}
}

func stampedBinary() Binary {
	return Binary{Version: "1.2.3", Build: "abc123", Executable: "/nonexistent/acdc-mcp"}
}

func mustCompute(t *testing.T, s *config.Settings, bin Binary) string {
	t.Helper()
	id, err := Compute(s, bin)
	require.NoError(t, err)
	return id
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
}

type settingLeaf struct {
	path  string
	index []int
}

func settingLeaves(t *testing.T, typ reflect.Type, prefix string, index []int) []settingLeaf {
	t.Helper()
	var leaves []settingLeaf
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := field.Tag.Get("mapstructure")
		if name == "" {
			name = field.Name
		}
		path := prefix + name
		fieldIndex := append(append([]int{}, index...), i)
		if field.Type.Kind() == reflect.Struct {
			leaves = append(leaves, settingLeaves(t, field.Type, path+".", fieldIndex)...)
			continue
		}
		leaves = append(leaves, settingLeaf{path: path, index: fieldIndex})
	}
	return leaves
}

// mutate changes a leaf to a different value of its type. A kind it does not
// know fails the test, so a new Settings field of a new kind cannot slip past
// the guard unmutated.
func mutate(t *testing.T, s *config.Settings, leaf settingLeaf) {
	t.Helper()
	v := reflect.ValueOf(s).Elem().FieldByIndex(leaf.index)
	if leaf.path == "content_dir" {
		v.SetString(t.TempDir())
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(v.String() + "-changed")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 0.5)
	case reflect.Slice:
		require.Equal(t, reflect.String, v.Type().Elem().Kind(), "mutate: unsupported slice of %s at %s", v.Type().Elem(), leaf.path)
		v.Set(reflect.Append(v, reflect.ValueOf("added").Convert(v.Type().Elem())))
	default:
		t.Fatalf("mutate: unsupported kind %s at %s; extend the guard", v.Kind(), leaf.path)
	}
}

func isExcluded(path string) bool {
	for _, excluded := range excludedSettingPaths {
		if path == excluded || strings.HasPrefix(path, excluded+".") {
			return true
		}
	}
	return false
}

func TestCompute_OutputIs32LowercaseHex(t *testing.T) {
	id := mustCompute(t, baseSettings(t), stampedBinary())
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{32}$`), id)
}

func TestCompute_Deterministic(t *testing.T) {
	s := baseSettings(t)
	require.Equal(t, mustCompute(t, s, stampedBinary()), mustCompute(t, s, stampedBinary()))
}

func TestCompute_DoesNotModifySettings(t *testing.T) {
	s := baseSettings(t)
	link := filepath.Join(t.TempDir(), "link")
	symlinkOrSkip(t, s.ContentDir, link)
	s.ContentDir = link
	before := *s
	before.Auth.APIKeys = append([]string{}, s.Auth.APIKeys...)

	mustCompute(t, s, stampedBinary())

	require.Equal(t, before, *s)
}

// TestCompute_EverySettingIsHashedUnlessExcluded is the exclude-list guard: a
// Settings field added later is mutated here automatically and must change the
// identity unless it is on the spec's exclusion list.
func TestCompute_EverySettingIsHashedUnlessExcluded(t *testing.T) {
	leaves := settingLeaves(t, reflect.TypeOf(config.Settings{}), "", nil)
	require.NotEmpty(t, leaves)

	for _, leaf := range leaves {
		t.Run(leaf.path, func(t *testing.T) {
			s := baseSettings(t)
			before := mustCompute(t, s, stampedBinary())
			mutate(t, s, leaf)
			after := mustCompute(t, s, stampedBinary())

			if isExcluded(leaf.path) {
				require.Equal(t, before, after, "excluded setting %s changed the identity", leaf.path)
			} else {
				require.NotEqual(t, before, after, "setting %s does not take part in the identity", leaf.path)
			}
		})
	}
}

func TestCompute_ExcludedSettingsLeaveIdentityUnchanged(t *testing.T) {
	cases := map[string]func(*config.Settings){
		"auth password":     func(s *config.Settings) { s.Auth.Basic.Password = "other" },
		"auth username":     func(s *config.Settings) { s.Auth.Basic.Username = "other" },
		"auth api keys":     func(s *config.Settings) { s.Auth.APIKeys = []string{"k1", "k2"} },
		"auth type":         func(s *config.Settings) { s.Auth.Type = config.AuthTypeAPIKey },
		"ui enabled":        func(s *config.Settings) { s.UI.Enabled = true },
		"ui max results":    func(s *config.Settings) { s.UI.MaxResults = 99 },
		"transport":         func(s *config.Settings) { s.Transport = "sse" },
		"host":              func(s *config.Settings) { s.Host = "127.0.0.1" },
		"port":              func(s *config.Settings) { s.Port = 9999 },
		"server mode":       func(s *config.Settings) { s.ServerMode = config.ServerModeSession },
		"connect wait":      func(s *config.Settings) { s.SharedServer.ConnectWait = time.Minute },
		"linger":            func(s *config.Settings) { s.SharedServer.Linger = time.Hour },
		"auth subtree zero": func(s *config.Settings) { s.Auth = config.AuthSettings{} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := baseSettings(t)
			before := mustCompute(t, s, stampedBinary())
			change(s)
			require.Equal(t, before, mustCompute(t, s, stampedBinary()))
		})
	}
}

func TestCompute_IncludedSettingsChangeIdentity(t *testing.T) {
	cases := map[string]func(*config.Settings){
		"search in memory":   func(s *config.Settings) { s.Search.InMemory = false },
		"search max results": func(s *config.Settings) { s.Search.MaxResults = 11 },
		"search result mode": func(s *config.Settings) { s.Search.ResultMode = config.SearchResultModeContent },
		"keywords boost":     func(s *config.Settings) { s.Search.KeywordsBoost = 0 },
		"scheme":             func(s *config.Settings) { s.Scheme = "docs" },
		"cross ref":          func(s *config.Settings) { s.CrossRef = true },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s := baseSettings(t)
			before := mustCompute(t, s, stampedBinary())
			change(s)
			require.NotEqual(t, before, mustCompute(t, s, stampedBinary()))
		})
	}
}

func TestCanonicalInput_ExcludesCredentials(t *testing.T) {
	s := baseSettings(t)
	input, err := canonicalInput(s, stampedBinary())
	require.NoError(t, err)

	for _, credential := range []string{s.Auth.Basic.Password, s.Auth.Basic.Username, s.Auth.APIKeys[0]} {
		require.False(t, bytes.Contains(input, []byte(credential)), "credential %q entered the hash input", credential)
	}
}

func TestCompute_ContentDirSymlinkEqualsResolvedPath(t *testing.T) {
	s := baseSettings(t)
	resolved := mustCompute(t, s, stampedBinary())

	link := filepath.Join(t.TempDir(), "link")
	symlinkOrSkip(t, s.ContentDir, link)
	s.ContentDir = link

	require.Equal(t, resolved, mustCompute(t, s, stampedBinary()))
}

func TestCompute_RelativeContentDirEqualsAbsolutePath(t *testing.T) {
	s := baseSettings(t)
	absolute := mustCompute(t, s, stampedBinary())

	t.Chdir(s.ContentDir)
	s.ContentDir = "."

	require.Equal(t, absolute, mustCompute(t, s, stampedBinary()))
}

// TestCompute_ContentDirCaseIsNotFolded holds on case-insensitive file systems
// too, where both spellings name one directory. It is skipped on Windows,
// where filepath.EvalSymlinks rewrites each component to its on-disk case and
// where no launch computes an identity (spec section 12).
func TestCompute_ContentDirCaseIsNotFolded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("filepath.EvalSymlinks normalises case on Windows")
	}
	parent := t.TempDir()
	lower := filepath.Join(parent, "content")
	upper := filepath.Join(parent, "CONTENT")
	require.NoError(t, os.MkdirAll(lower, 0o755))
	require.NoError(t, os.MkdirAll(upper, 0o755))

	s := baseSettings(t)
	s.ContentDir = lower
	lowerID := mustCompute(t, s, stampedBinary())
	s.ContentDir = upper

	require.NotEqual(t, lowerID, mustCompute(t, s, stampedBinary()))
}

func TestCompute_StampedBuildUsesVersionAndBuild(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "acdc-mcp")
	writeFile(t, exe, "binary-one")
	bin := Binary{Version: "1.2.3", Build: "abc123", Executable: exe}
	s := baseSettings(t)
	base := mustCompute(t, s, bin)

	writeFile(t, exe, "binary-two")
	require.Equal(t, base, mustCompute(t, s, bin), "executable bytes changed a stamped identity")

	missing := bin
	missing.Executable = filepath.Join(t.TempDir(), "missing")
	require.Equal(t, base, mustCompute(t, s, missing), "executable path changed a stamped identity")

	otherVersion := bin
	otherVersion.Version = "1.2.4"
	require.NotEqual(t, base, mustCompute(t, s, otherVersion))

	otherBuild := bin
	otherBuild.Build = "def456"
	require.NotEqual(t, base, mustCompute(t, s, otherBuild))
}

func TestCompute_UnstampedBuildUsesExecutableHash(t *testing.T) {
	cases := []struct {
		name      string
		bin       Binary
		unstamped Binary
	}{
		{
			name:      "dev and unknown",
			bin:       Binary{Version: "dev", Build: "unknown"},
			unstamped: Binary{Version: "1.0.0", Build: "unknown"},
		},
		{
			name:      "stamped version, unknown build",
			bin:       Binary{Version: "1.2.3", Build: "unknown"},
			unstamped: Binary{Version: "9.9.9", Build: "unknown"},
		},
		{
			name:      "dev version, stamped build",
			bin:       Binary{Version: "dev", Build: "abc123"},
			unstamped: Binary{Version: "dev", Build: "def456"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "acdc-mcp")
			writeFile(t, exe, "binary-one")
			s := baseSettings(t)
			bin := tc.bin
			bin.Executable = exe
			base := mustCompute(t, s, bin)

			other := tc.unstamped
			other.Executable = exe
			require.Equal(t, base, mustCompute(t, s, other), "Version/Build changed an unstamped identity")

			copied := filepath.Join(dir, "copy")
			writeFile(t, copied, "binary-one")
			bin.Executable = copied
			require.Equal(t, base, mustCompute(t, s, bin), "executable path changed an unstamped identity")

			writeFile(t, copied, "binary-two")
			require.NotEqual(t, base, mustCompute(t, s, bin), "executable bytes did not change an unstamped identity")
		})
	}
}

func TestCompute_StampedAndUnstampedDiffer(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "acdc-mcp")
	writeFile(t, exe, "1.2.3abc123")
	s := baseSettings(t)

	stamped := mustCompute(t, s, Binary{Version: "1.2.3", Build: "abc123", Executable: exe})
	unstamped := mustCompute(t, s, Binary{Version: "dev", Build: "unknown", Executable: exe})

	require.NotEqual(t, stamped, unstamped)
}

func TestCompute_StartupFixedContent(t *testing.T) {
	cases := map[string]struct {
		before func(t *testing.T, root string)
		change func(t *testing.T, root string)
		same   bool
	}{
		"manifest absent vs empty": {
			change: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, ".acdc", "config.yaml"), "") },
		},
		"manifest byte change": {
			before: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, ".acdc", "config.yaml"), "a: 1\n") },
			change: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, ".acdc", "config.yaml"), "a: 2\n") },
		},
		"prompt added": {
			change: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, ".acdc", "prompts", "p.md"), "x") },
		},
		"prompt added in subdirectory": {
			change: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, ".acdc", "prompts", "sub", "p.md"), "x")
			},
		},
		"empty prompt added": {
			change: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, ".acdc", "prompts", "p.md"), "") },
		},
		"prompt removed": {
			before: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, ".acdc", "prompts", "p.md"), "x") },
			change: func(t *testing.T, root string) {
				require.NoError(t, os.Remove(filepath.Join(root, ".acdc", "prompts", "p.md")))
			},
		},
		"prompt renamed": {
			before: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, ".acdc", "prompts", "p.md"), "x") },
			change: func(t *testing.T, root string) {
				prompts := filepath.Join(root, ".acdc", "prompts")
				require.NoError(t, os.Rename(filepath.Join(prompts, "p.md"), filepath.Join(prompts, "q.md")))
			},
		},
		"prompt byte change": {
			before: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, ".acdc", "prompts", "p.md"), "x") },
			change: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, ".acdc", "prompts", "p.md"), "y") },
		},
		"bytes moved between prompts": {
			before: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, ".acdc", "prompts", "a.md"), "xy")
				writeFile(t, filepath.Join(root, ".acdc", "prompts", "b.md"), "z")
			},
			change: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, ".acdc", "prompts", "a.md"), "x")
				writeFile(t, filepath.Join(root, ".acdc", "prompts", "b.md"), "yz")
			},
		},
		"non-md file under prompts": {
			change: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, ".acdc", "prompts", "p.txt"), "x") },
			same:   true,
		},
		"upper-case MD extension under prompts": {
			change: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, ".acdc", "prompts", "p.MD"), "x") },
			same:   true,
		},
		"directory named like a prompt": {
			change: func(t *testing.T, root string) {
				require.NoError(t, os.MkdirAll(filepath.Join(root, ".acdc", "prompts", "d.md"), 0o755))
			},
			same: true,
		},
		"document outside .acdc": {
			before: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, "doc.md"), "x") },
			change: func(t *testing.T, root string) { writeFile(t, filepath.Join(root, "doc.md"), "y") },
			same:   true,
		},
		"resource under .acdc": {
			change: func(t *testing.T, root string) {
				writeFile(t, filepath.Join(root, ".acdc", "resources", "r.md"), "x")
			},
			same: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := baseSettings(t)
			if tc.before != nil {
				tc.before(t, s.ContentDir)
			}
			before := mustCompute(t, s, stampedBinary())
			tc.change(t, s.ContentDir)
			after := mustCompute(t, s, stampedBinary())
			if tc.same {
				require.Equal(t, before, after)
			} else {
				require.NotEqual(t, before, after)
			}
		})
	}
}

// skipIfPermissionBitsIgnored skips where mode 000 does not deny reading:
// for root, and on Windows, where chmod only toggles the read-only attribute.
func skipIfPermissionBitsIgnored(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not remove read access on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not restrict root")
	}
}

// symlinkOrSkip skips on Windows when creating a symlink needs a privilege
// the test process lacks; elsewhere a failure is a test failure.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err != nil && runtime.GOOS == "windows" {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	require.NoError(t, err)
}

func TestCompute_UncomputableIdentityReturnsError(t *testing.T) {
	cases := map[string]func(t *testing.T, s *config.Settings, bin *Binary){
		"content dir does not exist": func(t *testing.T, s *config.Settings, _ *Binary) {
			s.ContentDir = filepath.Join(t.TempDir(), "missing")
		},
		"content dir through dangling symlink": func(t *testing.T, s *config.Settings, _ *Binary) {
			link := filepath.Join(t.TempDir(), "link")
			symlinkOrSkip(t, filepath.Join(t.TempDir(), "missing"), link)
			s.ContentDir = link
		},
		"unstamped executable missing": func(t *testing.T, _ *config.Settings, bin *Binary) {
			*bin = Binary{Version: "dev", Build: "unknown", Executable: filepath.Join(t.TempDir(), "missing")}
		},
		"unstamped executable path empty": func(t *testing.T, _ *config.Settings, bin *Binary) {
			*bin = Binary{Version: "dev", Build: "unknown"}
		},
		"unstamped executable unreadable": func(t *testing.T, _ *config.Settings, bin *Binary) {
			skipIfPermissionBitsIgnored(t)
			exe := filepath.Join(t.TempDir(), "acdc-mcp")
			writeFile(t, exe, "x")
			require.NoError(t, os.Chmod(exe, 0o000))
			*bin = Binary{Version: "dev", Build: "unknown", Executable: exe}
		},
		"manifest exists but is a directory": func(t *testing.T, s *config.Settings, _ *Binary) {
			require.NoError(t, os.MkdirAll(filepath.Join(s.ContentDir, ".acdc", "config.yaml"), 0o755))
		},
		"manifest exists but is unreadable": func(t *testing.T, s *config.Settings, _ *Binary) {
			skipIfPermissionBitsIgnored(t)
			manifest := filepath.Join(s.ContentDir, ".acdc", "config.yaml")
			writeFile(t, manifest, "a: 1\n")
			require.NoError(t, os.Chmod(manifest, 0o000))
		},
		"prompts directory unreadable": func(t *testing.T, s *config.Settings, _ *Binary) {
			skipIfPermissionBitsIgnored(t)
			prompts := filepath.Join(s.ContentDir, ".acdc", "prompts")
			writeFile(t, filepath.Join(prompts, "p.md"), "x")
			require.NoError(t, os.Chmod(prompts, 0o000))
			t.Cleanup(func() { _ = os.Chmod(prompts, 0o755) })
		},
		"prompts subdirectory unreadable": func(t *testing.T, s *config.Settings, _ *Binary) {
			skipIfPermissionBitsIgnored(t)
			sub := filepath.Join(s.ContentDir, ".acdc", "prompts", "sub")
			writeFile(t, filepath.Join(sub, "p.md"), "x")
			require.NoError(t, os.Chmod(sub, 0o000))
			t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })
		},
		"prompt file unreadable": func(t *testing.T, s *config.Settings, _ *Binary) {
			skipIfPermissionBitsIgnored(t)
			prompt := filepath.Join(s.ContentDir, ".acdc", "prompts", "p.md")
			writeFile(t, prompt, "x")
			require.NoError(t, os.Chmod(prompt, 0o000))
		},
		"prompt is a dangling symlink": func(t *testing.T, s *config.Settings, _ *Binary) {
			prompts := filepath.Join(s.ContentDir, ".acdc", "prompts")
			require.NoError(t, os.MkdirAll(prompts, 0o755))
			symlinkOrSkip(t, filepath.Join(prompts, "missing"), filepath.Join(prompts, "p.md"))
		},
		"prompt is a symlink to a directory": func(t *testing.T, s *config.Settings, _ *Binary) {
			prompts := filepath.Join(s.ContentDir, ".acdc", "prompts")
			require.NoError(t, os.MkdirAll(prompts, 0o755))
			symlinkOrSkip(t, t.TempDir(), filepath.Join(prompts, "p.md"))
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			s := baseSettings(t)
			bin := stampedBinary()
			setup(t, s, &bin)

			id, err := Compute(s, bin)

			require.Error(t, err)
			require.Empty(t, id)
		})
	}
}

func TestCompute_PromptsPathAbsentIsNotAnError(t *testing.T) {
	s := baseSettings(t)
	require.NoError(t, os.MkdirAll(filepath.Join(s.ContentDir, ".acdc"), 0o755))
	_, err := Compute(s, stampedBinary())
	require.NoError(t, err)
}

func TestCompute_UnstampedExecutableIsADirectoryReturnsError(t *testing.T) {
	_, err := Compute(baseSettings(t), Binary{Version: "dev", Build: "unknown", Executable: t.TempDir()})
	require.Error(t, err)
}

// Kinds no hashed Settings field uses today must still encode canonically, so
// a field of that kind added later is hashed rather than dropped.
func TestEncodeSettingValue_Kinds(t *testing.T) {
	cases := map[string]struct {
		value any
		want  string
	}{
		"string":   {"x", "x"},
		"bool":     {true, "true"},
		"int":      {-3, "-3"},
		"duration": {5 * time.Second, "5000000000"},
		"uint":     {uint16(7), "7"},
		"float64":  {1.25, "1.25"},
		"float32":  {float32(0.5), "0.5"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := encodeSettingValue(reflect.ValueOf(tc.value))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestEncodeSettingValue_SlicesAreUnambiguous(t *testing.T) {
	encode := func(v any) string {
		t.Helper()
		got, err := encodeSettingValue(reflect.ValueOf(v))
		require.NoError(t, err)
		return got
	}
	require.NotEqual(t, encode([]string{"a,b"}), encode([]string{"a", "b"}))
	require.NotEqual(t, encode([]string{}), encode([]string{""}))
	require.NotEqual(t, encode([]int{1, 2}), encode([]int{2, 1}))
}

func TestEncodeSettingValue_UnsupportedKindIsAnError(t *testing.T) {
	for name, value := range map[string]any{
		"map":              map[string]string{"a": "b"},
		"pointer":          new(int),
		"slice of maps":    []map[string]int{{"a": 1}},
		"interface in map": map[string]any{},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := encodeSettingValue(reflect.ValueOf(value))
			require.Error(t, err)
		})
	}
}

func TestAppendSettingRecords_FieldOfUnsupportedKindIsAnError(t *testing.T) {
	type nested struct {
		Labels map[string]string `mapstructure:"labels"`
	}
	type settings struct {
		Name   string `mapstructure:"name"`
		Nested nested `mapstructure:"nested"`
	}
	var records []record
	err := appendSettingRecords(&records, reflect.ValueOf(settings{}), "")
	require.ErrorContains(t, err, "nested.labels")
}

func TestAppendSettingRecords_UntaggedFieldUsesFieldName(t *testing.T) {
	type settings struct {
		Plain  string
		Tagged string `mapstructure:"tagged"`
	}
	var records []record
	require.NoError(t, appendSettingRecords(&records, reflect.ValueOf(settings{Plain: "p", Tagged: "t"}), ""))
	require.Equal(t, []record{{"Plain", "p"}, {"tagged", "t"}}, records)
}

// A dangling manifest symlink is what the server itself reads as no manifest,
// so the identity treats it as absent; the target appearing changes it.
func TestCompute_DanglingManifestSymlinkEqualsAbsentManifest(t *testing.T) {
	s := baseSettings(t)
	absent := mustCompute(t, s, stampedBinary())

	acdc := filepath.Join(s.ContentDir, ".acdc")
	require.NoError(t, os.MkdirAll(acdc, 0o755))
	target := filepath.Join(t.TempDir(), "config.yaml")
	symlinkOrSkip(t, target, filepath.Join(acdc, "config.yaml"))
	require.Equal(t, absent, mustCompute(t, s, stampedBinary()))

	writeFile(t, target, "")
	require.NotEqual(t, absent, mustCompute(t, s, stampedBinary()))
}
