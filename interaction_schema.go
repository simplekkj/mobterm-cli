package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const interactionFrameLimit = 64 * 1024
const interactionRequestLimit = 24 * 1024
const interactionAnswerLimit = 8 * 1024

type InteractionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type InteractionCondition struct {
	FieldID string          `json:"field_id"`
	Op      string          `json:"op"`
	Value   json.RawMessage `json:"value"`
}
type InteractionField struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Label       string                 `json:"label"`
	Description string                 `json:"description,omitempty"`
	Required    bool                   `json:"required"`
	Options     []InteractionOption    `json:"options,omitempty"`
	Custom      bool                   `json:"custom,omitempty"`
	MinLength   *int                   `json:"min_length,omitempty"`
	MaxLength   *int                   `json:"max_length,omitempty"`
	Minimum     *float64               `json:"minimum,omitempty"`
	Maximum     *float64               `json:"maximum,omitempty"`
	MinItems    *int                   `json:"min_items,omitempty"`
	MaxItems    *int                   `json:"max_items,omitempty"`
	LengthUnit  string                 `json:"length_unit,omitempty"`
	Format      string                 `json:"format,omitempty"`
	When        []InteractionCondition `json:"when,omitempty"`
}
type InteractionSpec struct {
	OwnerPID         int                `json:"-"`
	OwnerBirth       string             `json:"-"`
	V                int                `json:"v"`
	RequestID        string             `json:"request_id"`
	Agent            string             `json:"agent"`
	AgentVersion     string             `json:"agent_version"`
	AdapterVersion   int                `json:"adapter_version"`
	Kind             string             `json:"kind"`
	SessionID        string             `json:"session_id"`
	RunID            string             `json:"run_id"`
	TurnID           string             `json:"turn_id,omitempty"`
	NativeID         string             `json:"native_id,omitempty"`
	Locator          Locator            `json:"locator"`
	ExpiresAt        int64              `json:"expires_at"`
	CreatedAt        int64              `json:"created_at"`
	RuntimeIdentity  string             `json:"runtime_identity"`
	PID              int                `json:"pid"`
	Birth            string             `json:"birth"`
	ParentPID        int                `json:"parent_pid"`
	ParentBirth      string             `json:"parent_birth"`
	Title            string             `json:"title,omitempty"`
	Message          string             `json:"message,omitempty"`
	ToolName         string             `json:"tool_name,omitempty"`
	ToolInput        json.RawMessage    `json:"tool_input,omitempty"`
	Cwd              string             `json:"cwd,omitempty"`
	Fields           []InteractionField `json:"fields"`
	Scope            string             `json:"scope,omitempty"`
	NativeInput      json.RawMessage    `json:"native_input,omitempty"`
	NativeMetadata   json.RawMessage    `json:"native_metadata,omitempty"`
	SupportedActions []string           `json:"supported_actions"`
	TTL              time.Duration      `json:"-"`
}

// Check all object levels before decoding into a type. encoding/json alone accepts
// repeated keys, invalid UTF-8 and trailing values, none of which is a wire contract.
func interactionJSON(raw []byte, dst any) error {
	if !utf8.Valid(raw) || !interactionSurrogatesValid(raw) {
		return errors.New("invalid_interaction_json")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var scan func(int) error
	scan = func(depth int) error {
		if depth > 32 {
			return errors.New("interaction_json_depth")
		}
		t, e := d.Token()
		if e != nil {
			return e
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					key, ok := k.(string)
					if !ok || seen[key] {
						return errors.New("duplicate_interaction_key")
					}
					seen[key] = true
					if e = scan(depth + 1); e != nil {
						return e
					}
				}
				t, e = d.Token()
				if e != nil || t != json.Delim('}') {
					return errors.New("invalid_interaction_json")
				}
			case '[':
				for d.More() {
					if e = scan(depth + 1); e != nil {
						return e
					}
				}
				t, e = d.Token()
				if e != nil || t != json.Delim(']') {
					return errors.New("invalid_interaction_json")
				}
			default:
				return errors.New("invalid_interaction_json")
			}
		}
		return nil
	}
	if err := scan(0); err != nil {
		return errors.New("invalid_interaction_json")
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("invalid_interaction_json")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return errors.New("invalid_interaction_json")
	}
	return nil
}
func interactionKind(kind string) bool {
	switch kind {
	case "approval", "question", "form", "elicitation", "planApproval", "followupMessage":
		return true
	}
	return false
}
func interactionAgent(agent string) bool {
	return agent == "codex" || agent == "claude" || agent == "opencode" || agent == "pi"
}
func interactionAction(kind, action string) bool {
	if action == "release" {
		return true
	}
	switch kind {
	case "approval", "planApproval":
		return action == "allow_once" || action == "deny"
	case "question", "form":
		return action == "answer" || action == "cancel"
	case "elicitation":
		return action == "accept" || action == "decline" || action == "cancel"
	case "followupMessage":
		return action == "send_message"
	}
	return false
}
func interactionSchema(fields []InteractionField) error {
	if len(fields) > 64 {
		return errors.New("unsupported_interaction_schema")
	}
	seen := map[string]InteractionField{}
	options := 0
	for _, f := range fields {
		if f.ID == "" || len(f.ID) > 256 || f.Label == "" {
			return errors.New("unsupported_interaction_schema")
		}
		if _, ok := seen[f.ID]; ok {
			return errors.New("unsupported_interaction_schema")
		}
		switch f.Type {
		case "string", "number", "integer", "boolean", "multiselect", "external":
		default:
			return errors.New("unsupported_interaction_schema")
		}
		if f.LengthUnit != "" && f.LengthUnit != "unicode" && f.LengthUnit != "utf16" {
			return errors.New("unsupported_interaction_schema")
		}
		switch f.Format {
		case "", "email", "uri", "date", "date-time":
		default:
			return errors.New("unsupported_interaction_schema")
		}
		if f.Type == "external" && !f.Required {
			return errors.New("unsupported_interaction_schema")
		}
		if (f.MinLength != nil || f.MaxLength != nil || f.LengthUnit != "") && f.Type != "string" && f.Type != "multiselect" || (f.MinItems != nil || f.MaxItems != nil) && f.Type != "multiselect" || (f.Minimum != nil || f.Maximum != nil) && f.Type != "number" && f.Type != "integer" {
			return errors.New("unsupported_interaction_schema")
		}
		if f.Format != "" && f.Type != "string" || len(f.Options) > 0 && f.Type != "string" && f.Type != "multiselect" || f.Custom && f.Type != "string" && f.Type != "multiselect" {
			return errors.New("unsupported_interaction_schema")
		}
		for _, p := range []*int{f.MinLength, f.MaxLength, f.MinItems, f.MaxItems} {
			if p != nil && (*p < 0 || *p > 8192) {
				return errors.New("unsupported_interaction_schema")
			}
		}
		if f.MinLength != nil && f.MaxLength != nil && *f.MinLength > *f.MaxLength || f.MinItems != nil && f.MaxItems != nil && *f.MinItems > *f.MaxItems || f.Minimum != nil && f.Maximum != nil && *f.Minimum > *f.Maximum {
			return errors.New("unsupported_interaction_schema")
		}
		for _, p := range []*float64{f.Minimum, f.Maximum} {
			if p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0) || math.Abs(*p) > 9007199254740991) {
				return errors.New("unsupported_interaction_schema")
			}
		}
		ids := map[string]bool{}
		for _, o := range f.Options {
			if o.ID == "" || ids[o.ID] {
				return errors.New("unsupported_interaction_schema")
			}
			ids[o.ID] = true
			options++
		}
		for _, c := range f.When {
			prior, ok := seen[c.FieldID]
			if !ok || (c.Op != "eq" && c.Op != "neq") {
				return errors.New("unsupported_interaction_schema")
			}
			var v any
			if prior.Type == "multiselect" {
				prior.Type = "string"
				prior.MinLength = nil
				prior.MaxLength = nil
			}
			if interactionJSON(c.Value, &v) != nil || interactionFieldValue(prior, v) != nil {
				return errors.New("unsupported_interaction_schema")
			}
		}
		seen[f.ID] = f
	}
	if options > 256 {
		return errors.New("unsupported_interaction_schema")
	}
	return nil
}
func interactionNumber(n json.Number) (float64, error) {
	s := string(n)
	if strings.ContainsAny(s, "eE") {
		return 0, errors.New("unsupported_number")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 1 && len(parts[1]) > 6 {
		return 0, errors.New("unsupported_number")
	}
	f, e := n.Float64()
	if e != nil || math.Abs(f) > 9007199254740991 {
		return 0, errors.New("unsupported_number")
	}
	a, ok := new(big.Rat).SetString(s)
	b, ok2 := new(big.Rat).SetString(strconv.FormatFloat(f, 'f', -1, 64))
	if !ok || !ok2 || a.Cmp(b) != 0 {
		return 0, errors.New("unsupported_number")
	}
	return f, nil
}
func interactionFieldValue(f InteractionField, v any) error {
	fail := errors.New("invalid_interaction_answer")
	validString := func(s string) bool {
		n := utf8.RuneCountInString(s)
		if f.LengthUnit == "utf16" {
			n = len(utf16.Encode([]rune(s)))
		}
		if f.MinLength != nil && n < *f.MinLength || f.MaxLength != nil && n > *f.MaxLength {
			return false
		}
		if len(f.Options) > 0 && !f.Custom {
			found := false
			for _, o := range f.Options {
				found = found || o.ID == s
			}
			if !found {
				return false
			}
		}
		switch f.Format {
		case "email":
			return regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`).MatchString(s)
		case "uri":
			u, e := url.Parse(s)
			return e == nil && u.IsAbs() && !strings.ContainsAny(s, "\r\n\t ")
		case "date":
			_, e := time.Parse("2006-01-02", s)
			return e == nil
		case "date-time":
			_, e := time.Parse(time.RFC3339, s)
			return e == nil
		}
		return true
	}
	switch f.Type {
	case "string":
		s, ok := v.(string)
		if !ok || !validString(s) {
			return fail
		}
	case "external":
		if b, ok := v.(bool); !ok || !b {
			return fail
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fail
		}
	case "number", "integer":
		n, ok := v.(json.Number)
		if !ok {
			return fail
		}
		x, e := interactionNumber(n)
		if e != nil || f.Type == "integer" && math.Trunc(x) != x || f.Minimum != nil && x < *f.Minimum || f.Maximum != nil && x > *f.Maximum {
			return fail
		}
	case "multiselect":
		a, ok := v.([]any)
		if !ok || f.MinItems != nil && len(a) < *f.MinItems || f.MaxItems != nil && len(a) > *f.MaxItems {
			return fail
		}
		seen := map[string]bool{}
		for _, v := range a {
			s, ok := v.(string)
			if !ok || seen[s] || !validString(s) {
				return fail
			}
			seen[s] = true
		}
	default:
		return fail
	}
	return nil
}
func interactionAnswers(spec InteractionSpec, action, raw string) error {
	if !interactionAction(spec.Kind, action) {
		return errors.New("invalid_interaction_action")
	}
	allowed := action == "release"
	for _, a := range spec.SupportedActions {
		allowed = allowed || a == action
	}
	if !allowed {
		return errors.New("invalid_interaction_action")
	}
	if len(raw) > interactionAnswerLimit {
		return errors.New("interaction_answer_too_large")
	}
	var response struct {
		Answers map[string]any `json:"answers,omitempty"`
	}
	if interactionJSON([]byte(raw), &response) != nil {
		return errors.New("invalid_interaction_answer")
	}
	if action != "answer" && action != "accept" && action != "send_message" {
		if len(response.Answers) > 0 {
			return errors.New("unexpected_interaction_answer")
		}
		return nil
	}
	known := map[string]bool{}
	active := map[string]bool{}
	for _, f := range spec.Fields {
		enabled := true
		for _, c := range f.When {
			value, present := response.Answers[c.FieldID]
			if !active[c.FieldID] || !present {
				enabled = false
				continue
			}
			var expected any
			_ = interactionJSON(c.Value, &expected)
			equal := interactionValueEqual(value, expected)
			if values, ok := value.([]any); ok {
				equal = false
				for _, item := range values {
					equal = equal || interactionValueEqual(item, expected)
				}
			}
			if c.Op == "eq" && !equal || c.Op == "neq" && equal {
				enabled = false
			}
		}
		active[f.ID] = enabled
		known[f.ID] = true
		v, present := response.Answers[f.ID]
		if !enabled {
			if present {
				return errors.New("inactive_interaction_field")
			}
			continue
		}
		if !present {
			if f.Required {
				return errors.New("missing_interaction_answer")
			}
			continue
		}
		if err := interactionFieldValue(f, v); err != nil {
			return err
		}
	}
	for k := range response.Answers {
		if !known[k] {
			return errors.New("unknown_interaction_field")
		}
	}
	return nil
}

// JSON's Unicode escapes must encode scalar values. Go would otherwise replace
// lone surrogates with U+FFFD while another client may reject or preserve them.
func interactionSurrogatesValid(raw []byte) bool {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xDC00 && n <= 0xDFFF {
			return false
		}
		if n < 0xD800 || n > 0xDBFF {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return false
		}
		low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if e != nil || low < 0xDC00 || low > 0xDFFF {
			return false
		}
		i += 6
	}
	return true
}

func interactionValueEqual(a, b any) bool {
	if an, ok := a.(json.Number); ok {
		bn, ok := b.(json.Number)
		if !ok {
			return false
		}
		ar, aok := new(big.Rat).SetString(string(an))
		br, bok := new(big.Rat).SetString(string(bn))
		return aok && bok && ar.Cmp(br) == 0
	}
	left, e := json.Marshal(a)
	right, e2 := json.Marshal(b)
	return e == nil && e2 == nil && bytes.Equal(left, right)
}
