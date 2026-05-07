package inventory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// ValidationResult is the JSON-friendly result returned to the frontend.
type ValidationResult struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

// ValidateYAML parses the wizard's draft YAML and validates it against
// inventory.schema.json located in the supplied content directory.
func ValidateYAML(yamlText, contentDir string) (ValidationResult, error) {
	var doc any
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return ValidationResult{Valid: false, Errors: []string{"yaml: " + err.Error()}}, nil
	}
	doc = normalizeYAML(doc)

	schemaPath := filepath.Join(contentDir, "schema", "inventory.schema.json")
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("read schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	var schemaDoc any
	if err := json.Unmarshal(raw, &schemaDoc); err != nil {
		return ValidationResult{}, fmt.Errorf("parse schema: %w", err)
	}
	if err := c.AddResource(schemaPath, schemaDoc); err != nil {
		return ValidationResult{}, err
	}
	sch, err := c.Compile(schemaPath)
	if err != nil {
		return ValidationResult{}, err
	}
	if err := sch.Validate(doc); err != nil {
		var ve *jsonschema.ValidationError
		errs := []string{err.Error()}
		if asErr := errAs(err, &ve); asErr {
			errs = nil
			for _, c := range ve.Causes {
				errs = append(errs, fmt.Sprintf("%s: %s", c.InstanceLocation, c.Error()))
			}
		}
		return ValidationResult{Valid: false, Errors: errs}, nil
	}
	return ValidationResult{Valid: true}, nil
}

func errAs(err error, target any) bool {
	type aser interface{ As(any) bool }
	if a, ok := err.(aser); ok {
		return a.As(target)
	}
	return false
}

// normalizeYAML converts map[interface{}]interface{} (which yaml.v3 emits in
// some shapes) into map[string]interface{} so the JSON Schema validator can
// process it.
func normalizeYAML(v any) any {
	switch x := v.(type) {
	case map[any]any:
		m := map[string]any{}
		for k, vv := range x {
			m[fmt.Sprint(k)] = normalizeYAML(vv)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normalizeYAML(vv)
		}
		return out
	default:
		return v
	}
}
