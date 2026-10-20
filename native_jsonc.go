package main

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Positions refer to the original document. Parsing never rewrites comments,
// whitespace or unrelated values; edits only insert/remove the owned array item.
type jsoncNode struct {
	start, end int
	kind       byte
	text       string
	children   []*jsoncNode
	keys       map[string]*jsoncNode
	commas     []int
}
type jsoncParser struct {
	data []byte
	pos  int
}

func (p *jsoncParser) space() error {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		case '/':
			if p.pos+1 >= len(p.data) {
				return errors.New("invalid JSONC comment")
			}
			if p.data[p.pos+1] == '/' {
				p.pos += 2
				for p.pos < len(p.data) && p.data[p.pos] != '\n' {
					p.pos++
				}
			} else if p.data[p.pos+1] == '*' {
				end := bytes.Index(p.data[p.pos+2:], []byte("*/"))
				if end < 0 {
					return errors.New("unterminated JSONC comment")
				}
				p.pos += end + 4
			} else {
				return errors.New("invalid JSONC comment")
			}
		default:
			return nil
		}
	}
	return nil
}
func (p *jsoncParser) value(depth int) (*jsoncNode, error) {
	if err := p.space(); err != nil {
		return nil, err
	}
	if depth > 64 || p.pos >= len(p.data) {
		return nil, errors.New("invalid JSONC value")
	}
	n := &jsoncNode{start: p.pos, kind: p.data[p.pos]}
	p.pos++
	switch n.kind {
	case '"':
		for p.pos < len(p.data) {
			c := p.data[p.pos]
			p.pos++
			if c == '\\' {
				p.pos++
				continue
			}
			if c == '"' {
				n.end = p.pos
				if json.Unmarshal(p.data[n.start:n.end], &n.text) != nil {
					return nil, errors.New("invalid JSONC string")
				}
				return n, nil
			}
		}
		return nil, errors.New("unterminated JSONC string")
	case '{', '[':
		closing := byte(']')
		if n.kind == '{' {
			closing = '}'
			n.keys = map[string]*jsoncNode{}
		}
		for {
			if err := p.space(); err != nil {
				return nil, err
			}
			if p.pos >= len(p.data) {
				return nil, errors.New("unterminated JSONC container")
			}
			if p.data[p.pos] == closing {
				p.pos++
				n.end = p.pos
				return n, nil
			}
			key := ""
			if n.kind == '{' {
				k, err := p.value(depth + 1)
				if err != nil {
					return nil, err
				}
				if k.kind != '"' {
					return nil, errors.New("invalid JSONC object key")
				}
				key = k.text
				if _, ok := n.keys[key]; ok {
					return nil, errors.New("duplicate JSONC object key")
				}
				if err = p.space(); err != nil {
					return nil, err
				}
				if p.pos >= len(p.data) || p.data[p.pos] != ':' {
					return nil, errors.New("invalid JSONC object")
				}
				p.pos++
			}
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.children = append(n.children, v)
			if n.kind == '{' {
				n.keys[key] = v
			}
			if err = p.space(); err != nil {
				return nil, err
			}
			if p.pos >= len(p.data) {
				return nil, errors.New("unterminated JSONC container")
			}
			if p.data[p.pos] == closing {
				p.pos++
				n.end = p.pos
				return n, nil
			}
			if p.data[p.pos] != ',' {
				return nil, errors.New("invalid JSONC separator")
			}
			n.commas = append(n.commas, p.pos)
			p.pos++
		}
	default:
		for p.pos < len(p.data) && !bytes.ContainsRune([]byte(" \t\r\n,]} /"), rune(p.data[p.pos])) {
			p.pos++
		}
		n.end = p.pos
		if !json.Valid(p.data[n.start:n.end]) {
			return nil, errors.New("invalid JSONC scalar")
		}
		return n, nil
	}
}
func parseJSONC(data []byte) (*jsoncNode, error) {
	p := jsoncParser{data: data}
	n, err := p.value(0)
	if err != nil {
		return nil, err
	}
	if err = p.space(); err != nil {
		return nil, err
	}
	if p.pos != len(data) || n.kind != '{' {
		return nil, errors.New("OpenCode config must be a JSONC object")
	}
	return n, nil
}
func editNativePlugin(data []byte, reference, action string, owned, pending bool) ([]byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		if action == "uninstall" {
			if owned && !pending {
				return nil, errors.New("managed plugin reference removed; config unchanged")
			}
			return data, nil
		}
		data = []byte("{}\n")
	}
	root, err := parseJSONC(data)
	if err != nil {
		return nil, err
	}
	plugins := root.keys["plugins"]
	if plugins == nil {
		if action == "uninstall" {
			if owned && !pending {
				return nil, errors.New("managed plugin reference removed; config unchanged")
			}
			return data, nil
		}
		if owned && !pending {
			return nil, errors.New("managed plugin reference removed; config unchanged")
		}
		encoded, _ := json.Marshal(reference)
		addition := append([]byte("\"plugins\": ["), encoded...)
		addition = append(addition, ']')
		if len(root.keys) > 0 {
			addition = append(addition, ',')
		}
		out := append([]byte{}, data[:root.start+1]...)
		out = append(out, addition...)
		return append(out, data[root.start+1:]...), nil
	}
	if plugins.kind != '[' {
		return nil, errors.New("unsupported OpenCode plugins shape; config unchanged")
	}
	found := -1
	for i, item := range plugins.children {
		if item.kind == '{' {
			pkg := item.keys["package"]
			if pkg == nil || pkg.kind != '"' || len(item.keys) > 2 || (item.keys["options"] != nil && item.keys["options"].kind != '{') {
				return nil, errors.New("unsupported OpenCode plugin entry; config unchanged")
			}
			for key := range item.keys {
				if key != "package" && key != "options" {
					return nil, errors.New("unsupported OpenCode plugin entry; config unchanged")
				}
			}
			if pkg.text == reference {
				return nil, errors.New("managed plugin reference has user options or is not owned; config unchanged")
			}
			continue
		}
		if item.kind != '"' {
			return nil, errors.New("unsupported OpenCode plugin entry; config unchanged")
		}
		if item.text == reference {
			if found >= 0 {
				return nil, errors.New("duplicate managed plugin reference; config unchanged")
			}
			found = i
		}
	}
	if found >= 0 && !owned {
		return nil, errors.New("existing plugin reference is not owned; config unchanged")
	}
	if found < 0 {
		if owned && !pending {
			return nil, errors.New("managed plugin reference removed; config unchanged")
		}
		if action == "uninstall" {
			return data, nil
		}
		encoded, _ := json.Marshal(reference)
		at := plugins.end - 1
		// Add before the closing bracket; retain any existing trailing comma.
		if len(plugins.children) > 0 && len(plugins.commas) < len(plugins.children) {
			encoded = append([]byte(","), encoded...)
		}
		out := append([]byte{}, data[:at]...)
		out = append(out, encoded...)
		return append(out, data[at:]...), nil
	}
	if action == "install" {
		return data, nil
	}
	item := plugins.children[found]
	removeComma := -1
	if found < len(plugins.commas) {
		removeComma = plugins.commas[found]
	} else if found > 0 {
		removeComma = plugins.commas[found-1]
	}
	out := make([]byte, 0, len(data))
	for i, c := range data {
		if i >= item.start && i < item.end || i == removeComma {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
