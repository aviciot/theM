package transform

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// init registers all built-in transform functions.
// Phase 1: LLM-era, JSON, String, Validation, Numeric.
// Phase 2 (future): Date, Encoding, Conditional.
func init() {
	// ── LLM-era ──────────────────────────────────────────────────────────────

	register(FunctionDef{
		Name:        "strip_fences",
		Category:    CategoryLLMEra,
		Description: "Removes markdown code fences (```json...```) that LLMs add despite instructions.",
		Args:        []ArgDef{},
		Examples: []Example{
			{In: "```json\n{\"a\":1}\n```", Out: `{"a":1}`},
			{In: "hello", Out: "hello"},
		},
	}, func(input string, _ map[string]string) (string, error) {
		s := strings.TrimSpace(input)
		// Remove opening fence: ```json, ```javascript, ```text, ``` (with optional language tag)
		s = regexp.MustCompile(`(?i)^` + "```" + `[a-z]*\s*\n?`).ReplaceAllString(s, "")
		// Remove closing fence
		s = regexp.MustCompile("\n?" + "```" + `\s*$`).ReplaceAllString(s, "")
		return strings.TrimSpace(s), nil
	})

	register(FunctionDef{
		Name:        "normalize_whitespace",
		Category:    CategoryLLMEra,
		Description: "Collapses multiple spaces/newlines into single spaces and trims.",
		Args:        []ArgDef{},
		Examples: []Example{
			{In: "  hello   world  ", Out: "hello world"},
			{In: "line1\n\n\nline2", Out: "line1 line2"},
		},
	}, func(input string, _ map[string]string) (string, error) {
		s := regexp.MustCompile(`[\s]+`).ReplaceAllString(input, " ")
		return strings.TrimSpace(s), nil
	})

	register(FunctionDef{
		Name:        "extract_code_block",
		Category:    CategoryLLMEra,
		Description: "Extracts the content of the first code block of a given language (default: any).",
		Args: []ArgDef{
			{Key: "lang", Description: "Language tag to match (e.g. json, python). Empty matches any.", Required: false, Default: ""},
		},
		Examples: []Example{
			{In: "Here is the result:\n```json\n{\"a\":1}\n```\nDone.", Args: map[string]string{"lang": "json"}, Out: `{"a":1}`},
		},
	}, func(input string, args map[string]string) (string, error) {
		lang := args["lang"]
		pattern := "```" + lang + `[\s\S]*?\n?([\s\S]*?)` + "```"
		re := regexp.MustCompile(`(?i)` + "```" + regexp.QuoteMeta(lang) + `\s*\n?([\s\S]*?)\n?` + "```")
		m := re.FindStringSubmatch(input)
		if len(m) < 2 {
			return "", fmt.Errorf("no code block found (lang=%q, pattern=%q)", lang, pattern)
		}
		return strings.TrimSpace(m[1]), nil
	})

	// ── JSON / Structure ──────────────────────────────────────────────────────

	register(FunctionDef{
		Name:        "parse_json",
		Category:    CategoryJSON,
		Description: "Parses a JSON string into a structured value. Output is the re-serialized JSON.",
		Args:        []ArgDef{},
		Examples: []Example{
			{In: `{"city":"Rome"}`, Out: `{"city":"Rome"}`},
		},
	}, func(input string, _ map[string]string) (string, error) {
		var v any
		if err := json.Unmarshal([]byte(input), &v); err != nil {
			return "", fmt.Errorf("parse_json: %w", err)
		}
		b, _ := json.Marshal(v)
		return string(b), nil
	})

	register(FunctionDef{
		Name:        "to_string",
		Category:    CategoryJSON,
		Description: "Converts any value to its JSON string representation.",
		Args:        []ArgDef{},
		Examples: []Example{
			{In: `{"a":1}`, Out: `{"a":1}`},
			{In: "hello", Out: "hello"},
		},
	}, func(input string, _ map[string]string) (string, error) {
		return input, nil
	})

	register(FunctionDef{
		Name:        "json_path",
		Category:    CategoryJSON,
		Description: "Extracts a value from a JSON string by dot-separated path (e.g. $.city1 or city1.name).",
		Args: []ArgDef{
			{Key: "path", Description: "Dot-separated JSON path, with or without leading $.", Required: true},
		},
		Examples: []Example{
			{In: `{"city":"Rome","lat":"41.9"}`, Args: map[string]string{"path": "$.city"}, Out: "Rome"},
		},
	}, func(input string, args map[string]string) (string, error) {
		path := strings.TrimPrefix(args["path"], "$.")
		if path == "" {
			return "", fmt.Errorf("json_path: path arg is required")
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(input), &v); err != nil {
			return "", fmt.Errorf("json_path: input is not a JSON object: %w", err)
		}
		parts := strings.Split(path, ".")
		var cur any = v
		for _, p := range parts {
			m, ok := cur.(map[string]any)
			if !ok {
				return "", fmt.Errorf("json_path: %q is not an object at segment %q", path, p)
			}
			cur, ok = m[p]
			if !ok {
				return "", fmt.Errorf("json_path: key %q not found", p)
			}
		}
		switch val := cur.(type) {
		case string:
			return val, nil
		default:
			b, _ := json.Marshal(val)
			return string(b), nil
		}
	})

	register(FunctionDef{
		Name:        "merge_json",
		Category:    CategoryJSON,
		Description: "Merges a JSON object literal (the 'patch' arg) into the input JSON object. Patch keys win on conflict.",
		Args: []ArgDef{
			{Key: "patch", Description: "JSON object string to merge in.", Required: true},
		},
		Examples: []Example{
			{In: `{"a":1}`, Args: map[string]string{"patch": `{"b":2}`}, Out: `{"a":1,"b":2}`},
		},
	}, func(input string, args map[string]string) (string, error) {
		var base, patch map[string]any
		if err := json.Unmarshal([]byte(input), &base); err != nil {
			return "", fmt.Errorf("merge_json: base is not a JSON object: %w", err)
		}
		if err := json.Unmarshal([]byte(args["patch"]), &patch); err != nil {
			return "", fmt.Errorf("merge_json: patch is not a JSON object: %w", err)
		}
		for k, v := range patch {
			base[k] = v
		}
		b, _ := json.Marshal(base)
		return string(b), nil
	})

	register(FunctionDef{
		Name:        "json_keys",
		Category:    CategoryJSON,
		Description: "Returns a JSON array of the top-level keys of a JSON object.",
		Args:        []ArgDef{},
		Examples: []Example{
			{In: `{"a":1,"b":2}`, Out: `["a","b"]`},
		},
	}, func(input string, _ map[string]string) (string, error) {
		var v map[string]any
		if err := json.Unmarshal([]byte(input), &v); err != nil {
			return "", fmt.Errorf("json_keys: %w", err)
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		b, _ := json.Marshal(keys)
		return string(b), nil
	})

	// ── String ────────────────────────────────────────────────────────────────

	register(FunctionDef{
		Name:        "trim",
		Category:    CategoryString,
		Description: "Removes leading and trailing whitespace.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "  hello  ", Out: "hello"}},
	}, func(input string, _ map[string]string) (string, error) {
		return strings.TrimSpace(input), nil
	})

	register(FunctionDef{
		Name:        "ltrim",
		Category:    CategoryString,
		Description: "Removes leading whitespace.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "  hello  ", Out: "hello  "}},
	}, func(input string, _ map[string]string) (string, error) {
		return strings.TrimLeft(input, " \t\n\r"), nil
	})

	register(FunctionDef{
		Name:        "rtrim",
		Category:    CategoryString,
		Description: "Removes trailing whitespace.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "  hello  ", Out: "  hello"}},
	}, func(input string, _ map[string]string) (string, error) {
		return strings.TrimRight(input, " \t\n\r"), nil
	})

	register(FunctionDef{
		Name:        "upper",
		Category:    CategoryString,
		Description: "Converts string to UPPER CASE.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "tel aviv", Out: "TEL AVIV"}},
	}, func(input string, _ map[string]string) (string, error) {
		return strings.ToUpper(input), nil
	})

	register(FunctionDef{
		Name:        "lower",
		Category:    CategoryString,
		Description: "Converts string to lower case.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "TEL AVIV", Out: "tel aviv"}},
	}, func(input string, _ map[string]string) (string, error) {
		return strings.ToLower(input), nil
	})

	register(FunctionDef{
		Name:        "replace",
		Category:    CategoryString,
		Description: "Replaces all occurrences of 'old' with 'new'.",
		Args: []ArgDef{
			{Key: "old", Description: "String to find.", Required: true},
			{Key: "new", Description: "Replacement string.", Required: true},
		},
		Examples: []Example{{In: "hello world", Args: map[string]string{"old": "world", "new": "planet"}, Out: "hello planet"}},
	}, func(input string, args map[string]string) (string, error) {
		return strings.ReplaceAll(input, args["old"], args["new"]), nil
	})

	register(FunctionDef{
		Name:        "substring",
		Category:    CategoryString,
		Description: "Extracts a substring by rune index. 'end' is exclusive; -1 means end of string.",
		Args: []ArgDef{
			{Key: "start", Description: "Start rune index (0-based).", Required: true},
			{Key: "end", Description: "End rune index, exclusive. -1 = end of string.", Required: false, Default: "-1"},
		},
		Examples: []Example{{In: "hello world", Args: map[string]string{"start": "6"}, Out: "world"}},
	}, func(input string, args map[string]string) (string, error) {
		runes := []rune(input)
		start, err := strconv.Atoi(args["start"])
		if err != nil {
			return "", fmt.Errorf("substring: start must be an integer")
		}
		end := len(runes)
		if e, ok := args["end"]; ok && e != "" && e != "-1" {
			end, err = strconv.Atoi(e)
			if err != nil {
				return "", fmt.Errorf("substring: end must be an integer")
			}
		}
		if start < 0 || start > len(runes) {
			return "", fmt.Errorf("substring: start %d out of range (len=%d)", start, len(runes))
		}
		if end < start || end > len(runes) {
			end = len(runes)
		}
		return string(runes[start:end]), nil
	})

	register(FunctionDef{
		Name:        "length",
		Category:    CategoryString,
		Description: "Returns the number of Unicode characters (runes) in the string.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "hello", Out: "5"}},
	}, func(input string, _ map[string]string) (string, error) {
		return strconv.Itoa(utf8.RuneCountInString(input)), nil
	})

	register(FunctionDef{
		Name:        "concat",
		Category:    CategoryString,
		Description: "Prepends 'prefix' and/or appends 'suffix' to the input.",
		Args: []ArgDef{
			{Key: "prefix", Description: "String to prepend.", Required: false, Default: ""},
			{Key: "suffix", Description: "String to append.", Required: false, Default: ""},
		},
		Examples: []Example{{In: "TEL AVIV", Args: map[string]string{"prefix": "City: "}, Out: "City: TEL AVIV"}},
	}, func(input string, args map[string]string) (string, error) {
		return args["prefix"] + input + args["suffix"], nil
	})

	register(FunctionDef{
		Name:        "split",
		Category:    CategoryString,
		Description: "Splits string by delimiter and returns a JSON array of parts.",
		Args: []ArgDef{
			{Key: "delimiter", Description: "Delimiter string.", Required: true},
		},
		Examples: []Example{{In: "a,b,c", Args: map[string]string{"delimiter": ","}, Out: `["a","b","c"]`}},
	}, func(input string, args map[string]string) (string, error) {
		parts := strings.Split(input, args["delimiter"])
		b, _ := json.Marshal(parts)
		return string(b), nil
	})

	register(FunctionDef{
		Name:        "join",
		Category:    CategoryString,
		Description: "Joins a JSON array of strings with a delimiter.",
		Args: []ArgDef{
			{Key: "delimiter", Description: "Delimiter to join with.", Required: true},
		},
		Examples: []Example{{In: `["a","b","c"]`, Args: map[string]string{"delimiter": ","}, Out: "a,b,c"}},
	}, func(input string, args map[string]string) (string, error) {
		var parts []string
		if err := json.Unmarshal([]byte(input), &parts); err != nil {
			return "", fmt.Errorf("join: input must be a JSON array of strings: %w", err)
		}
		return strings.Join(parts, args["delimiter"]), nil
	})

	register(FunctionDef{
		Name:        "regex_replace",
		Category:    CategoryString,
		Description: "Replaces all regex matches with 'replacement'. Supports capture group references ($1, $2).",
		Args: []ArgDef{
			{Key: "pattern", Description: "RE2 regular expression.", Required: true},
			{Key: "replacement", Description: "Replacement string.", Required: true},
		},
		Examples: []Example{{In: "foo123bar", Args: map[string]string{"pattern": `\d+`, "replacement": "_"}, Out: "foo_bar"}},
	}, func(input string, args map[string]string) (string, error) {
		re, err := regexp.Compile(args["pattern"])
		if err != nil {
			return "", fmt.Errorf("regex_replace: invalid pattern: %w", err)
		}
		return re.ReplaceAllString(input, args["replacement"]), nil
	})

	register(FunctionDef{
		Name:        "regex_extract",
		Category:    CategoryString,
		Description: "Returns the first match of 'pattern'. If 'group' is set, returns that capture group (1-based).",
		Args: []ArgDef{
			{Key: "pattern", Description: "RE2 regular expression.", Required: true},
			{Key: "group", Description: "Capture group index (1-based). 0 = full match.", Required: false, Default: "0"},
		},
		Examples: []Example{{In: "order-12345-done", Args: map[string]string{"pattern": `(\d+)`, "group": "1"}, Out: "12345"}},
	}, func(input string, args map[string]string) (string, error) {
		re, err := regexp.Compile(args["pattern"])
		if err != nil {
			return "", fmt.Errorf("regex_extract: invalid pattern: %w", err)
		}
		m := re.FindStringSubmatch(input)
		if m == nil {
			return "", fmt.Errorf("regex_extract: no match for pattern %q", args["pattern"])
		}
		idx := 0
		if g, ok := args["group"]; ok && g != "" {
			idx, err = strconv.Atoi(g)
			if err != nil || idx < 0 || idx >= len(m) {
				return "", fmt.Errorf("regex_extract: group %q out of range", g)
			}
		}
		return m[idx], nil
	})

	// ── Type / Validation ─────────────────────────────────────────────────────

	register(FunctionDef{
		Name:        "is_json",
		Category:    CategoryValidation,
		Description: "Returns 'true' if the input is valid JSON, 'false' otherwise.",
		Args:        []ArgDef{},
		Examples: []Example{
			{In: `{"a":1}`, Out: "true"},
			{In: "hello", Out: "false"},
		},
	}, func(input string, _ map[string]string) (string, error) {
		var v any
		if err := json.Unmarshal([]byte(input), &v); err != nil {
			return "false", nil
		}
		return "true", nil
	})

	register(FunctionDef{
		Name:        "is_empty",
		Category:    CategoryValidation,
		Description: "Returns 'true' if the input is empty or whitespace-only.",
		Args:        []ArgDef{},
		Examples: []Example{
			{In: "", Out: "true"},
			{In: "hello", Out: "false"},
		},
	}, func(input string, _ map[string]string) (string, error) {
		if strings.TrimSpace(input) == "" {
			return "true", nil
		}
		return "false", nil
	})

	register(FunctionDef{
		Name:        "is_number",
		Category:    CategoryValidation,
		Description: "Returns 'true' if the input can be parsed as a number.",
		Args:        []ArgDef{},
		Examples: []Example{
			{In: "3.14", Out: "true"},
			{In: "hello", Out: "false"},
		},
	}, func(input string, _ map[string]string) (string, error) {
		_, err := strconv.ParseFloat(strings.TrimSpace(input), 64)
		if err != nil {
			return "false", nil
		}
		return "true", nil
	})

	register(FunctionDef{
		Name:        "coalesce",
		Category:    CategoryValidation,
		Description: "Returns the input if non-empty, otherwise returns the 'default' arg.",
		Args: []ArgDef{
			{Key: "default", Description: "Fallback value if input is empty.", Required: true},
		},
		Examples: []Example{
			{In: "", Args: map[string]string{"default": "fallback"}, Out: "fallback"},
			{In: "value", Args: map[string]string{"default": "fallback"}, Out: "value"},
		},
	}, func(input string, args map[string]string) (string, error) {
		if strings.TrimSpace(input) == "" {
			return args["default"], nil
		}
		return input, nil
	})

	register(FunctionDef{
		Name:        "default_if_empty",
		Category:    CategoryValidation,
		Description: "Alias for coalesce. Returns the input if non-empty, otherwise the 'default' arg.",
		Args: []ArgDef{
			{Key: "default", Description: "Fallback value.", Required: true},
		},
		Examples: []Example{
			{In: "", Args: map[string]string{"default": "N/A"}, Out: "N/A"},
		},
	}, func(input string, args map[string]string) (string, error) {
		if strings.TrimSpace(input) == "" {
			return args["default"], nil
		}
		return input, nil
	})

	register(FunctionDef{
		Name:        "assert_json",
		Category:    CategoryValidation,
		Description: "Passes the input through unchanged if it is valid JSON. Errors the step if not.",
		Args:        []ArgDef{},
		Examples: []Example{
			{In: `{"a":1}`, Out: `{"a":1}`},
		},
	}, func(input string, _ map[string]string) (string, error) {
		var v any
		if err := json.Unmarshal([]byte(input), &v); err != nil {
			return "", fmt.Errorf("assert_json: input is not valid JSON: %w", err)
		}
		return input, nil
	})

	register(FunctionDef{
		Name:        "type_of",
		Category:    CategoryValidation,
		Description: "Returns the JSON type of the input: string, number, boolean, array, object, or null.",
		Args:        []ArgDef{},
		Examples: []Example{
			{In: `{"a":1}`, Out: "object"},
			{In: `[1,2]`, Out: "array"},
			{In: "42", Out: "number"},
		},
	}, func(input string, _ map[string]string) (string, error) {
		s := strings.TrimSpace(input)
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return "string", nil // unparseable → treat as raw string
		}
		switch v.(type) {
		case map[string]any:
			return "object", nil
		case []any:
			return "array", nil
		case float64:
			return "number", nil
		case bool:
			return "boolean", nil
		case nil:
			return "null", nil
		default:
			return "string", nil
		}
	})

	// ── Numeric ───────────────────────────────────────────────────────────────

	register(FunctionDef{
		Name:        "to_number",
		Category:    CategoryNumeric,
		Description: "Parses a string as a float64 and returns its canonical string representation.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "  3.14  ", Out: "3.14"}},
	}, func(input string, _ map[string]string) (string, error) {
		f, err := strconv.ParseFloat(strings.TrimSpace(input), 64)
		if err != nil {
			return "", fmt.Errorf("to_number: %q is not a number", input)
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	})

	register(FunctionDef{
		Name:        "to_int",
		Category:    CategoryNumeric,
		Description: "Parses a string as an integer (truncates decimals).",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "3.9", Out: "3"}},
	}, func(input string, _ map[string]string) (string, error) {
		f, err := strconv.ParseFloat(strings.TrimSpace(input), 64)
		if err != nil {
			return "", fmt.Errorf("to_int: %q is not a number", input)
		}
		return strconv.FormatInt(int64(f), 10), nil
	})

	register(FunctionDef{
		Name:        "round",
		Category:    CategoryNumeric,
		Description: "Rounds to 'decimals' decimal places (default 0).",
		Args: []ArgDef{
			{Key: "decimals", Description: "Number of decimal places.", Required: false, Default: "0"},
		},
		Examples: []Example{{In: "3.14159", Args: map[string]string{"decimals": "2"}, Out: "3.14"}},
	}, func(input string, args map[string]string) (string, error) {
		f, err := strconv.ParseFloat(strings.TrimSpace(input), 64)
		if err != nil {
			return "", fmt.Errorf("round: %q is not a number", input)
		}
		dec := 0
		if d, ok := args["decimals"]; ok && d != "" {
			dec, err = strconv.Atoi(d)
			if err != nil {
				return "", fmt.Errorf("round: decimals must be an integer")
			}
		}
		pow := math.Pow(10, float64(dec))
		rounded := math.Round(f*pow) / pow
		return strconv.FormatFloat(rounded, 'f', dec, 64), nil
	})

	register(FunctionDef{
		Name:        "abs",
		Category:    CategoryNumeric,
		Description: "Returns the absolute value of a number.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "-5.5", Out: "5.5"}},
	}, func(input string, _ map[string]string) (string, error) {
		f, err := strconv.ParseFloat(strings.TrimSpace(input), 64)
		if err != nil {
			return "", fmt.Errorf("abs: %q is not a number", input)
		}
		return strconv.FormatFloat(math.Abs(f), 'f', -1, 64), nil
	})

	register(FunctionDef{
		Name:        "add",
		Category:    CategoryNumeric,
		Description: "Adds 'value' to the input number.",
		Args: []ArgDef{
			{Key: "value", Description: "Number to add.", Required: true},
		},
		Examples: []Example{{In: "10", Args: map[string]string{"value": "5"}, Out: "15"}},
	}, func(input string, args map[string]string) (string, error) {
		a, err := strconv.ParseFloat(strings.TrimSpace(input), 64)
		if err != nil {
			return "", fmt.Errorf("add: input %q is not a number", input)
		}
		b, err := strconv.ParseFloat(args["value"], 64)
		if err != nil {
			return "", fmt.Errorf("add: value %q is not a number", args["value"])
		}
		return strconv.FormatFloat(a+b, 'f', -1, 64), nil
	})

	register(FunctionDef{
		Name:        "multiply",
		Category:    CategoryNumeric,
		Description: "Multiplies the input number by 'value'.",
		Args: []ArgDef{
			{Key: "value", Description: "Multiplier.", Required: true},
		},
		Examples: []Example{{In: "5", Args: map[string]string{"value": "5"}, Out: "25"}},
	}, func(input string, args map[string]string) (string, error) {
		a, err := strconv.ParseFloat(strings.TrimSpace(input), 64)
		if err != nil {
			return "", fmt.Errorf("multiply: input %q is not a number", input)
		}
		b, err := strconv.ParseFloat(args["value"], 64)
		if err != nil {
			return "", fmt.Errorf("multiply: value %q is not a number", args["value"])
		}
		return strconv.FormatFloat(a*b, 'f', -1, 64), nil
	})

	// ── Encoding (Phase 2 — stubs register now so the catalog is complete) ────

	register(FunctionDef{
		Name:        "base64_encode",
		Category:    CategoryEncoding,
		Description: "Encodes input as base64.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "hello", Out: "aGVsbG8="}},
	}, func(input string, _ map[string]string) (string, error) {
		return base64.StdEncoding.EncodeToString([]byte(input)), nil
	})

	register(FunctionDef{
		Name:        "base64_decode",
		Category:    CategoryEncoding,
		Description: "Decodes a base64 string.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "aGVsbG8=", Out: "hello"}},
	}, func(input string, _ map[string]string) (string, error) {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(input))
		if err != nil {
			return "", fmt.Errorf("base64_decode: %w", err)
		}
		return string(b), nil
	})

	register(FunctionDef{
		Name:        "url_encode",
		Category:    CategoryEncoding,
		Description: "URL-encodes the input string.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "hello world", Out: "hello+world"}},
	}, func(input string, _ map[string]string) (string, error) {
		return url.QueryEscape(input), nil
	})

	register(FunctionDef{
		Name:        "url_decode",
		Category:    CategoryEncoding,
		Description: "URL-decodes the input string.",
		Args:        []ArgDef{},
		Examples:    []Example{{In: "hello+world", Out: "hello world"}},
	}, func(input string, _ map[string]string) (string, error) {
		s, err := url.QueryUnescape(input)
		if err != nil {
			return "", fmt.Errorf("url_decode: %w", err)
		}
		return s, nil
	})
}
