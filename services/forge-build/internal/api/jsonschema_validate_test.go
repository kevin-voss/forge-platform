package api_test

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// validateJSONSchema is a minimal draft-2020-12 subset sufficient for forge.schema.json.
func validateJSONSchema(schema map[string]any, instance any) error {
	return validateNode("", schema, instance)
}

func validateNode(path string, schema map[string]any, instance any) error {
	if t, ok := schema["type"].(string); ok {
		if err := checkType(path, t, instance); err != nil {
			return err
		}
	}
	if req, ok := schema["required"].([]any); ok {
		obj, ok := instance.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object for required checks", loc(path))
		}
		for _, r := range req {
			key, _ := r.(string)
			if _, ok := obj[key]; !ok {
				return fmt.Errorf("%s: missing required property %q", loc(path), key)
			}
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		obj, _ := instance.(map[string]any)
		if obj == nil {
			return nil
		}
		if addl, ok := schema["additionalProperties"]; ok {
			if b, isBool := addl.(bool); isBool && !b {
				for k := range obj {
					if _, ok := props[k]; !ok {
						return fmt.Errorf("%s: unexpected property %q", loc(path), k)
					}
				}
			}
		}
		for name, sub := range props {
			subSchema, ok := sub.(map[string]any)
			if !ok {
				continue
			}
			val, exists := obj[name]
			if !exists {
				continue
			}
			child := name
			if path != "" {
				child = path + "." + name
			}
			if err := validateNode(child, subSchema, val); err != nil {
				return err
			}
		}
	}
	if pattern, ok := schema["pattern"].(string); ok {
		s, ok := instance.(string)
		if !ok {
			return fmt.Errorf("%s: expected string for pattern", loc(path))
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("schema pattern: %w", err)
		}
		if !re.MatchString(s) {
			return fmt.Errorf("%s: value %q does not match pattern %q", loc(path), s, pattern)
		}
	}
	if minLen, ok := asInt(schema["minLength"]); ok {
		s, _ := instance.(string)
		if len(s) < minLen {
			return fmt.Errorf("%s: string shorter than minLength %d", loc(path), minLen)
		}
	}
	if min, ok := asFloat(schema["minimum"]); ok {
		n, ok := asFloat(instance)
		if !ok {
			return fmt.Errorf("%s: expected number for minimum", loc(path))
		}
		if n < min {
			return fmt.Errorf("%s: %v < minimum %v", loc(path), n, min)
		}
	}
	if max, ok := asFloat(schema["maximum"]); ok {
		n, ok := asFloat(instance)
		if !ok {
			return fmt.Errorf("%s: expected number for maximum", loc(path))
		}
		if n > max {
			return fmt.Errorf("%s: %v > maximum %v", loc(path), n, max)
		}
	}
	return nil
}

func checkType(path, want string, instance any) error {
	ok := false
	switch want {
	case "object":
		_, ok = instance.(map[string]any)
	case "string":
		_, ok = instance.(string)
	case "integer":
		switch v := instance.(type) {
		case int:
			ok = true
		case int64:
			ok = true
		case float64:
			ok = v == math.Trunc(v)
		}
	case "number":
		_, ok = asFloat(instance)
	case "boolean":
		_, ok = instance.(bool)
	case "array":
		_, ok = instance.([]any)
	}
	if !ok {
		return fmt.Errorf("%s: expected type %s, got %T", loc(path), want, instance)
	}
	return nil
}

func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeYAML(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalizeYAML(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeYAML(val)
		}
		return out
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	default:
		return v
	}
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func asInt(v any) (int, bool) {
	f, ok := asFloat(v)
	if !ok || f != math.Trunc(f) {
		return 0, false
	}
	return int(f), true
}

func loc(path string) string {
	if path == "" {
		return "$"
	}
	return "$." + path
}
