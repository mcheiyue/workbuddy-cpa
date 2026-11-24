package wbexecutor

import (
	"regexp"
	"strconv"
	"strings"
)

// normalizeToolSchemaPatterns rewrites only patterns inside tools[].function.parameters schemas.
func normalizeToolSchemaPatterns(payload map[string]any) {
	tools, ok := payload["tools"].([]any)
	if !ok {
		return
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		function, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		if parameters, ok := function["parameters"].(map[string]any); ok {
			normalizeSchemaPatterns(parameters)
		}
	}
}

func normalizeSchemaPatterns(schema map[string]any) {
	if pattern, ok := schema["pattern"].(string); ok {
		schema["pattern"] = normalizeSchemaPattern(pattern)
	}
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		normalizeSchemaMap(schema[key])
	}
	if dependencies, ok := schema["dependencies"].(map[string]any); ok {
		for _, value := range dependencies {
			normalizeSchemaPatternsValue(value)
		}
	}
	for _, key := range []string{"items", "additionalItems", "additionalProperties", "contains", "propertyNames", "not", "if", "then", "else", "unevaluatedItems", "unevaluatedProperties"} {
		normalizeSchemaPatternsValue(schema[key])
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if schemas, ok := schema[key].([]any); ok {
			for _, child := range schemas {
				normalizeSchemaPatternsValue(child)
			}
		}
	}
}

func normalizeSchemaMap(value any) {
	if schemas, ok := value.(map[string]any); ok {
		for _, child := range schemas {
			normalizeSchemaPatternsValue(child)
		}
	}
}

func normalizeSchemaPatternsValue(value any) {
	switch schema := value.(type) {
	case map[string]any:
		normalizeSchemaPatterns(schema)
	case []any:
		for _, child := range schema {
			normalizeSchemaPatternsValue(child)
		}
	}
}

// normalizeSchemaPattern supports Unicode code-point patterns over valid UTF-8 input.
// Surrogate pairs inside character classes stay byte-for-byte intact because merging
// them would change the class's set semantics.
func normalizeSchemaPattern(pattern string) string {
	var out strings.Builder
	out.Grow(len(pattern))
	converted := false
	inClass := false
	for i := 0; i < len(pattern); {
		if pattern[i] == '\\' {
			if i+1 >= len(pattern) {
				out.WriteByte(pattern[i])
				i++
				continue
			}
			if pattern[i+1] != 'u' {
				out.WriteString(pattern[i : i+2])
				i += 2
				continue
			}
			end, value, valid := parseUnicodeEscape(pattern, i)
			if !valid {
				return pattern
			}
			if value >= 0xD800 && value <= 0xDBFF {
				nextEnd, nextValue, nextValid := parseUnicodeEscape(pattern, end)
				if nextValid && nextValue >= 0xDC00 && nextValue <= 0xDFFF {
					if inClass {
						return pattern
					}
					value = (value-0xD800)<<10 + (nextValue - 0xDC00) + 0x10000
					end = nextEnd
				} else if !inClass {
					return pattern
				}
			} else if value >= 0xDC00 && value <= 0xDFFF && !inClass {
				return pattern
			}
			out.WriteString(`\x{`)
			hex := strings.ToUpper(strconv.FormatUint(value, 16))
			if len(hex) < 4 {
				hex = strings.Repeat("0", 4-len(hex)) + hex
			}
			out.WriteString(hex)
			out.WriteByte('}')
			converted = true
			i = end
			continue
		}
		if pattern[i] == '[' && !inClass {
			inClass = true
		} else if pattern[i] == ']' && inClass {
			inClass = false
		}
		out.WriteByte(pattern[i])
		i++
	}
	if !converted {
		return pattern
	}
	candidate := out.String()
	if _, err := regexp.Compile(candidate); err != nil {
		return pattern
	}
	return candidate
}

func parseUnicodeEscape(pattern string, start int) (int, uint64, bool) {
	if start+2 > len(pattern) || pattern[start] != '\\' || pattern[start+1] != 'u' {
		return start, 0, false
	}
	if start+2 < len(pattern) && pattern[start+2] == '{' {
		close := strings.IndexByte(pattern[start+3:], '}')
		if close < 0 {
			return start, 0, false
		}
		end := start + 3 + close
		digits := pattern[start+3 : end]
		if len(digits) < 1 || len(digits) > 6 {
			return start, 0, false
		}
		value, err := strconv.ParseUint(digits, 16, 32)
		if err != nil || value > 0x10FFFF {
			return start, 0, false
		}
		return end + 1, value, true
	}
	if start+6 > len(pattern) {
		return start, 0, false
	}
	value, err := strconv.ParseUint(pattern[start+2:start+6], 16, 16)
	if err != nil {
		return start, 0, false
	}
	return start + 6, value, true
}
