package identity

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/config"
)

const contentDirKey = "content_dir"

// excludedSettings lists the settings keys that do not take part in the
// identity. An entry excludes its whole subtree. Every other field of
// config.Settings, including one added later, is hashed.
var excludedSettings = []string{
	"transport",
	"host",
	"port",
	"auth",
	"ui",
	"server_mode",
	"shared_server.connect_wait",
	"shared_server.linger",
}

func isExcludedSetting(key string) bool {
	for _, excluded := range excludedSettings {
		if key == excluded || strings.HasPrefix(key, excluded+".") {
			return true
		}
	}
	return false
}

// settingRecords flattens s into one record per non-excluded leaf, keyed by
// its configuration key and sorted, so field order in the struct does not
// matter. The content dir is replaced by its resolved form.
func settingRecords(s *config.Settings, resolvedContentDir string) ([]record, error) {
	var records []record
	if err := appendSettingRecords(&records, reflect.ValueOf(*s), ""); err != nil {
		return nil, err
	}
	for i := range records {
		if records[i].key == contentDirKey {
			records[i].value = resolvedContentDir
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].key < records[j].key })
	return records, nil
}

func appendSettingRecords(records *[]record, v reflect.Value, prefix string) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		key := prefix + settingKey(t.Field(i))
		if isExcludedSetting(key) {
			continue
		}
		field := v.Field(i)
		if field.Kind() == reflect.Struct {
			if err := appendSettingRecords(records, field, key+"."); err != nil {
				return err
			}
			continue
		}
		value, err := encodeSettingValue(field)
		if err != nil {
			return fmt.Errorf("setting %s: %w", key, err)
		}
		*records = append(*records, record{key, value})
	}
	return nil
}

func settingKey(f reflect.StructField) string {
	if name := f.Tag.Get("mapstructure"); name != "" {
		return name
	}
	return f.Name
}

// encodeSettingValue rejects kinds it has no canonical form for, so a new
// field of such a kind fails loudly instead of being dropped from the hash.
func encodeSettingValue(v reflect.Value) (string, error) {
	switch v.Kind() {
	case reflect.String:
		return v.String(), nil
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64), nil
	case reflect.Slice:
		return encodeSlice(v)
	default:
		return "", fmt.Errorf("unsupported kind %s", v.Kind())
	}
}

func encodeSlice(v reflect.Value) (string, error) {
	var w recordWriter
	w.field(strconv.Itoa(v.Len()))
	for i := 0; i < v.Len(); i++ {
		element, err := encodeSettingValue(v.Index(i))
		if err != nil {
			return "", err
		}
		w.field(element)
	}
	return string(w.buf), nil
}
