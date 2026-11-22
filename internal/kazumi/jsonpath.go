package kazumi

import (
	"fmt"
	"strconv"
	"strings"
)

type jpStepKind int

const (
	jpField jpStepKind = iota
	jpIndex
	jpWildcard
)

type jpStep struct {
	kind  jpStepKind
	field string
	index int
}

// ParseJSONPath parses Kazumi's RestrictedJsonPath subset:
// $, .field, [*], [index], ['field'], ["field"].
func ParseJSONPath(expr string) ([]jpStep, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" || expr[0] != '$' {
		return nil, fmt.Errorf("JSONPath 必须以 $ 开头: %s", expr)
	}
	var steps []jpStep
	i := 1
	for i < len(expr) {
		switch expr[i] {
		case '.':
			i++
			start := i
			for i < len(expr) && isJPIdentChar(expr[i]) {
				i++
			}
			if i == start {
				return nil, fmt.Errorf("不支持的 JSONPath: %s", expr)
			}
			steps = append(steps, jpStep{kind: jpField, field: expr[start:i]})
		case '[':
			end, err := findJPBracketEnd(expr, i)
			if err != nil {
				return nil, err
			}
			inner := strings.TrimSpace(expr[i+1 : end])
			if inner == "*" {
				steps = append(steps, jpStep{kind: jpWildcard})
			} else if len(inner) >= 2 && ((inner[0] == '\'' && inner[len(inner)-1] == '\'') || (inner[0] == '"' && inner[len(inner)-1] == '"')) {
				steps = append(steps, jpStep{kind: jpField, field: unescapeJPString(inner[1 : len(inner)-1])})
			} else if idx, err := strconv.Atoi(inner); err == nil {
				steps = append(steps, jpStep{kind: jpIndex, index: idx})
			} else {
				return nil, fmt.Errorf("不支持的 JSONPath 索引 %q: %s", inner, expr)
			}
			i = end + 1
		default:
			return nil, fmt.Errorf("不支持的 JSONPath: %s", expr)
		}
	}
	return steps, nil
}

func isJPIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '$' || c == '-'
}

func findJPBracketEnd(expr string, start int) (int, error) {
	var quote byte
	escaped := false
	for i := start + 1; i < len(expr); i++ {
		c := expr[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == ']' {
			return i, nil
		}
	}
	return -1, fmt.Errorf("JSONPath 缺少 ]: %s", expr)
}

func unescapeJPString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ReadJSONPath evaluates a RestrictedJsonPath expression and returns all matched values.
func ReadJSONPath(doc any, expr string) ([]any, error) {
	steps, err := ParseJSONPath(expr)
	if err != nil {
		return nil, err
	}
	cur := []any{doc}
	for _, step := range steps {
		next := make([]any, 0, len(cur))
		for _, node := range cur {
			switch step.kind {
			case jpField:
				if m, ok := node.(map[string]any); ok {
					if v, exists := m[step.field]; exists {
						next = append(next, v)
					}
				}
			case jpIndex:
				if arr, ok := node.([]any); ok {
					idx := step.index
					if idx < 0 {
						idx = len(arr) + idx
					}
					if idx >= 0 && idx < len(arr) {
						next = append(next, arr[idx])
					}
				}
			case jpWildcard:
				switch v := node.(type) {
				case []any:
					next = append(next, v...)
				case map[string]any:
					for _, val := range v {
						next = append(next, val)
					}
				}
			}
		}
		cur = next
		if len(cur) == 0 {
			break
		}
	}
	return cur, nil
}

// ReadJSONPathFirst evaluates expr and returns the first matched value or nil.
func ReadJSONPathFirst(doc any, expr string) (any, error) {
	vals, err := ReadJSONPath(doc, expr)
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, nil
	}
	return vals[0], nil
}
