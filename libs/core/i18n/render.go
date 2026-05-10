package i18n

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cbroglie/mustache"
	"github.com/thescaffold/gox-packages-core/utils"
)

// RenderHelpers is the map passed as lambdas to the Mustache renderer.
// Each entry is a mustache.LambdaFunc (text string, render RenderFunc) (string, error).
var RenderHelpers = map[string]any{
	"xSanitizer": mustache.LambdaFunc(func(text string, render mustache.RenderFunc) (string, error) {
		s, _ := render(text)
		return xSanitizer(s), nil
	}),
	"xAlphanum": mustache.LambdaFunc(func(text string, render mustache.RenderFunc) (string, error) {
		s, _ := render(text)
		return xAlphanum(s), nil
	}),
	"xTime": mustache.LambdaFunc(func(text string, render mustache.RenderFunc) (string, error) {
		s, _ := render(text)
		return xTime(s), nil
	}),
	"xEnvValue": mustache.LambdaFunc(func(text string, render mustache.RenderFunc) (string, error) {
		s, _ := render(text)
		return xEnvValue(s), nil
	}),
	"xToBase64": mustache.LambdaFunc(func(text string, render mustache.RenderFunc) (string, error) {
		s, _ := render(text)
		return xToBase64(s), nil
	}),
	"xFromBase64": mustache.LambdaFunc(func(text string, render mustache.RenderFunc) (string, error) {
		s, _ := render(text)
		return xFromBase64(s), nil
	}),
	"xMajor": mustache.LambdaFunc(func(text string, render mustache.RenderFunc) (string, error) {
		s, _ := render(text)
		return xMajor(s), nil
	}),
	"xMinor": mustache.LambdaFunc(func(text string, render mustache.RenderFunc) (string, error) {
		s, _ := render(text)
		return xMinor(s), nil
	}),
	"xRandomSuffix": mustache.LambdaFunc(func(text string, render mustache.RenderFunc) (string, error) {
		_, _ = render(text)
		return xRandomSuffix(""), nil
	}),
}

// xSanitizer strips non-alphanumeric characters and lowercases the result.
func xSanitizer(text string) string {
	re := regexp.MustCompile(`[^A-Za-z0-9]`)
	result := strings.ToLower(re.ReplaceAllString(text, ""))
	// remove leading digits
	return regexp.MustCompile(`^[0-9]+`).ReplaceAllString(result, "")
}

// xAlphanum generates a random alphanumeric string of the given length (parsed from text).
func xAlphanum(text string) string {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n <= 0 {
		n = 7
	}
	return utils.Random(n)
}

// xTime formats a time string using the "02 Jan 2006" layout.
func xTime(text string) string {
	input := strings.TrimSpace(text)
	t, err := time.Parse(time.RFC3339, input)
	if err != nil {
		return input
	}
	return t.UTC().Format("02 Jan 2006")
}

// xEnvValue returns ASCII-only text with double quotes escaped.
func xEnvValue(text string) string {
	for _, r := range text {
		if r > 127 {
			return ""
		}
	}
	return strings.ReplaceAll(text, `"`, `\"`)
}

// xToBase64 base64-encodes the input text.
func xToBase64(text string) string {
	return base64.StdEncoding.EncodeToString([]byte(text))
}

// xFromBase64 base64-decodes the input text.
func xFromBase64(text string) string {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return text
	}
	return string(data)
}

// xMajor converts a minor-unit integer string to a major-unit decimal string.
func xMajor(text string) string {
	return utils.ToMajor(parseInt64(text), 2)
}

// xMinor converts a major-unit decimal string to a minor-unit integer string.
func xMinor(text string) string {
	v, err := utils.ToMinor(strings.TrimSpace(text), 2)
	if err != nil {
		return text
	}
	return strconv.FormatInt(v, 10)
}

// xRandomSuffix returns a 7-character random string (ignores input).
func xRandomSuffix(_ string) string {
	return utils.Random(7)
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}
