package utils

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var (
	emailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	// Nigerian: +2340XXXXXXXXX (14), 2340XXXXXXXXX (13), 0XXXXXXXXXX (11)
	phoneRe  = regexp.MustCompile(`^(\+234|234|0)[789][01]\d{8}$`)
	numRe    = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	digitsRe = regexp.MustCompile(`\d`)
	uuidRe   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
)

// IsEmail returns true if v is a valid email address.
func IsEmail(v string) bool {
	return emailRe.MatchString(strings.TrimSpace(v))
}

// IsPhone returns true if v is a valid Nigerian phone number.
func IsPhone(v string) bool {
	return phoneRe.MatchString(strings.TrimSpace(v))
}

// IsNumeric returns true if v is a numeric string (integer or decimal).
func IsNumeric(v string) bool {
	if v == "" {
		return false
	}
	return numRe.MatchString(strings.TrimSpace(v))
}

// IsNullOrUndefined returns true if v is nil.
// In Go there is no "undefined"; nil covers both TS null and undefined.
func IsNullOrUndefined(v any) bool {
	return v == nil
}

// IsName returns true if v contains no digits. Mirrors the TS @IsName decorator
// in validation.util.ts: a "name" must not contain a numeric character.
func IsName(v string) bool {
	return !digitsRe.MatchString(v)
}

// IsUUID returns true if v is a valid v1-v5 UUID.
func IsUUID(v string) bool {
	return uuidRe.MatchString(v)
}

// EnvFieldRule describes a validation rule for a single environment variable.
type EnvFieldRule struct {
	Name     string
	Required bool
	// Type is one of: "string", "int", "bool", "uuid". Empty string is treated as "string".
	Type string
	// In, when non-empty, restricts the value to this set.
	In []string
	// Length, when non-zero, requires the string to be exactly this many characters.
	Length int
	// Default is used when the env var is missing AND Required is false.
	Default string
}

// ValidateEnv validates env values against the given rules and returns a typed
// map (string→any) of accepted values, mirroring the TS envValidateFn behavior.
// On any failure it returns nil + a multi-line error explaining each issue.
func ValidateEnv(env map[string]string, rules []EnvFieldRule) (map[string]any, error) {
	out := map[string]any{}
	var errs []string

	for _, r := range rules {
		raw, present := env[r.Name]
		if !present || raw == "" {
			if r.Required && r.Default == "" {
				errs = append(errs, fmt.Sprintf("%s is required", r.Name))
				continue
			}
			raw = r.Default
		}
		if raw == "" {
			continue
		}

		if len(r.In) > 0 {
			ok := false
			for _, allowed := range r.In {
				if raw == allowed {
					ok = true
					break
				}
			}
			if !ok {
				errs = append(errs, fmt.Sprintf("%s must be one of %v", r.Name, r.In))
				continue
			}
		}

		if r.Length > 0 && len(raw) != r.Length {
			errs = append(errs, fmt.Sprintf("%s must be exactly %d characters long", r.Name, r.Length))
			continue
		}

		typ := strings.ToLower(r.Type)
		switch typ {
		case "", "string":
			out[r.Name] = raw
		case "int":
			n, err := strconv.Atoi(raw)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s must be an integer", r.Name))
				continue
			}
			out[r.Name] = n
		case "bool":
			b := raw == "true" || raw == "1" || raw == "on"
			out[r.Name] = b
		case "uuid":
			if !IsUUID(raw) {
				errs = append(errs, fmt.Sprintf("%s must be a valid UUID", r.Name))
				continue
			}
			out[r.Name] = raw
		default:
			out[r.Name] = raw
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("env validation: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

// envFieldsFromStruct extracts EnvFieldRule slice from a struct's tags.
// Tags supported: `env:"NAME"`, `env-required:"true"`, `env-default:"..."`,
// `env-in:"a,b,c"`, `env-length:"32"`, `env-type:"int|bool|uuid|string"`.
//
// This is a Go-idiomatic equivalent of the TS DefaultEnvironmentVariables DTO.
func envFieldsFromStruct(spec any) []EnvFieldRule {
	t := reflect.TypeOf(spec)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	rules := make([]EnvFieldRule, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := f.Tag.Get("env")
		if name == "" {
			continue
		}
		r := EnvFieldRule{
			Name:     name,
			Required: f.Tag.Get("env-required") == "true",
			Default:  f.Tag.Get("env-default"),
			Type:     f.Tag.Get("env-type"),
		}
		if length := f.Tag.Get("env-length"); length != "" {
			if n, err := strconv.Atoi(length); err == nil {
				r.Length = n
			}
		}
		if in := f.Tag.Get("env-in"); in != "" {
			r.In = strings.Split(in, ",")
		}
		rules = append(rules, r)
	}
	return rules
}

// ValidateEnvStruct is a convenience wrapper that derives EnvFieldRule from a
// struct's `env:"..."` tags and validates the env map against them.
func ValidateEnvStruct(env map[string]string, spec any) (map[string]any, error) {
	return ValidateEnv(env, envFieldsFromStruct(spec))
}
