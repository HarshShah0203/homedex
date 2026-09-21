package nginx

import (
	"bytes"
	"fmt"
)

// directive is one parsed nginx directive. Pruning happens as each directive
// completes, so header values, auth files and map keys never outlive the
// tokenizer: only keepArgs names keep arguments, only keepChildren blocks keep
// children.
type directive struct {
	name    string
	args    []string
	file    string
	line    int
	isBlock bool
	block   []*directive
}

var keepArgs = map[string]bool{"include": true, "server_name": true, "listen": true, "location": true, "proxy_pass": true, "set": true, "upstream": true, "server": true, "ssl": true}

// Includes inside any other block (if, map, types, stream, ...) are dropped
// with its children and so never followed.
var keepChildren = map[string]bool{"http": true, "server": true, "location": true, "upstream": true}

const (
	tokEnd = iota
	tokOpen
	tokClose
	tokEOF
)

// parser mirrors nginx's ngx_conf_read_token so a config nginx accepts reads
// the same way here: '#' is a comment only at a token start, '}' inside a bare
// word is literal, and "${" does not open a block.
type parser struct {
	src     []byte
	pos     int
	line    int
	file    string
	lim     limits
	args    []string
	argLine int
}

func parse(src []byte, file string, lim limits) ([]*directive, error) {
	p := &parser{src: src, line: 1, file: file, lim: lim}
	return p.block(0)
}

// Messages are fixed strings: they never quote the config text around them.
func (p *parser) errf(line int, msg string) error {
	return fmt.Errorf("nginx: %s:%d: %s", p.file, line, msg)
}

func (p *parser) block(depth int) ([]*directive, error) {
	var out []*directive
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		switch tok {
		case tokClose:
			if depth == 0 {
				return nil, p.errf(p.line, `unexpected "}"`)
			}
			return out, nil
		case tokEOF:
			if depth > 0 {
				return nil, p.errf(p.line, `unexpected end of file, expecting "}"`)
			}
			return out, nil
		}
		d := &directive{name: p.args[0], file: p.file, line: p.argLine}
		if keepArgs[d.name] {
			d.args = p.args[1:]
		}
		if tok == tokOpen {
			if depth >= p.lim.blockDepth {
				return nil, p.errf(p.line, fmt.Sprintf("blocks nested deeper than %d", p.lim.blockDepth))
			}
			children, err := p.block(depth + 1)
			if err != nil {
				return nil, err
			}
			d.isBlock = true
			if keepChildren[d.name] {
				d.block = children
			}
		}
		out = append(out, d)
	}
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// next reads one directive's words into p.args and reports what ended it.
func (p *parser) next() (int, error) {
	p.args = nil
	lastSpace := true
	var needSpace, comment, escaped, variable, sq, dq bool
	var start, startLine int
	for {
		if p.pos >= len(p.src) {
			if sq || dq {
				return 0, p.errf(startLine, "unterminated quoted string")
			}
			if len(p.args) > 0 || !lastSpace {
				return 0, p.errf(p.line, `unexpected end of file, expecting ";" or "}"`)
			}
			return tokEOF, nil
		}
		ch := p.src[p.pos]
		p.pos++
		if ch == '\n' {
			p.line++
			comment = false
		}
		if comment {
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		if needSpace {
			switch {
			case isSpace(ch):
				lastSpace, needSpace = true, false
				continue
			case ch == ';':
				return tokEnd, nil
			case ch == '{':
				return tokOpen, nil
			case ch == ')':
				lastSpace, needSpace = true, false
			default:
				return 0, p.errf(p.line, "unexpected character after quoted string")
			}
		}
		if lastSpace {
			start, startLine = p.pos-1, p.line
			switch ch {
			case ' ', '\t', '\r', '\n':
			case ';', '{':
				if len(p.args) == 0 {
					return 0, p.errf(p.line, fmt.Sprintf(`unexpected "%c"`, ch))
				}
				if ch == '{' {
					return tokOpen, nil
				}
				return tokEnd, nil
			case '}':
				if len(p.args) > 0 {
					return 0, p.errf(p.line, `unexpected "}"`)
				}
				return tokClose, nil
			case '#':
				comment = true
			case '\\':
				escaped, lastSpace = true, false
			case '"':
				start++
				dq, lastSpace = true, false
			case '\'':
				start++
				sq, lastSpace = true, false
			case '$':
				variable, lastSpace = true, false
			default:
				lastSpace = false
			}
			continue
		}
		if p.pos-1-start > p.lim.tokenBytes {
			return 0, p.errf(startLine, fmt.Sprintf("token longer than %d bytes", p.lim.tokenBytes))
		}
		if ch == '{' && variable {
			continue
		}
		variable = false
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '$' {
			variable = true
			continue
		}
		found := false
		switch {
		case dq:
			if ch == '"' {
				dq, needSpace, found = false, true, true
			}
		case sq:
			if ch == '\'' {
				sq, needSpace, found = false, true, true
			}
		case isSpace(ch) || ch == ';' || ch == '{':
			lastSpace, found = true, true
		}
		if !found {
			continue
		}
		if len(p.args) == 0 {
			p.argLine = startLine
		}
		p.args = append(p.args, unescape(p.src[start:p.pos-1]))
		switch ch {
		case ';':
			return tokEnd, nil
		case '{':
			return tokOpen, nil
		}
	}
}

// unescape applies nginx's escape rules: \" \' \\ become the character, \t \r
// \n become control characters, and any other pair is kept as written.
func unescape(raw []byte) string {
	if bytes.IndexByte(raw, '\\') < 0 {
		return string(raw)
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '\\' && i+1 < len(raw) {
			switch raw[i+1] {
			case '"', '\'', '\\':
				i++
				c = raw[i]
			case 't':
				i++
				c = '\t'
			case 'r':
				i++
				c = '\r'
			case 'n':
				i++
				c = '\n'
			}
		}
		out = append(out, c)
	}
	return string(out)
}
