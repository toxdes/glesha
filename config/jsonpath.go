package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

type fieldEntry struct {
	index []int
}

var fieldRegistry map[string]fieldEntry

func init() {
	fieldRegistry = make(map[string]fieldEntry)
	buildFieldRegistry(reflect.TypeOf(Config{}), "", nil)
}

func buildFieldRegistry(typ reflect.Type, prefix string, index []int) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonName == "" || jsonName == "-" {
			continue
		}
		path := jsonName
		if prefix != "" {
			path = prefix + "." + jsonName
		}
		idx := append(append([]int{}, index...), i)
		fieldRegistry[path] = fieldEntry{index: idx}

		fieldType := field.Type
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct {
			buildFieldRegistry(fieldType, path, idx)
		}
	}
}

func ParsePath(path string) ([]string, error) {
	if !strings.HasPrefix(path, "$.") {
		return nil, fmt.Errorf("path must start with $.")
	}
	path = strings.TrimPrefix(path, "$.")
	if len(path) == 0 {
		return nil, fmt.Errorf("path is empty")
	}
	segments := strings.Split(path, ".")
	for _, s := range segments {
		if s == "" {
			return nil, fmt.Errorf("invalid path: empty segment")
		}
	}
	return segments, nil
}

func GetValueByPath(cfg *Config, path string) (any, error) {
	segments, err := ParsePath(path)
	if err != nil {
		return nil, err
	}

	p := strings.Join(segments, ".")
	entry, ok := fieldRegistry[p]
	if !ok {
		return nil, fmt.Errorf("unknown config key: %s", segments[len(segments)-1])
	}

	v := reflect.ValueOf(cfg)
	for i, idx := range entry.index {
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return nil, fmt.Errorf("%s config is nil", segments[i-1])
			}
			v = v.Elem()
		}
		v = v.Field(idx)
	}

	return v.Interface(), nil
}

func SetValueByPath(cfg *Config, path string, value string) error {
	segments, err := ParsePath(path)
	if err != nil {
		return err
	}

	p := strings.Join(segments, ".")
	entry, ok := fieldRegistry[p]
	if !ok {
		return fmt.Errorf("unknown config key: %s", segments[len(segments)-1])
	}

	typ := reflect.TypeOf(cfg).Elem()
	for i, idx := range entry.index {
		f := typ.Field(idx)
		if i == len(entry.index)-1 {
			parsedVal, err := parseFieldValue(f.Type, value)
			if err != nil {
				return err
			}
			v := reflect.ValueOf(cfg)
			for _, idx2 := range entry.index {
				if v.Kind() == reflect.Ptr {
					if v.IsNil() {
						v.Set(reflect.New(v.Type().Elem()))
					}
					v = v.Elem()
				}
				v = v.Field(idx2)
			}
			if !v.CanSet() {
				return fmt.Errorf("cannot set field: %s", path)
			}
			v.Set(reflect.ValueOf(parsedVal))
			return nil
		}
		ft := f.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		typ = ft
	}
	return nil
}

func parseFieldValue(typ reflect.Type, value string) (any, error) {
	switch typ {
	case reflect.TypeOf(AF_TARGZ):
		return ParseArchiveFormat(value)
	case reflect.TypeOf(PROVIDER_AWS):
		return ParseProvider(value)
	}

	switch typ.Kind() {
	case reflect.String:
		return value, nil
	case reflect.Bool:
		return parseBool(value)
	case reflect.Uint64:
		return parseUint64(value)
	default:
		return nil, fmt.Errorf("unsupported field type: %s", typ)
	}
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean value: %s", value)
}

func parseUint64(value string) (uint64, error) {
	v, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid uint64 value: %s", value)
	}
	return v, nil
}
