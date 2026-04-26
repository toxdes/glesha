package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type RedactLevel int

const (
	RedactNone RedactLevel = iota
	RedactMask
	RedactFull
)

var redactFields map[string]RedactLevel

func init() {
	redactFields = make(map[string]RedactLevel)
	collectRedactTags(reflect.TypeOf(Config{}), "")
}

func collectRedactTags(typ reflect.Type, prefix string) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonName == "" {
			continue
		}
		path := jsonName
		if prefix != "" {
			path = prefix + "." + jsonName
		}
		switch field.Tag.Get("redact") {
		case "mask":
			redactFields[path] = RedactMask
		case "full":
			redactFields[path] = RedactFull
		}
		fieldType := field.Type
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct {
			collectRedactTags(fieldType, path)
		}
	}
}

func (r RedactLevel) Apply(val any) string {
	switch r {
	case RedactFull:
		return "<redacted>"
	case RedactMask:
		s := fmt.Sprintf("%v", val)
		if len(s) <= 3 {
			return strings.Repeat("*", len(s))
		}
		return s[:3] + strings.Repeat("*", len(s)-3)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func GetFieldRedactLevel(segments []string) RedactLevel {
	path := strings.Join(segments, ".")
	if level, ok := redactFields[path]; ok {
		return level
	}
	return RedactNone
}

func ToRedactedJSON(cfg *Config) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return "", err
	}
	redactMap(m, "")
	result, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func redactMap(m map[string]any, prefix string) {
	for key, val := range m {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		switch v := val.(type) {
		case map[string]any:
			redactMap(v, path)
		case json.Number:
			if level, ok := redactFields[path]; ok {
				m[key] = level.Apply(string(v))
			}
		default:
			if level, ok := redactFields[path]; ok {
				m[key] = level.Apply(val)
			}
		}
	}
}
