package values

import (
	"strings"
	"testing"
)

const testSchema = `{
  "type": "object",
  "properties": {
    "image": {
      "type": "object",
      "properties": {
        "tag": {"type": "string"},
        "pullPolicy": {"enum": ["always", "missing"]}
      },
      "required": ["tag"]
    },
    "port": {"type": "integer", "minimum": 1, "maximum": 65535},
    "properties": {"type": "object", "properties": {"x": {"type": "string"}}}
  },
  "required": ["image"]
}`

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		values  string
		strict  bool
		wantErr string
	}{
		{"valid", "image: {tag: '7.4'}\nport: 6379\n", false, ""},
		{"missing required", "port: 1\n", false, "image"},
		{"nested required", "image: {}\n", false, "/image"},
		{"wrong type", "image: {tag: 7}\n", false, "/image/tag"},
		{"enum", "image: {tag: a, pullPolicy: never}\n", false, "/image/pullPolicy"},
		{"maximum", "image: {tag: a}\nport: 70000\n", false, "/port"},
		{"unknown allowed without strict", "image: {tag: a, typo: 1}\nextra: 1\n", false, ""},
		{"unknown nested rejected with strict", "image: {tag: a, typo: 1}\n", true, "typo"},
		{"unknown top-level rejected with strict", "image: {tag: a}\nextra: 1\n", true, "extra"},
		{"property named properties is a property", "image: {tag: a}\nproperties: {x: y}\n", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate([]byte(testSchema), mustDecode(t, tt.values), tt.strict)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateNoSchema(t *testing.T) {
	if err := Validate(nil, map[string]any{"a": 1}, true); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsExternalRef(t *testing.T) {
	for _, ref := range []string{"file:///etc/passwd", "https://example.com/s.json", "other.json"} {
		schema := `{"properties": {"a": {"$ref": "` + ref + `"}}}`
		err := Validate([]byte(schema), map[string]any{"a": 1}, false)
		if err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("$ref %s: want 'not allowed' error, got %v", ref, err)
		}
	}
}

func TestValidateLocalRef(t *testing.T) {
	schema := `{"$defs": {"tag": {"type": "string"}}, "properties": {"tag": {"$ref": "#/$defs/tag"}}}`
	if err := Validate([]byte(schema), map[string]any{"tag": 1}, false); err == nil {
		t.Fatal("expected type error through local $ref")
	}
}

func TestValidateInvalidSchema(t *testing.T) {
	if err := Validate([]byte(`{"type": 5}`), map[string]any{}, false); err == nil {
		t.Fatal("expected schema compile error")
	}
}
